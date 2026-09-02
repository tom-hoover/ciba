package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/tom-hoover/ciba/internal/ciba"
	"github.com/tom-hoover/darkroom/imaging"
	"github.com/tom-hoover/darkroom/jobplan"
)

// sheetPathFor returns the contact sheet path for a source image.
func sheetPathFor(src, outDir string) string {
	dir := filepath.Dir(src)
	if outDir != "" {
		dir = outDir
	}
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	return filepath.Join(dir, base+jobplan.ContactSuffix+".png")
}

// runPreview writes a labelled contact sheet of every available look: the
// built-ins, then any custom looks in the personal library.
//
// Custom looks belong on the sheet because comparing a look you are authoring
// against the presets is the only way to judge it; without them, iterating on a
// look means rendering it alone and remembering what the others looked like.
//
// PNG rather than JPEG: these tiles are judged by eye, and JPEG artefacts on
// label text and on deliberately crushed blacks would misrepresent the very
// looks being compared.
func runPreview(src, outDir string, px int) error {
	img, _, err := imaging.Decode(src)
	if err != nil {
		return err
	}
	looks, err := previewLooks()
	if err != nil {
		return err
	}
	path := sheetPathFor(src, outDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, ciba.ContactSheet(img, looks, px)); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("%s\npick a label, then: ciba -style <name> <directory>\n", path)
	return nil
}
