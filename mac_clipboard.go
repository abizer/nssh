package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// readText returns the current macOS clipboard text via pbpaste.
func readText() ([]byte, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return nil, fmt.Errorf("pbpaste: %w", err)
	}
	return out, nil
}

// writeText replaces the macOS clipboard text via pbcopy.
func writeText(data []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(data)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}
	return nil
}

// readImagePNG returns PNG bytes from the macOS clipboard using pngpaste.
// pngpaste is required — install via `brew install pngpaste`. We deliberately
// skip the osascript «data PNGf» hex-unwrap fallback: it's fragile, and
// requiring pngpaste keeps the code path simple.
func readImagePNG() ([]byte, error) {
	out, err := exec.Command("pngpaste", "-").Output()
	if err != nil {
		return nil, fmt.Errorf("pngpaste: %w (install via 'brew install pngpaste')", err)
	}
	return out, nil
}

// writeImagePNG replaces the macOS clipboard with PNG bytes by writing them
// to a temp file and running osascript.
func writeImagePNG(data []byte) error {
	f, err := os.CreateTemp("", "nssh-clip-*.png")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	script := fmt.Sprintf(`set the clipboard to (read (POSIX file "%s") as «class PNGf»)`, f.Name())
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
