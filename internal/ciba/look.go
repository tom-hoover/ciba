// Package ciba renders colour photographs with the look of an early-1980s
// Cibachrome print, using a single deterministic pipeline so a thumbnail
// preview predicts the full-resolution result: every spatial radius is a
// fraction of the short edge, so the two differ only by resampling.
//
// The look is derived from the silver dye-bleach process rather than from
// reference scans; see docs/superpowers/specs/2026-08-28-ciba-design.md §2 for
// the trait-to-parameter mapping.
package ciba

import (
	"fmt"
	"math"
)

// DefaultLook is applied when none is named.
const DefaultLook = "classic"

// Look is one complete rendering recipe.
//
// Radius and BloomRadius are FRACTIONS of the image's short edge, never pixel
// counts. That is what makes a 256px preview tile predict a full-resolution
// render; see the design spec §4 and tone.RadiusPx.
//
// Dmin and Dmax own the ends of the tonal range and Curve and Pivot own the
// middle: tone.Sigmoid is normalised so f(0) = 0 and f(1) = 1 exactly, which
// pins the clipped highlight to Dmin[c] and the clipped shadow to Dmax[c]
// whatever the curve does. But Dmin and Dmax are COUPLED — the multiplier is
// (Dmax[c] - Dmin[c]), so lowering one channel's Dmax lifts that channel at
// every tone, not only in shadow. Compensate by raising the same channel's
// Dmin by roughly a fifth of the change. Spec §5.1 has the arithmetic and the
// white-balance error that skipping it produces.
type Look struct {
	Name        string     `json:"name"`
	Desc        string     `json:"desc"`
	Scale       float64    `json:"scale"`       // width of the exposure window, log10 units; smaller = steeper
	Exposure    float64    `json:"exposure"`    // placement of the window, log10 units; positive = brighter, clips speculars
	Curve       [3]float64 `json:"curve"`       // per-channel sigmoidal strength; 0 = straight line
	Pivot       [3]float64 `json:"pivot"`       // per-channel curve centre, 0..1
	Dmin        [3]float64 `json:"dmin"`        // per-channel base density: the paper-white tint
	Dmax        [3]float64 `json:"dmax"`        // per-channel maximum density: the clipped-shadow tint
	Clarity     float64    `json:"clarity"`     // local-contrast amount, 0..2
	Radius      float64    `json:"radius"`      // clarity radius, fraction of the short edge
	Bloom       float64    `json:"bloom"`       // highlight bloom amount, 0..1
	BloomRadius float64    `json:"bloomRadius"` // bloom radius, fraction of the short edge
	BloomThresh float64    `json:"bloomThresh"` // luminance above which highlights bloom, 0..1
}

// Validate reports whether the look's parameters are usable. Render expects a
// look that has passed this and does not re-validate one itself.
func (l Look) Validate() error {
	if l.Name == "" {
		return fmt.Errorf("look has no name")
	}
	// A range check alone cannot catch NaN: < and > are both false for it in
	// either direction, so a NaN field would sail through every check below
	// and reach the pixel loop untouched. This explicit sweep is what catches
	// it instead.
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"scale", l.Scale}, {"exposure", l.Exposure},
		{"curve[0]", l.Curve[0]}, {"curve[1]", l.Curve[1]}, {"curve[2]", l.Curve[2]},
		{"pivot[0]", l.Pivot[0]}, {"pivot[1]", l.Pivot[1]}, {"pivot[2]", l.Pivot[2]},
		{"dmin[0]", l.Dmin[0]}, {"dmin[1]", l.Dmin[1]}, {"dmin[2]", l.Dmin[2]},
		{"dmax[0]", l.Dmax[0]}, {"dmax[1]", l.Dmax[1]}, {"dmax[2]", l.Dmax[2]},
		{"clarity", l.Clarity}, {"radius", l.Radius},
		{"bloom", l.Bloom}, {"bloomRadius", l.BloomRadius}, {"bloomThresh", l.BloomThresh},
	} {
		if math.IsNaN(f.v) {
			return fmt.Errorf("%s: %s is NaN", l.Name, f.name)
		}
	}
	// Scale divides in the exposure calculation, so zero is fatal rather than
	// merely odd. Four log units exceeds the range of any print process.
	if l.Scale <= 0 || l.Scale > 4 {
		return fmt.Errorf("%s: scale %.3f out of range (0-4 log units, exclusive of 0)", l.Name, l.Scale)
	}
	// Beyond two log units of offset the entire frame sits on one clamp.
	if l.Exposure < -2 || l.Exposure > 2 {
		return fmt.Errorf("%s: exposure %.3f out of range (-2 to 2 log units)", l.Name, l.Exposure)
	}
	for c := 0; c < 3; c++ {
		// 0-30 is the range tone.Sigmoid is tested across.
		if l.Curve[c] < 0 || l.Curve[c] > 30 {
			return fmt.Errorf("%s: curve[%d] %.3f out of range (0-30)", l.Name, c, l.Curve[c])
		}
		if l.Pivot[c] < 0 || l.Pivot[c] > 1 {
			return fmt.Errorf("%s: pivot[%d] %.3f out of range (0-1)", l.Name, c, l.Pivot[c])
		}
		// A base density above 1.0 is not a printable paper.
		if l.Dmin[c] < 0 || l.Dmin[c] > 1 {
			return fmt.Errorf("%s: dmin[%d] %.3f out of range (0-1)", l.Name, c, l.Dmin[c])
		}
		// The < 0 half can never be the reason a Look is rejected: Dmin[c] is
		// already validated >= 0 above, so any Dmax[c] < 0 also trips the
		// coupled check below. It stays for a more specific message and for
		// symmetry with Dmin's two-sided check.
		if l.Dmax[c] < 0 || l.Dmax[c] > 5 {
			return fmt.Errorf("%s: dmax[%d] %.3f out of range (0-5)", l.Name, c, l.Dmax[c])
		}
		// Equal or inverted densities render that channel flat or negative.
		if l.Dmax[c] <= l.Dmin[c] {
			return fmt.Errorf("%s: dmax[%d] %.3f must exceed dmin[%d] %.3f", l.Name, c, l.Dmax[c], c, l.Dmin[c])
		}
	}
	if l.Clarity < 0 || l.Clarity > 2 {
		return fmt.Errorf("%s: clarity %.3f out of range (0-2)", l.Name, l.Clarity)
	}
	// Above a quarter of the short edge this stops being local contrast and
	// starts being a second exposure.
	if l.Radius < 0 || l.Radius > 0.25 {
		return fmt.Errorf("%s: radius %.4f out of range (0-0.25 of the short edge)", l.Name, l.Radius)
	}
	// Above 1.0 a highlight would add back more light than it contains.
	if l.Bloom < 0 || l.Bloom > 1 {
		return fmt.Errorf("%s: bloom %.3f out of range (0-1)", l.Name, l.Bloom)
	}
	// Bloom is legitimately wider than clarity — it models a surface, not an edge.
	if l.BloomRadius < 0 || l.BloomRadius > 0.5 {
		return fmt.Errorf("%s: bloomRadius %.4f out of range (0-0.5 of the short edge)", l.Name, l.BloomRadius)
	}
	// At exactly 1.0 the highlight normalisation in Render divides by zero.
	if l.BloomThresh < 0 || l.BloomThresh >= 1 {
		return fmt.Errorf("%s: bloomThresh %.3f out of range (0 to just under 1)", l.Name, l.BloomThresh)
	}
	return nil
}

// Builtins returns the presets, in the order they appear on a contact sheet.
//
// Every Pivot was solved numerically so the look places a neutral sRGB 128
// where spec §6's "mid" column says, and every Dmin/Dmax pair follows the
// coupling relation in §5.1. Changing one of a pair without the other
// reintroduces a midtone cast; TestMidtoneStaysNearNeutral is the render-side
// regression test for that, so a retune here is checked against pixel
// output.
func Builtins() []Look {
	return []Look{
		{
			Name: "classic", Desc: "the reference Cibachrome: dense blacks, warm paper white",
			Scale: 1.70, Exposure: 0.15,
			Curve:   [3]float64{6.0, 6.0, 6.0},
			Pivot:   [3]float64{0.552, 0.552, 0.532},
			Dmin:    [3]float64{0.04, 0.05, 0.06},
			Dmax:    [3]float64{2.50, 2.50, 2.42},
			Clarity: 0.35, Radius: 0.020,
			Bloom: 0.06, BloomRadius: 0.060, BloomThresh: 0.80,
		},
		{
			Name: "wet", Desc: "maximum gloss: highest D-max, hardest acutance, specular sheen",
			Scale: 1.65, Exposure: 0.20,
			Curve: [3]float64{7.0, 7.0, 7.0},
			Pivot: [3]float64{0.568, 0.568, 0.548},
			Dmin:  [3]float64{0.03, 0.04, 0.05},
			Dmax:  [3]float64{3.00, 3.00, 2.92},
			// 0.45 rather than the 0.55 this shipped with. Judged on full-
			// resolution crops of two subjects: on a scree slope 0.55 added
			// separation rather than detail, and on skin it hardened smooth
			// tonal gradients into patches and left a pale halo tracing a
			// high-contrast edge — unsharp masking overshooting, not acutance.
			// 0.45 improves both subjects over 0.35 without damaging either.
			//
			// The two subjects disagree because the radius is a fraction of the
			// short edge, so at full resolution it is around 60px: larger than
			// a scree stone, comparable to a patch of cheek. It sharpens texture
			// it can resolve and blocks up gradients it cannot.
			Clarity: 0.45, Radius: 0.020,
			Bloom: 0.12, BloomRadius: 0.070, BloomThresh: 0.78,
		},
		{
			// wet's ends with a flatter middle. Placed next to wet so a contact
			// sheet pairs them.
			//
			// The problem it addresses, measured rather than judged: on a
			// midtone cheek sampled from a real portrait (input 154,106,97,
			// red-green separation +48) wet renders 186,70,60 — separation
			// +116, more than double. Skin is a strongly red-dominant midtone,
			// and a monotone curve applied independently per channel cannot
			// raise contrast without widening the gaps between channels, which
			// is the same mechanism spec §2 credits for the saturation.
			//
			// So this is a partial remedy, not a fix, and the parameters were
			// chosen by measuring the alternatives rather than by taste:
			// flattening the curve (Scale 1.65-2.10, Curve 7-4) moves the
			// separation only to +82 at its limit; cutting the density range
			// (Dmax 3.0-2.0) reaches +94 and destroys the dense blacks; moving
			// where skin sits on the curve (Exposure +0.35 to -0.25) reaches
			// +106. Scale 1.95 with Curve 5.0 is the best return for character
			// lost, landing separation at +91 on that patch and cutting the
			// excess over a neutral rendering by about a third on a whole face.
			//
			// Dmin and Dmax are wet's exactly, so the two signature traits are
			// untouched: paper white stays (255,252,250) and the clipped shadow
			// stays (4,4,4). That holds by construction rather than by tuning —
			// tone.Sigmoid pins the ends whatever the curve does — which is why
			// softening the middle costs none of the look's identity.
			//
			// Clarity is 0.35 rather than wet's 0.45: a portrait is the subject
			// this exists for, and 0.45 hardens skin gradients into patches.
			Name: "wetportrait", Desc: "wet's ends, flatter middle: less lurid skin",
			Scale: 1.95, Exposure: 0.20,
			Curve:   [3]float64{5.0, 5.0, 5.0},
			Pivot:   [3]float64{0.600, 0.600, 0.580},
			Dmin:    [3]float64{0.03, 0.04, 0.05},
			Dmax:    [3]float64{3.00, 3.00, 2.92},
			Clarity: 0.35, Radius: 0.020,
			Bloom: 0.12, BloomRadius: 0.070, BloomThresh: 0.78,
		},
		{
			Name: "deep", Desc: "shortest scale: hardest clip at both ends",
			Scale: 1.50, Exposure: 0.25,
			Curve:   [3]float64{8.0, 8.0, 8.0},
			Pivot:   [3]float64{0.616, 0.616, 0.596},
			Dmin:    [3]float64{0.04, 0.05, 0.06},
			Dmax:    [3]float64{2.80, 2.80, 2.72},
			Clarity: 0.40, Radius: 0.018,
			Bloom: 0.05, BloomRadius: 0.060, BloomThresh: 0.82,
		},
		{
			Name: "azo", Desc: "crossover exaggerated: hot reds, yellow-green greens, navy skies",
			Scale: 1.70, Exposure: 0.15,
			Curve:   [3]float64{7.5, 6.0, 8.5},
			Pivot:   [3]float64{0.564, 0.594, 0.624},
			Dmin:    [3]float64{0.03, 0.05, 0.08},
			Dmax:    [3]float64{2.60, 2.50, 2.34},
			Clarity: 0.35, Radius: 0.020,
			Bloom: 0.06, BloomRadius: 0.060, BloomThresh: 0.80,
		},
		{
			// The achromatic control tile. Without a straight-line reference on
			// the sheet there is no way to see what the curve contributes. It is
			// also the only look with Exposure 0, so it is the only one where a
			// near-white input does NOT clip to paper white.
			Name: "flat", Desc: "straight-line control: shows what the curve contributes",
			Scale: 2.20, Exposure: 0.00,
			Curve:   [3]float64{0, 0, 0},
			Pivot:   [3]float64{0.50, 0.50, 0.50},
			Dmin:    [3]float64{0.04, 0.04, 0.04},
			Dmax:    [3]float64{2.20, 2.20, 2.20},
			Clarity: 0, Radius: 0.020,
			Bloom: 0, BloomRadius: 0.060, BloomThresh: 0.80,
		},
	}
}

// Lookup finds a built-in look by name.
func Lookup(name string) (Look, bool) {
	for _, l := range Builtins() {
		if l.Name == name {
			return l, true
		}
	}
	return Look{}, false
}
