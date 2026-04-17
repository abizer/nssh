package clipboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// ReadText returns the current macOS clipboard text via pbpaste.
func ReadText() ([]byte, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return nil, fmt.Errorf("pbpaste: %w", err)
	}
	return out, nil
}

// WriteText replaces the macOS clipboard text via pbcopy.
func WriteText(data []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(data)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}
	return nil
}

// ReadImagePNG returns PNG bytes from the macOS clipboard using pngpaste.
func ReadImagePNG() ([]byte, error) {
	out, err := exec.Command("pngpaste", "-").Output()
	if err != nil {
		return nil, fmt.Errorf("pngpaste: %w (install via 'brew install pngpaste')", err)
	}
	return out, nil
}

// WriteImagePNG replaces the macOS clipboard with PNG bytes by writing them
// to a temp file and running osascript.
func WriteImagePNG(data []byte) error {
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
