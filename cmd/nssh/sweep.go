package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// moshServer is one mosh-server process on the remote.
type moshServer struct {
	PID    int
	Uptime time.Duration
}

func sweepUsage() {
	fmt.Fprintln(os.Stderr, "usage: nssh sweep [--all|--older <dur>] <host>")
	fmt.Fprintln(os.Stderr, "  --all          kill every mosh-server owned by $USER on <host>")
	fmt.Fprintln(os.Stderr, "  --older <dur>  kill mosh-servers older than the given duration (e.g. 24h, 168h)")
	os.Exit(1)
}

// sweepCmd handles `nssh sweep [--all|--older <dur>] <host>`.
//
// Lists mosh-server processes owned by $USER on the remote, then either kills
// all of them (--all), kills those older than a threshold (--older), or
// interactively prompts. SIGTERM with a 2s grace period, then SIGKILL.
//
// Safe to use when running tmux-inside-mosh: killing mosh-server doesn't kill
// the tmux server, so detached sessions survive.
func sweepCmd(args []string) {
	all := false
	var olderThan time.Duration
	var target string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all":
			all = true
		case a == "--older":
			i++
			if i >= len(args) {
				sweepUsage()
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "nssh: invalid duration %q: %v\n", args[i], err)
				os.Exit(1)
			}
			olderThan = d
		case strings.HasPrefix(a, "--older="):
			d, err := time.ParseDuration(strings.TrimPrefix(a, "--older="))
			if err != nil {
				fmt.Fprintf(os.Stderr, "nssh: invalid duration %q: %v\n", a, err)
				os.Exit(1)
			}
			olderThan = d
		case a == "-h", a == "--help":
			sweepUsage()
		default:
			if target != "" {
				fmt.Fprintf(os.Stderr, "nssh: unexpected arg %q\n", a)
				os.Exit(1)
			}
			target = a
		}
	}
	if target == "" {
		sweepUsage()
	}
	if all && olderThan > 0 {
		fmt.Fprintln(os.Stderr, "nssh: --all and --older are mutually exclusive")
		os.Exit(1)
	}

	servers, err := listRemoteMoshServers(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: list mosh-servers on %s: %v\n", target, err)
		os.Exit(1)
	}
	if len(servers) == 0 {
		fmt.Printf("no mosh-server processes for $USER on %s\n", target)
		return
	}

	fmt.Printf("%d mosh-server process(es) on %s:\n", len(servers), target)
	fmt.Printf("  %-8s %s\n", "PID", "UPTIME")
	for _, s := range servers {
		fmt.Printf("  %-8d %s\n", s.PID, shortDuration(s.Uptime))
	}

	var toKill []int
	switch {
	case all:
		for _, s := range servers {
			toKill = append(toKill, s.PID)
		}
	case olderThan > 0:
		for _, s := range servers {
			if s.Uptime > olderThan {
				toKill = append(toKill, s.PID)
			}
		}
		if len(toKill) == 0 {
			fmt.Printf("no mosh-servers older than %s\n", olderThan)
			return
		}
	default:
		toKill = promptSweepSelection(servers)
		if len(toKill) == 0 {
			return
		}
	}

	pidStrs := make([]string, len(toKill))
	for i, p := range toKill {
		pidStrs[i] = strconv.Itoa(p)
	}
	fmt.Printf("nssh: sending SIGTERM to %s on %s…\n", strings.Join(pidStrs, ", "), target)
	if err := killRemotePIDs(target, "TERM", pidStrs); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: kill -TERM: %v\n", err)
		os.Exit(1)
	}

	// Give them a couple seconds to exit cleanly, then re-check and escalate.
	time.Sleep(2 * time.Second)
	survivors, err := listRemoteMoshServers(target)
	if err != nil {
		return // partial success is still success; don't error out
	}
	stillAlive := pidsStillIn(toKill, survivors)
	if len(stillAlive) == 0 {
		return
	}
	pidStrs = pidStrs[:0]
	for _, p := range stillAlive {
		pidStrs = append(pidStrs, strconv.Itoa(p))
	}
	fmt.Fprintf(os.Stderr, "nssh: %d survived SIGTERM, sending SIGKILL: %s\n", len(stillAlive), strings.Join(pidStrs, ", "))
	if err := killRemotePIDs(target, "KILL", pidStrs); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: kill -KILL: %v\n", err)
		os.Exit(1)
	}
}

// listRemoteMoshServers SSHs to target and returns mosh-server processes
// owned by the user, sorted oldest-first. Parses `ps`'s portable etime
// format ([[DD-]hh:]mm:ss) since not every system's procps supports etimes.
func listRemoteMoshServers(target string) ([]moshServer, error) {
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", target,
		`ps -u "$USER" -o pid=,etime=,comm= 2>/dev/null | awk '$3=="mosh-server"{print $1, $2}'`)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var servers []moshServer
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		dur, err := parseEtime(parts[1])
		if err != nil {
			continue
		}
		servers = append(servers, moshServer{PID: pid, Uptime: dur})
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].Uptime > servers[j].Uptime // oldest first
	})
	return servers, nil
}

// parseEtime parses ps `etime` format: [[DD-]hh:]mm:ss. Returns the duration.
func parseEtime(s string) (time.Duration, error) {
	var days int
	if i := strings.Index(s, "-"); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0, fmt.Errorf("etime days %q: %w", s, err)
		}
		days = d
		s = s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("etime %q: unexpected format", s)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, fmt.Errorf("etime %q: %w", s, err)
		}
		nums[i] = n
	}
	var hours, minutes, seconds int
	switch len(nums) {
	case 3:
		hours, minutes, seconds = nums[0], nums[1], nums[2]
	case 2:
		minutes, seconds = nums[0], nums[1]
	}
	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second, nil
}

// promptSweepSelection asks the user which PIDs to kill. Accepts:
//   - "all" or "a": every server in the list
//   - "old": every server older than 24h
//   - comma- or space-separated PIDs
//   - empty: skip (kill nothing)
func promptSweepSelection(servers []moshServer) []int {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		fmt.Fprintln(os.Stderr, "nssh: non-interactive; pass --all or --older to choose")
		return nil
	}
	fmt.Print("kill which? (PIDs, \"all\", \"old\" for >24h, empty to skip): ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	switch strings.ToLower(line) {
	case "all", "a":
		out := make([]int, len(servers))
		for i, s := range servers {
			out[i] = s.PID
		}
		return out
	case "old", "o":
		var out []int
		for _, s := range servers {
			if s.Uptime > 24*time.Hour {
				out = append(out, s.PID)
			}
		}
		return out
	}
	wanted := map[int]bool{}
	for _, tok := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: ignoring unparseable token %q\n", tok)
			continue
		}
		wanted[n] = true
	}
	var out []int
	for _, s := range servers {
		if wanted[s.PID] {
			out = append(out, s.PID)
			delete(wanted, s.PID)
		}
	}
	for p := range wanted {
		fmt.Fprintf(os.Stderr, "nssh: PID %d not in the list, ignoring\n", p)
	}
	return out
}

// killRemotePIDs runs `kill -<sig> <pid>...` on the remote.
func killRemotePIDs(target, sig string, pids []string) error {
	args := append([]string{"-o", "BatchMode=yes", target, "kill", "-" + sig}, pids...)
	cmd := exec.Command("ssh", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// pidsStillIn returns the subset of `pids` whose values appear in `servers`.
func pidsStillIn(pids []int, servers []moshServer) []int {
	alive := map[int]bool{}
	for _, s := range servers {
		alive[s.PID] = true
	}
	var out []int
	for _, p := range pids {
		if alive[p] {
			out = append(out, p)
		}
	}
	return out
}
