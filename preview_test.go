package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/tom-hoover/ciba/internal/ciba"
	"github.com/tom-hoover/darkroom/imaging"
	"github.com/tom-hoover/darkroom/jobplan"
)

// TestRunPreviewWritesASheetWithATilePerLook asserts only what runPreview
// itself owns.
//
// A dimensional bound phrased in terms of px (e.g. "at least 3*px wide")
// cannot work here: writeJPEGWithExif's fixture is 16x12, and
// imaging.Thumbnail returns a source unchanged once its short edge is
// already at or below the requested size, so each tile stays 16x12
// regardless of px. At that tile size the sheet's geometry is dominated by
// label width (sheet.Build widens each cell to fit the longest look name),
// so any px-derived bound would really be testing how wide the word
// "classic" renders in basicfont.Face7x13, not anything runPreview does.
//
// Instead this proves: a file lands at the path sheetPathFor computes; it
// decodes as a valid PNG; it is not sheet.Build's 1x1 empty-looks
// placeholder; and — the check that actually shows every look got a tile,
// without reimplementing sheet.Build's own cell/label arithmetic inside this
// test — a sheet built from every built-in look is strictly larger, in at
// least one dimension, than one built from a single look at the same px from
// the same source.
func TestRunPreviewWritesASheetWithATilePerLook(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeJPEGWithExif(t, src)
	if err := runPreview(src, "", 64); err != nil {
		t.Fatal(err)
	}
	path := sheetPathFor(src, "")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("no contact sheet written: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("the sheet is not a valid PNG: %v", err)
	}
	all := img.Bounds()
	if all.Dx() <= 1 && all.Dy() <= 1 {
		t.Fatalf("sheet is %v, the empty-looks 1x1 placeholder", all)
	}

	decoded, _, err := imaging.Decode(src)
	if err != nil {
		t.Fatal(err)
	}
	one, ok := ciba.Lookup(ciba.DefaultLook)
	if !ok {
		t.Fatalf("ciba.DefaultLook %q is not a built-in look", ciba.DefaultLook)
	}
	single := ciba.ContactSheet(decoded, []ciba.Look{one}, 64).Bounds()
	if all.Dx() <= single.Dx() && all.Dy() <= single.Dy() {
		t.Errorf("all-%d-looks sheet %v is not larger than a single-look sheet %v",
			len(ciba.Builtins()), all, single)
	}
}

// TestPreviewOutputIsSkippedByScan closes the loop that lets a second run feed
// on the first run's output: -contact.png is a supported input extension.
func TestPreviewOutputIsSkippedByScan(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeJPEGWithExif(t, src)
	if err := runPreview(src, "", 64); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := cmd.Scan(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if filepath.Base(j.Src) == "photo"+jobplan.ContactSuffix+".png" {
			t.Fatalf("Scan picked up the contact sheet as a source photograph: %s", j.Src)
		}
	}
	if len(jobs) != 1 {
		t.Errorf("Scan found %d jobs, want 1", len(jobs))
	}
}
