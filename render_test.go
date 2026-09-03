package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/tom-hoover/ciba/internal/ciba"
	"github.com/tom-hoover/darkroom/imaging"
	"github.com/tom-hoover/darkroom/jobplan"
)

func writeJPEG(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
}

// TestRenderFileAppliesTheLook proves RenderFile writes a decodable JPEG at
// the source's dimensions and that the pixels actually went through
// ciba.Render rather than being copied through untouched. The colour science
// itself — which look does what to which pixel — is internal/ciba's job and
// is already covered there; this only pins the wiring between this command's
// renderer and the shared job planner.
//
// It stays in cmd/ciba, rather than moving to darkroom/jobplan with the rest
// of apply_test.go, because it is the one test in that file that asserts
// something about ciba specifically: jobplan has no opinion about pixels and
// its own tests use a renderer that ignores its input.
func TestRenderFileAppliesTheLook(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sky.jpg")
	dst := filepath.Join(dir, "sky-ciba.jpg")
	writeJPEG(t, src, color.RGBA{R: 40, G: 90, B: 190, A: 255})

	l, _ := ciba.Lookup("deep")
	render := func(img image.Image) image.Image { return ciba.Render(img, l) }
	if err := cmd.RenderFile(jobplan.Job{Src: src, Dst: dst}, render, 95); err != nil {
		t.Fatal(err)
	}

	// The expected pixel is computed independently: decode the same source
	// RenderFile decoded, and run ciba.Render on it directly. Comparing
	// against that — rather than against the untouched input, or a magnitude
	// threshold on how far the pixel moved — pins the actual transform: it
	// fails if the look is skipped, if the wrong look is applied, or if the
	// pipeline is mis-wired, where a threshold only proves "something big
	// happened" and a wrong-but-large transform satisfies it just as well.
	decoded, _, err := imaging.Decode(src)
	if err != nil {
		t.Fatal(err)
	}
	want := ciba.Render(decoded, l)
	wr, wg, wb, _ := want.At(16, 12).RGBA()

	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 24 {
		t.Errorf("output size = %dx%d, want 32x24", b.Dx(), b.Dy())
	}
	r, g, b, _ := img.At(16, 12).RGBA()

	// "deep" moves this fixture from (40,90,190) to (6,28,244) — a ~60-level
	// swing on the channel that moves least (G). A JPEG q95 round trip on a
	// uniform 32x24 block perturbs a channel by at most a couple of levels,
	// so a tolerance of 3 sits an order of magnitude below the signal: wide
	// enough to absorb codec noise, nowhere near wide enough to pass a
	// skipped or wrong look.
	const tol = 3
	if d := absDiff8(r>>8, wr>>8); d > tol {
		t.Errorf("R = %d, want %d ± %d (independently computed via ciba.Render)", r>>8, wr>>8, tol)
	}
	if d := absDiff8(g>>8, wg>>8); d > tol {
		t.Errorf("G = %d, want %d ± %d (independently computed via ciba.Render)", g>>8, wg>>8, tol)
	}
	if d := absDiff8(b>>8, wb>>8); d > tol {
		t.Errorf("B = %d, want %d ± %d (independently computed via ciba.Render)", b>>8, wb>>8, tol)
	}
}

// absDiff8 returns the absolute difference between two 8-bit-range channel
// values carried in uint32 (as img.At(...).RGBA() yields after >>8).
func absDiff8(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
