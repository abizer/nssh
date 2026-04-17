package main

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

// These tests clobber the user's real macOS clipboard. They run only when
// NSSH_CLIPBOARD_TESTS=1 and GOOS=darwin.
func skipUnlessClipboard(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	if os.Getenv("NSSH_CLIPBOARD_TESTS") != "1" {
		t.Skip("set NSSH_CLIPBOARD_TESTS=1 to run (will clobber your clipboard)")
	}
}

func TestTextRoundTrip(t *testing.T) {
	skipUnlessClipboard(t)
	want := []byte("hello, nssh " + t.Name())
	if err := writeText(want); err != nil {
		t.Fatalf("writeText: %v", err)
	}
	got, err := readText()
	if err != nil {
		t.Fatalf("readText: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip: got %q want %q", got, want)
	}
}

// testPNG returns a valid 10×10 solid-red RGBA PNG. We can't use a 1×1 PNG
// because macOS CGImageDestinationFinalize fails on images that small.
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
	skipUnlessClipboard(t)
	if _, err := exec.LookPath("pngpaste"); err != nil {
		t.Skip("pngpaste not installed; brew install pngpaste")
	}
	fixture := testPNG(t)
	if err := writeImagePNG(fixture); err != nil {
		t.Fatalf("writeImagePNG: %v", err)
	}
	got, err := readImagePNG()
	if err != nil {
		t.Fatalf("readImagePNG: %v", err)
	}
	// writeImagePNG roundtrips via osascript «class PNGf» which may re-encode.
	// Verify the output is a valid PNG (magic bytes) rather than byte-exact.
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if len(got) < 8 || !bytes.Equal(got[:8], pngMagic) {
		t.Fatalf("readImagePNG: not a PNG (len=%d)", len(got))
	}
}
