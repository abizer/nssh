package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// version returns the module version embedded in the binary. For builds from
// go install or go build with a tagged module, this is "v1.2.3"; for untagged
// builds (local dev) it returns "(devel)" and we fall back to the latest
// release on GitHub.
func version() string {
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
	// Cheap JSON extraction — avoids pulling in encoding/json for one field.
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

func infect(sshTarget string) {
	// 1. Detect remote arch.
	goos, goarch, err := resolveRemoteArch(sshTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "nssh: remote is %s/%s\n", goos, goarch)

	// 2. Resolve release tag.
	tag := version()
	if tag == "" || tag == "(devel)" {
		t, err := latestReleaseTag()
		if err != nil {
			fmt.Fprintf(os.Stderr, "nssh: couldn't resolve release tag: %v\n", err)
			os.Exit(1)
		}
		tag = t
	}
	fmt.Fprintf(os.Stderr, "nssh: using release %s\n", tag)

	// 3. Download binary (cached).
	binPath, err := downloadBinary(tag, goos, goarch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nssh: %v\n", err)
		os.Exit(1)
	}

	// 4. scp to remote.
	fmt.Fprintf(os.Stderr, "nssh: copying to %s:~/.local/bin/nssh\n", sshTarget)
	// Ensure ~/.local/bin exists first.
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

	// 5. Create symlinks on the remote.
	symlinkScript := `
set -e
chmod +x ~/.local/bin/nssh
for name in xdg-open sensible-browser xclip wl-copy wl-paste; do
  ln -sf ~/.local/bin/nssh ~/.local/bin/"$name"
done
`
	cmd := exec.Command("ssh", sshTarget, "bash", "-s")
	cmd.Stdin = strings.NewReader(symlinkScript)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "nssh: symlink: %v\n", err)
		os.Exit(1)
	}

	// 6. Sanity-check that our xclip shim resolves first in an interactive
	// login shell. A non-interactive `ssh host 'echo $PATH'` doesn't source
	// bashrc/profile, so it would lie — hence -l (login shell) here.
	out, _ := exec.Command("ssh", sshTarget, "bash", "-l", "-c", "command -v xclip").Output()
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
