package qr

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, c *Code, path string) {
	t.Helper()
	const scale, quiet = 8, 4
	n := (c.Size + 2*quiet) * scale
	img := image.NewGray(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			v := uint8(255)
			if c.Dark(x/scale-quiet, y/scale-quiet) {
				v = 0
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Round-trips through a real decoder (zbarimg) when one is installed, across
// every version the encoder can pick, including the multi-block and
// version-info (>= 7) ones. Skipped, not faked, when zbarimg is absent.
func TestEncodeDecodesWithZbar(t *testing.T) {
	zbar, err := exec.LookPath("zbarimg")
	if err != nil {
		t.Skip("zbarimg not installed")
	}
	for _, n := range []int{5, 30, 60, 100, 130, 160, 200, 240, 271} {
		text := "http://100.101.102.103:4242/slide#t=" + strings.Repeat("a1B2", n)[:n-min(n, 36)]
		if len(text) > n {
			text = text[:n]
		}
		c, err := Encode(text)
		if err != nil {
			t.Fatalf("len %d: %v", len(text), err)
		}
		path := filepath.Join(t.TempDir(), "q.png")
		writePNG(t, c, path)
		out, err := exec.Command(zbar, "--quiet", "--raw", "-Sdisable", "-Sqrcode.enable", path).Output()
		if err != nil {
			t.Fatalf("len %d (v%d): zbar failed: %v", len(text), (c.Size-17)/4, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != text {
			t.Fatalf("len %d: decoded %q, want %q", len(text), got, text)
		}
	}
}

func TestEncodeRejectsOversize(t *testing.T) {
	if _, err := Encode(strings.Repeat("x", MaxBytes+1)); err == nil {
		t.Fatal("want error for oversize payload")
	}
	if _, err := Encode(strings.Repeat("x", MaxBytes)); err != nil {
		t.Fatalf("max payload should fit: %v", err)
	}
}

func TestTerminalIsBlackOnWhite(t *testing.T) {
	c, _ := Encode("hi")
	out := c.Terminal()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "\x1b[47;30m") || !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line not black-on-white wrapped: %q", line)
		}
	}
}
