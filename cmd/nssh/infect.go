package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

// personas are the argv[0] names nssh answers to when symlinked.
var personas = []string{"xdg-open", "sensible-browser", "xclip", "wl-copy", "wl-paste"}

// version returns the module version embedded in the binary. For builds with
// -X main.buildVersion=... set, that wins. Otherwise we read debug.BuildInfo,
// which is "(devel)" or "+dirty" for local builds and a real tag for
// go-install builds from a tagged module.
func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Version
}

// latestReleaseTag queries the GitHub API for the most recent nssh release.
func latestReleaseTag() (string, error) {
	resp, err := http.Get("https://api.github.com/repos/abizer/nssh/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	const key = `"tag_name":"`
	i := strings.Index(string(body), key)
	if i < 0 {
		return "", fmt.Errorf("no tag_name in github response")
	}
	rest := string(body[i+len(key):])
	j := strings.Index(rest, `"`)
	if j < 0 {
		return "", fmt.Errorf("malformed tag_name")
	}
	return rest[:j], nil
}

// looksLikeSemver reports whether v is a clean "vX.Y.Z" tag (no +dirty etc).
func looksLikeSemver(v string) bool {
	if !strings.HasPrefix(v, "v") || strings.ContainsAny(v, "+ ") {
		return false
	}
	parts := strings.Split(v[1:], ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// detectLocalDesktop returns (true, reason) if a desktop session appears to be
// running on the local machine — via DISPLAY/WAYLAND_DISPLAY env, an X11 socket,
// or a Wayland socket. Returns (false, "") on headless systems.
func detectLocalDesktop() (bool, string) {
	if v := os.Getenv("DISPLAY"); v != "" {
		return true, "$DISPLAY=" + v
	}
	if v := os.Getenv("WAYLAND_DISPLAY"); v != "" {
		return true, "$WAYLAND_DISPLAY=" + v
	}
	if entries, err := os.ReadDir("/tmp/.X11-unix"); err == nil && len(entries) > 0 {
		return true, "/tmp/.X11-unix/" + entries[0].Name()
	}
	if matches, _ := filepath.Glob("/run/user/*/wayland-*"); len(matches) > 0 {
		return true, matches[0]
	}
	return false, ""
}

// detectRemoteDesktop runs the same check on the remote via SSH.
// Returns (true, reason) on any desktop signal, (false, "") on headless.
func detectRemoteDesktop(sshTarget string) (bool, string) {
	script := `
if [ -n "$DISPLAY" ]; then echo "DISPLAY=$DISPLAY"; exit 0; fi
if [ -n "$WAYLAND_DISPLAY" ]; then echo "WAYLAND_DISPLAY=$WAYLAND_DISPLAY"; exit 0; fi
ls /tmp/.X11-unix/ 2>/dev/null | head -1 | grep -q . && { ls /tmp/.X11-unix/ | head -1; exit 0; }
ls /run/user/*/wayland-* 2>/dev/null | head -1 | grep -q . && { ls /run/user/*/wayland-* | head -1; exit 0; }
exit 1
`
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "bash -l -s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.Output()
	if err != nil {
		// Exit 1 from our script = no desktop. ssh errors also end up here.
		return false, ""
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return false, ""
	}
	return true, line
}

// resolveRemoteArch runs `uname -sm` on the remote and maps to Go's GOOS/GOARCH.
func resolveRemoteArch(sshTarget string) (goos, goarch string, err error) {
	out, err := exec.Command("ssh", sshTarget, "uname -sm").Output()
	if err != nil {
		return "", "", fmt.Errorf("ssh uname: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected uname output: %q", out)
	}
	sys, mach := parts[0], parts[1]

	switch sys {
	case "Linux":
		goos = "linux"
	case "Darwin":
		goos = "darwin"
	default:
		return "", "", fmt.Errorf("unsupported OS: %s", sys)
	}
	switch mach {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	default:
		return "", "", fmt.Errorf("unsupported arch: %s", mach)
	}
	return goos, goarch, nil
}

// downloadBinary fetches nssh-<goos>-<goarch> from the given release tag,
// caching in ~/.cache/nssh/releases/<tag>/<goos>-<goarch>/nssh.
func downloadBinary(tag, goos, goarch string) (string, error) {
	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".cache", "nssh", "releases", tag, goos+"-"+goarch)
	cachePath := filepath.Join(cacheDir, "nssh")
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://github.com/abizer/nssh/releases/download/%s/nssh-%s-%s", tag, goos, goarch)
	fmt.Fprintf(os.Stderr, "nssh: downloading %s\n", url)

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: %s", resp.Status)
	}

	f, err := os.OpenFile(cachePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(cachePath)
		return "", err
	}
	return cachePath, nil
}

// probeRemoteVersion SSHes in (login shell for PATH) and runs `nssh --version`
// on the remote. Returns the version string and whether nssh is installed.
func probeRemoteVersion(sshTarget string) (ver string, installed bool) {
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", sshTarget,
		`bash -l -c 'command -v nssh >/dev/null 2>&1 && nssh --version 2>&1 | head -1'`,
	).Output()
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", false
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", false
	}
	return parts[1], true
}

// promptYes returns true if stdin is a TTY and the user answers yes.
func promptYes(msg string) bool {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", msg)
	var resp string
	fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}

// checkRemoteVersion probes the remote's nssh version and warns if missing
// or mismatched. Prompts to infect if on a TTY. Non-fatal on any error.
func checkRemoteVersion(sshTarget string) {
	localVer := version()
	if !looksLikeSemver(localVer) {
		return
	}
	remoteVer, installed := probeRemoteVersion(sshTarget)
	if !installed {
		fmt.Fprintln(os.Stderr, "nssh: not installed on remote — clipboard bridge will not work")
		if promptYes("  install it now?") {
			infectRemote(sshTarget, false)
		}
		return
	}
	if remoteVer != localVer {
		fmt.Fprintf(os.Stderr, "nssh: remote version %s, local %s\n", remoteVer, localVer)
		if promptYes("  update remote to " + localVer + "?") {
			infectRemote(sshTarget, false)
		}
	}
}

// infectSelf sets up the local machine: creates persona symlinks in
// ~/.local/bin pointing to the currently running nssh binary. Refuses on
// desktop systems (unless force=true) since symlinking xclip/xdg-open/etc
// would shadow the user's real clipboard tools.
//
// On macOS this is a no-op — Mac apps don't use xclip.
func infectSelf(force bool) {
	if runtime.GOOS == "darwin" {
		fmt.Fprintln(os.Stderr, "nssh: infect self on macOS — nothing to do (personas not needed)")
		return
	}

	if !force {
		if desktop, reason := detectLocalDesktop(); desktop {
			fmt.Fprintf(os.Stderr, "nssh: desktop environment detected (%s)\n", reason)
			fmt.Fprintln(os.Stderr, "nssh: refusing to symlink xclip/xdg-open/etc — would shadow your real clipboard tools")
			fmt.Fprintln(os.Stderr, "nssh: use `nssh infect self --force` to override")
			os.Exit(1)
		}
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: cannot resolve own path: %v\n", err)
		os.Exit(1)
	}
	// Resolve symlinks so personas point at the real store path (or wherever
	// the binary actually lives), not at intermediate symlinks that may move.
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	home, _ := os.UserHomeDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: mkdir %s: %v\n", localBin, err)
		os.Exit(1)
	}

	for _, p := range personas {
		linkPath := filepath.Join(localBin, p)
		_ = os.Remove(linkPath)
		if err := os.Symlink(self, linkPath); err != nil {
			fmt.Fprintf(os.Stderr, "nssh: symlink %s: %v\n", linkPath, err)
			continue
		}
	}

	fmt.Fprintf(os.Stderr, "nssh: infect self — symlinks in %s → %s\n", localBin, self)
}

// infectRemote installs nssh on the remote host and sets up persona symlinks.
// Checks that the remote isn't a desktop system first.
func infectRemote(sshTarget string, force bool) {
	if !force {
		if desktop, reason := detectRemoteDesktop(sshTarget); desktop {
			fmt.Fprintf(os.Stderr, "nssh: desktop environment on remote (%s)\n", reason)
			fmt.Fprintln(os.Stderr, "nssh: refusing to install — would shadow real clipboard tools")
			fmt.Fprintln(os.Stderr, "nssh: use `nssh infect --force <host>` to override")
			os.Exit(1)
		}
	}

	goos, goarch, err := resolveRemoteArch(sshTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "nssh: remote is %s/%s\n", goos, goarch)

	tag := version()
	if !looksLikeSemver(tag) {
		t, err := latestReleaseTag()
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: couldn't resolve release tag: %v\n", err)
			os.Exit(1)
		}
		tag = t
	}
	fmt.Fprintf(os.Stderr, "nssh: using release %s\n", tag)

	binPath, err := downloadBinary(tag, goos, goarch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "nssh: copying to %s:~/.local/bin/nssh\n", sshTarget)
	if err := exec.Command("ssh", sshTarget, "mkdir -p ~/.local/bin").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: mkdir: %v\n", err)
		os.Exit(1)
	}
	scp := exec.Command("scp", "-q", binPath, sshTarget+":.local/bin/nssh")
	scp.Stdout, scp.Stderr = os.Stdout, os.Stderr
	if err := scp.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: scp: %v\n", err)
		os.Exit(1)
	}

	// Let the freshly-installed nssh infect the remote itself — this keeps
	// the symlink list in one place (personas var here) and means nssh always
	// owns its own symlinks.
	cmd := exec.Command("ssh", sshTarget, "bash -l -c '~/.local/bin/nssh infect self --force'")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: remote infect self: %v\n", err)
		os.Exit(1)
	}

	// Sanity-check PATH ordering.
	out, _ := exec.Command("ssh", sshTarget, `bash -l -c 'command -v xclip'`).Output()
	resolved := strings.TrimSpace(string(out))
	if !strings.Contains(resolved, ".local/bin/xclip") {
		fmt.Fprintln(os.Stderr, "nssh: WARNING: ~/.local/bin/xclip is not first in PATH on the remote")
		if resolved != "" {
			fmt.Fprintf(os.Stderr, "  xclip resolves to: %s\n", resolved)
		}
		fmt.Fprintln(os.Stderr, "  add to ~/.bashrc or ~/.profile: export PATH=\"$HOME/.local/bin:$PATH\"")
	}

	fmt.Fprintln(os.Stderr, "nssh: infection complete")
}
