package ciba

import (
	"image"
	"math"

	"github.com/tom-hoover/darkroom/tone"
)

// exposureFloor keeps log10 finite. It sits far below any usable exposure
// window, so it never binds on a real calculation — the clamp does that. It is
// NOT the bottom of the window; that is set by Scale and Exposure.
const exposureFloor = 1e-6

// Render applies a look to a colour photograph.
//
// Stages 1-5 of docs/design.md's section 4 are pointwise and are done here in
// a single pass, writing straight into the 8-bit output rather than holding
// three float planes. The two spatial stages follow in spatial().
//
// Render expects a Look that has already passed Validate; it does not
// re-validate one itself.
func Render(img image.Image, l Look) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return out
	}

	// Density is normalised against the LOWEST Dmin, not per channel. That is
	// what lets paper white reach 255 in its warmest channel while the others
	// sit just below it — normalising per channel would drive all three to 255
	// and lose the warm-white trait entirely.
	base := math.Min(l.Dmin[0], math.Min(l.Dmin[1], l.Dmin[2]))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r16, g16, b16, a16 := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// RGBA returns alpha-premultiplied values. Recover the straight
			// colour so a semi-transparent PNG renders as its own colour rather
			// than as itself composited over black. A fully transparent pixel
			// has no recoverable colour, so it is left at 0.
			if a16 > 0 && a16 < 0xffff {
				r16 = min(r16*0xffff/a16, 0xffff)
				g16 = min(g16*0xffff/a16, 0xffff)
				b16 = min(b16*0xffff/a16, 0xffff)
			}
			i := (y*w + x) * 4
			for c, v16 := range [3]uint32{r16, g16, b16} {
				lin := tone.SRGBToLinear(float64(v16) / 65535)
				// Scale is the width of the exposure window in log10 units and
				// Exposure its placement — the enlarger's printing time. A
				// positive Exposure pushes the brightest values past e = 1,
				// where the clamp collapses them onto a single density: the
				// specular clip that gives this look its short scale.
				e := tone.Clamp01((math.Log10(math.Max(lin, exposureFloor)) + l.Scale + l.Exposure) / l.Scale)
				// tone.Sigmoid is normalised so f(0) = 0 and f(1) = 1 exactly,
				// which pins the clipped highlight to Dmin[c] and the clipped
				// shadow to Dmax[c] whatever Curve and Pivot do.
				d := l.Dmin[c] + (l.Dmax[c]-l.Dmin[c])*(1-tone.Sigmoid(e, l.Curve[c], l.Pivot[c]))
				refl := math.Pow(10, -(d - base))
				out.Pix[i+c] = uint8(math.Round(tone.Clamp01(tone.LinearToSRGB(refl)) * 255))
			}
			// A rendered photograph is opaque. Carrying alpha through would let
			// the JPEG encoder composite it over black.
			out.Pix[i+3] = 255
		}
	}

	spatial(out, l)
	return out
}

// paperWhite returns the per-channel sRGB value of the look's own paper white:
// what stages 1-5 produce where the exposure clips at the top of the window
// and density is exactly Dmin[c]. It is the same expression as stage 5 with
// D_c = Dmin[c], so a channel's rendered output can never exceed it.
//
// For the shipped presets this is (255,252,250) for classic, wet and deep,
// (255,250,242) for azo, and a uniform 255 for flat.
func paperWhite(l Look) [3]float64 {
	base := math.Min(l.Dmin[0], math.Min(l.Dmin[1], l.Dmin[2]))
	var pw [3]float64
	for c := range pw {
		pw[c] = tone.Clamp01(tone.LinearToSRGB(math.Pow(10, -(l.Dmin[c] - base))))
	}
	return pw
}

// spatial applies clarity and bloom, the two stages that need neighbouring
// pixels. Both work on luminance and are applied equally to all three
// channels, so neither can shift hue — after the density stage most of the
// frame is saturated enough that a per-channel unsharp mask would fringe.
//
// Both stages clamp to the look's own paper white rather than to 1.0. Bloom
// models veiling flare, which adds light, but a print cannot be brighter than
// the paper it is on, and paper white is per-channel: this is the "no optical
// brighteners" trait, and it lives in the Dmin spread. Clamping to 1.0 instead
// erases it from every clipped highlight — measured on a synthetic scene,
// classic rendered 4666 pixels at R=255 and not one of them warm — because
// only ~5/255 separates classic's blue from white, which any bloom above about
// 0.02 closes. Clamping to paperWhite makes the trait a property of the model
// rather than of the parameter values, so a later preset retune cannot
// silently undo it.
//
// The two stages are inherently sequential: bloom reads luminance recomputed
// AFTER clarity, because clarity brightens highlight edges and those edges are
// exactly what should bloom. So each applies its own delta and rounds its own
// result. The double rounding costs at most 1/255; accumulating both deltas
// for a single rounding would cost a fourth float plane, roughly 190MB per
// worker at 24MP.
//
// The three float planes are allocated once and handed to both stages. Letting
// each stage allocate its own set costs six planes rather than three, because
// at default GOGC the clarity stage's are still live when bloom's are
// allocated: 52 bytes/px measured on a 3000x2000 render against 28 as it
// stands. docs/design.md §4.1 has the full accounting.
//
// Both radii go through tone.RadiusPx, which is what keeps a contact-sheet
// tile predictive of the full-resolution render.
func spatial(out *image.RGBA, l Look) {
	w, h := out.Rect.Dx(), out.Rect.Dy()
	short := min(w, h)

	clarityR, bloomR := 0, 0
	if l.Clarity > 0 && l.Radius > 0 {
		clarityR = tone.RadiusPx(l.Radius, short)
	}
	if l.Bloom > 0 && l.BloomRadius > 0 {
		bloomR = tone.RadiusPx(l.BloomRadius, short)
	}
	if clarityR <= 0 && bloomR <= 0 {
		return
	}

	ceiling := paperWhite(l)
	n := w * h
	y := make([]float64, n)
	blur := make([]float64, n)
	tmp := make([]float64, n)

	if clarityR > 0 {
		lumaInto(y, out)
		tone.BlurBox3Into(blur, y, tmp, w, h, clarityR)
		for i := range y {
			addAll(out, i, l.Clarity*(y[i]-blur[i]), ceiling)
		}
	}

	if bloomR > 0 {
		// Recomputed after clarity, deliberately: see the ordering rationale
		// above. No output-difference test at this tolerance separates this
		// from computing luma once before clarity, but docs/design.md calls
		// for luminance measured after clarity, so that is what this does.
		lumaInto(y, out)
		// Validate guarantees BloomThresh < 1, so this divisor is non-zero.
		for i, v := range y {
			y[i] = math.Max(0, v-l.BloomThresh) / (1 - l.BloomThresh)
		}
		tone.BlurBox3Into(blur, y, tmp, w, h, bloomR)
		for i := range blur {
			addAll(out, i, l.Bloom*blur[i], ceiling)
		}
	}
}

// lumaInto fills y with Rec.709 luminance over the sRGB-encoded output, in
// 0..1.
//
// Computed in the perceptual space rather than in linear light: that is where
// "an edge" means what the eye means by it, and it is what keeps unsharp
// masking from haloing.
func lumaInto(y []float64, out *image.RGBA) {
	for i := range y {
		p := i * 4
		y[i] = (0.2126*float64(out.Pix[p]) + 0.7152*float64(out.Pix[p+1]) + 0.0722*float64(out.Pix[p+2])) / 255
	}
}

// addAll adds the same delta to all three channels of pixel i, leaving hue
// untouched, and clamps each channel to its own paper-white ceiling.
//
// The ceiling can only ever lower a value the spatial stages pushed above the
// paper: stage 5's own output is already at or below it, bar half a unit of
// rounding, which rounds back to the same byte.
func addAll(out *image.RGBA, i int, delta float64, ceiling [3]float64) {
	p := i * 4
	for c := 0; c < 3; c++ {
		v := float64(out.Pix[p+c])/255 + delta
		if v > ceiling[c] {
			v = ceiling[c]
		} else if v < 0 {
			v = 0
		}
		out.Pix[p+c] = uint8(math.Round(v * 255))
	}
}
