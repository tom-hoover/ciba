package ciba

import (
	"image"
	"image/color"
	"testing"

	"github.com/tom-hoover/darkroom/imaging"
	"github.com/tom-hoover/darkroom/sheet"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

// countingImage records how many times a pixel is read, so a test can tell
// whether work happened at thumbnail scale or at full scale.
type countingImage struct {
	image.Image
	reads *int
}

func (c countingImage) At(x, y int) color.Color {
	*c.reads++
	return c.Image.At(x, y)
}

// TestContactSheetRendersAtThumbnailScale pins the ordering inside
// ContactSheet: the source is thumbnailed ONCE and every tile is rendered from
// that thumbnail.
//
// This is a COST property, not a correctness one. Rendering full size and
// shrinking afterwards produces nearly the same picture — that is exactly what
// Render's scale invariance guarantees — so an output-difference test cannot
// detect the mistake. Counting work is the only way to see the mistake.
func TestContactSheetRendersAtThumbnailScale(t *testing.T) {
	const src, px = 1024, 128
	looks := Builtins()

	var reads int
	ContactSheet(countingImage{Image: scene(src, src), reads: &reads}, looks, px)

	// Thumbnailing reads the source once. Rendering each tile then reads only
	// the thumbnail, which is not this counter.
	//
	// The one-versus-many comparison below is the real guard for the ordering
	// property. This absolute cap is a separate tripwire on the resampler's
	// tap count, and it is deliberately loose: the measured count is logged so
	// a drift towards the cap is visible on a green run, rather than the cap
	// firing here and in internal/bw at once and blaming the sheet for a
	// change in internal/imaging.
	budget := src * src * 8
	t.Logf("thumbnailing a %dpx source read it %d times (cap %d, %.1f%% of it)",
		src, reads, budget, 100*float64(reads)/float64(budget))
	if reads > budget {
		t.Fatalf("ContactSheet read the source %d times (budget %d) — it is rendering at full "+
			"size and shrinking afterwards, which costs one full-resolution render per look",
			reads, budget)
	}

	// And it must genuinely scale with the source, not with the look count:
	// re-running with one look must read the source about as many times.
	var oneLook int
	ContactSheet(countingImage{Image: scene(src, src), reads: &oneLook}, looks[:1], px)
	if reads > 2*oneLook {
		t.Fatalf("source reads grew from %d to %d when looks went from 1 to %d — the source "+
			"is being re-read per look instead of thumbnailed once",
			oneLook, reads, len(looks))
	}
}

// TestContactSheetTileMatchesDirectRender verifies the sheet composites the
// rendered tile faithfully: no accidental colour-model conversion, pixel
// offset, or double-processing between rendering a tile and drawing it.
//
// This does NOT pin the thumbnail/render ordering — see the test above for why
// an output comparison structurally cannot.
func TestContactSheetTileMatchesDirectRender(t *testing.T) {
	const px = 64
	src := scene(256, 256)
	looks := Builtins()[:1]
	got := ContactSheet(src, looks, px)
	want := Render(imaging.Thumbnail(src, px), looks[0])
	wb := want.Bounds()

	var compared int
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			wr, wg, wbl, _ := want.At(x, y).RGBA()
			gr, gg, gb, _ := got.At(sheet.Padding+x, sheet.Padding+y).RGBA()
			if wr != gr || wg != gg || wbl != gb {
				t.Fatalf("tile pixel (%d,%d): sheet has (%d,%d,%d), direct render has (%d,%d,%d)",
					x, y, gr>>8, gg>>8, gb>>8, wr>>8, wg>>8, wbl>>8)
			}
			// scene() is not uniform, so a real comparison must see more than
			// one distinct colour; otherwise this loop could be comparing two
			// blank regions and passing vacuously.
			if wr != 0 || wg != 0 || wbl != 0 {
				compared++
			}
		}
	}
	if compared == 0 {
		t.Fatal("every compared pixel was black — the offsets are likely reading background, not the rendered tile")
	}
}

// TestContactSheetPairsEachLookWithItsOwnTile pins the half of spec §9's
// second criterion that a single-tile comparison structurally cannot reach:
// tile i must be Render(thumb, looks[i]), for every i, under the label
// looks[i].Name.
//
// TestContactSheetTileMatchesDirectRender above compares a one-element slice,
// where i and 0 are the same number, so it is blind to an index that is
// ignored or paired with the wrong label. Two looks whose renders of the SAME
// thumbnail differ markedly is the minimum that makes the index observable;
// the vacuity guard below measures that difference rather than assuming it.
//
// Only the tile region is compared, not the label band: internal/sheet's
// TestContactSheetPairsEachLabelWithItsOwnTile pins the label-to-index pairing
// inside Build for every caller, and re-deriving glyph positions here would
// duplicate the geometry that lives in one package on purpose. What this test
// adds is that the pipeline hands Build the right look for the right index.
func TestContactSheetPairsEachLookWithItsOwnTile(t *testing.T) {
	const px = 64
	src := scene(256, 256)
	classic, _ := Lookup("classic")
	azo, _ := Lookup("azo")
	looks := []Look{classic, azo}

	thumb := imaging.Thumbnail(src, px)
	want := make([]*image.RGBA, len(looks))
	for i, l := range looks {
		want[i] = Render(thumb, l)
	}

	// Vacuity guard: if the two looks rendered the same thumbnail alike, a
	// tile drawn from the wrong index would be indistinguishable from a
	// correct one and everything below would pass for the wrong reason.
	var differing, maxDelta int
	for i := 0; i+3 < len(want[0].Pix); i += 4 {
		d := 0
		for c := 0; c < 3; c++ {
			if v := abs(int(want[0].Pix[i+c]) - int(want[1].Pix[i+c])); v > d {
				d = v
			}
		}
		if d > 0 {
			differing++
		}
		if d > maxDelta {
			maxDelta = d
		}
	}
	t.Logf("classic vs azo on the same %dpx thumbnail: %d of %d pixels differ, worst channel delta %d",
		px, differing, len(want[0].Pix)/4, maxDelta)
	if differing*4 < len(want[0].Pix) || maxDelta < 5 {
		t.Fatalf("classic and azo render this thumbnail too much alike (%d of %d pixels differ, worst delta %d) — a wrong tile index would be invisible",
			differing, len(want[0].Pix)/4, maxDelta)
	}

	got := ContactSheet(src, looks, px)

	// Two names tile as two across, one down. Both names are narrower than a
	// 64px tile, so the cell's inner width is the tile's — asserted rather
	// than assumed, because a wider name would shift every cell after the
	// first and this test would then be reading the wrong region.
	tb := thumb.Bounds()
	tw, th := tb.Dx(), tb.Dy()
	for _, l := range looks {
		if w := font.MeasureString(basicfont.Face7x13, l.Name).Ceil(); w > tw {
			t.Fatalf("label %q measures %dpx, wider than the %dpx tile — the cell width is no longer the tile width, so the offsets below are wrong", l.Name, w, tw)
		}
	}
	cellW := tw + sheet.Padding

	for i := range looks {
		x := sheet.Padding + i*cellW
		y := sheet.Padding
		for ty := 0; ty < th; ty++ {
			for tx := 0; tx < tw; tx++ {
				wr, wg, wb, _ := want[i].At(tx, ty).RGBA()
				gr, gg, gb, _ := got.At(x+tx, y+ty).RGBA()
				if wr == gr && wg == gg && wb == gb {
					continue
				}
				// Name the look the sheet actually drew, when it is one of
				// the others: that is the difference between "wrong index"
				// and "corrupted composite".
				blame := ""
				for j := range looks {
					if j == i {
						continue
					}
					or, og, ob, _ := want[j].At(tx, ty).RGBA()
					if or == gr && og == gg && ob == gb {
						blame = " — that is " + looks[j].Name + "'s render, so this tile came from the wrong index"
					}
				}
				t.Fatalf("tile %d (%s) pixel (%d,%d): sheet has (%d,%d,%d), %s renders (%d,%d,%d)%s",
					i, looks[i].Name, tx, ty, gr>>8, gg>>8, gb>>8, looks[i].Name, wr>>8, wg>>8, wb>>8, blame)
			}
		}
	}

	// The names ContactSheet hands Build are the other half of the pairing,
	// and the tile comparison above cannot see them. Comparing the whole
	// sheet against Build driven directly with the same two lists covers
	// them without re-deriving where glyphs land: a look order or a name
	// order that disagreed with the other would show up in a label band.
	// (This cannot substitute for the tile comparison — an index Build
	// ignored would be ignored identically on both sides.)
	direct := sheet.Build(src, []string{looks[0].Name, looks[1].Name}, px,
		func(thumb image.Image, i int) image.Image { return Render(thumb, looks[i]) })
	directBounds, gotBounds := direct.Bounds(), got.Bounds()
	if directBounds != gotBounds {
		t.Fatalf("ContactSheet produced %v, Build driven directly produced %v", gotBounds, directBounds)
	}
	for y := directBounds.Min.Y; y < directBounds.Max.Y; y++ {
		for x := directBounds.Min.X; x < directBounds.Max.X; x++ {
			dr, dg, dbl, da := direct.At(x, y).RGBA()
			gr, gg, gbl, ga := got.At(x, y).RGBA()
			if dr != gr || dg != gg || dbl != gbl || da != ga {
				t.Fatalf("sheet pixel (%d,%d): ContactSheet has (%d,%d,%d), Build driven directly with the same names and looks has (%d,%d,%d) — ContactSheet is not pairing look i with name i",
					x, y, gr>>8, gg>>8, gbl>>8, dr>>8, dg>>8, dbl>>8)
			}
		}
	}
}

func TestContactSheetHandlesNoLooks(t *testing.T) {
	if got := ContactSheet(scene(64, 64), nil, 32); got.Bounds().Empty() {
		t.Error("ContactSheet(nil looks) returned an empty image; want a 1x1 placeholder")
	}
}
