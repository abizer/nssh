package clipboard

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func skip(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	if os.Getenv("NSSH_CLIPBOARD_TESTS") != "1" {
		t.Skip("set NSSH_CLIPBOARD_TESTS=1 to run (will clobber your clipboard)")
	}
}

func TestTextRoundTrip(t *testing.T) {
	skip(t)
	want := []byte("hello, nssh " + t.Name())
	if err := WriteText(want); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	got, err := ReadText()
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip: got %q want %q", got, want)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestImagePNGRoundTrip(t *testing.T) {
	skip(t)
	if _, err := exec.LookPath("pngpaste"); err != nil {
		t.Skip("pngpaste not installed; brew install pngpaste")
	}
	fixture := testPNG(t)
	if err := WriteImagePNG(fixture); err != nil {
		t.Fatalf("WriteImagePNG: %v", err)
	}
	got, err := ReadImagePNG()
	if err != nil {
		t.Fatalf("ReadImagePNG: %v", err)
	}
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if len(got) < 8 || !bytes.Equal(got[:8], pngMagic) {
		t.Fatalf("ReadImagePNG: not a PNG (len=%d)", len(got))
	}
}
