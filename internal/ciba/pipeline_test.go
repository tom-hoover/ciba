package ciba

import (
	"image"
	"image/color"
	"math"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/tom-hoover/darkroom/imaging"
	"github.com/tom-hoover/darkroom/tone"
)

// solid builds a uniform colour image for tonal assertions.
func solid(size int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// at1 renders a uniform grey of value v and returns the centre pixel, after
// zeroing the look's Clarity and Bloom. That isolates the pointwise stages:
// clarity is already inert on a uniform field (the blur of a constant is the
// same constant, so its unsharp delta is exactly zero), but bloom is NOT — a
// uniform field near white still has luminance above BloomThresh, and that
// highlight would bloom even though there is no edge for it to sell. Zeroing
// both here keeps this fixture testing only stages 1-5; the spatial stages
// added later have their own tests.
func at1(t *testing.T, l Look, r, g, b uint8) (int, int, int) {
	t.Helper()
	l.Clarity = 0
	l.Bloom = 0
	out := Render(solid(8, color.RGBA{R: r, G: g, B: b, A: 255}), l)
	i := (4*8 + 4) * 4
	return int(out.Pix[i]), int(out.Pix[i+1]), int(out.Pix[i+2])
}

func TestRenderPreservesDimensions(t *testing.T) {
	l, _ := Lookup("classic")
	for _, d := range [][2]int{{1, 1}, {7, 13}, {64, 16}, {100, 100}} {
		src := image.NewRGBA(image.Rect(3, 5, 3+d[0], 5+d[1]))
		out := Render(src, l)
		if got := out.Bounds(); got.Dx() != d[0] || got.Dy() != d[1] {
			t.Errorf("%dx%d source rendered to %v", d[0], d[1], got)
		}
		if got := out.Bounds().Min; got.X != 0 || got.Y != 0 {
			t.Errorf("output origin is %v, want (0,0)", got)
		}
	}
}

func TestRenderZeroDimensionDoesNotPanic(t *testing.T) {
	l, _ := Lookup("classic")
	Render(image.NewRGBA(image.Rect(0, 0, 0, 10)), l)
	Render(image.NewRGBA(image.Rect(0, 0, 10, 0)), l)
}

// TestEveryChannelIsMonotonic sweeps all 256 input levels. A curve that
// reverses anywhere produces posterised or inverted tone, and no endpoint
// assertion can see it.
func TestEveryChannelIsMonotonic(t *testing.T) {
	for _, l := range Builtins() {
		prev := [3]int{-1, -1, -1}
		for v := 0; v < 256; v++ {
			r, g, b := at1(t, l, uint8(v), uint8(v), uint8(v))
			for c, got := range [3]int{r, g, b} {
				if got < prev[c] {
					t.Fatalf("%s channel %d: input %d rendered %d after %d — the curve reverses",
						l.Name, c, v, got, prev[c])
				}
				prev[c] = got
			}
		}
	}
}

// TestPaperWhiteIsWarm pins the "no optical brighteners" trait. flat is the
// achromatic control and has a uniform Dmin by design, so it is exempt.
func TestPaperWhiteIsWarm(t *testing.T) {
	for _, l := range Builtins() {
		if l.Name == "flat" {
			continue
		}
		r, g, b := at1(t, l, 255, 255, 255)
		if r != 255 {
			t.Errorf("%s: white input rendered R=%d, want 255 — the warmest channel must reach paper white", l.Name, r)
		}
		if !(r >= g && g >= b) {
			t.Errorf("%s: white input rendered (%d,%d,%d), want R >= G >= B", l.Name, r, g, b)
		}
		if b >= 255 {
			t.Errorf("%s: white input rendered B=%d — with no Dmin spread the white is not warm", l.Name, b)
		}
	}
}

// specular draws a white disc of the given pixel radius on a mid-toned
// background: a small clipped highlight surrounded by tone that does not clip,
// which is where bloom's lift is smallest and where a highlight can still be
// pushed off the paper.
func specular(size, radius int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := size / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			p := color.RGBA{R: 90, G: 100, B: 130, A: 255}
			if math.Hypot(float64(x-c), float64(y-c)) < float64(radius) {
				p = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.Set(x, y, p)
		}
	}
	return img
}

// TestPaperWhiteIsWarmThroughTheFullPipeline asserts the "no optical
// brighteners" trait on the pipeline the product actually ships.
//
// TestPaperWhiteIsWarm above goes through at1, which zeroes Clarity and Bloom
// to isolate the pointwise stages. That is right for what that test isolates
// and wrong as the trait's only coverage: bloom adds light to precisely the
// pixels the trait lives in. While both spatial stages clamped at 1.0 rather
// than at the look's own paper white, classic, wet and deep shipped a NEUTRAL
// clipped highlight — 4666 pixels at R=255 on a 1024px scene for classic, not
// one of them warm — with every pointwise assertion still green.
//
// The assertion is exact and cannot pass vacuously: on a fixture that contains
// pure white, the brightest value each channel reaches anywhere in the frame
// must be that channel's paper white, no more and no less. "No more" is the
// trait; "no less" requires the fixture to have actually clipped, so a look
// that stopped clipping could not pass this by never reaching the ceiling.
//
// flat is not exempt here, it is the control: its Dmin is uniform, so its
// paper white IS neutral 255 and the same assertion covers it.
func TestPaperWhiteIsWarmThroughTheFullPipeline(t *testing.T) {
	fixtures := map[string]image.Image{
		"uniform white":       solid(64, color.RGBA{R: 255, G: 255, B: 255, A: 255}),
		"5px specular on mid": specular(128, 5),
	}
	for _, l := range Builtins() {
		pw := paperWhite(l)
		var want [3]int
		for c := range want {
			want[c] = int(math.Round(pw[c] * 255))
		}
		for name, src := range fixtures {
			out := Render(src, l)
			got := [3]int{-1, -1, -1}
			for i := 0; i+3 < len(out.Pix); i += 4 {
				for c := 0; c < 3; c++ {
					if v := int(out.Pix[i+c]); v > got[c] {
						got[c] = v
					}
				}
			}
			t.Logf("%-8s %-20s brightest (%3d,%3d,%3d), paper white (%3d,%3d,%3d)",
				l.Name, name, got[0], got[1], got[2], want[0], want[1], want[2])
			if got != want {
				t.Errorf("%s, %s: the brightest value each channel reaches is (%d,%d,%d), want the look's own paper white (%d,%d,%d) — a print cannot be brighter than its paper, and paper white is per channel",
					l.Name, name, got[0], got[1], got[2], want[0], want[1], want[2])
			}
		}
	}
}

// TestClippedHighlightsStayWarmInAPicture is the same trait counted over a
// real frame rather than over a fixture built to clip, so the property is
// asserted where clipping actually occurs on a photograph: some pixels reach
// paper white, and none of them are neutral.
//
// flat is exempt, as it is for every warm-white assertion: uniform Dmin, and
// Exposure 0 so nothing clips at all.
func TestClippedHighlightsStayWarmInAPicture(t *testing.T) {
	src := scene(512, 512)
	for _, l := range Builtins() {
		if l.Name == "flat" {
			continue
		}
		out := Render(src, l)
		var atPaperWhite, neutral int
		for i := 0; i+3 < len(out.Pix); i += 4 {
			if out.Pix[i] != 255 {
				continue
			}
			atPaperWhite++
			if out.Pix[i+2] == 255 {
				neutral++
			}
		}
		t.Logf("%-8s %d pixels at R=255, %d of them neutral", l.Name, atPaperWhite, neutral)
		if atPaperWhite == 0 {
			t.Errorf("%s: no pixel reached paper white, so the assertion below is vacuous", l.Name)
		}
		if neutral != 0 {
			t.Errorf("%s: %d of %d clipped highlights rendered B=255 — the warm paper white is gone from the picture",
				l.Name, neutral, atPaperWhite)
		}
	}
}

// TestFlatRendersNeutralNeutral makes flat's job as the control tile
// executable. It is the ONE look that must not tint a neutral.
func TestFlatRendersNeutralNeutral(t *testing.T) {
	l, _ := Lookup("flat")
	for _, v := range []uint8{0, 32, 64, 128, 192, 250, 255} {
		r, g, b := at1(t, l, v, v, v)
		if abs(r-g) > 3 || abs(g-b) > 3 || abs(r-b) > 3 {
			t.Errorf("flat: neutral %d rendered (%d,%d,%d) — the control tile must stay neutral", v, r, g, b)
		}
	}
}

// TestMidtoneStaysNearNeutral is the regression test for the defect in spec
// §5.1: lowering a channel's Dmax without raising its Dmin lifts that channel
// at EVERY tone, which is a white-balance error rather than a hue crossover.
//
// The bound is 14, not tight, on purpose: classic's intended crossover from
// its Pivot spread is +8, and this test must permit that while failing a
// decoupled Dmin/Dmax pairing — lowering Dmax[2] alone produces a spread in
// the high teens to low twenties. azo is exempt: exaggerated crossover is its
// entire purpose. deep's baseline spread sits at 12, two units of margin
// under this bound rather than the zero margin a bound of 12 would leave it.
func TestMidtoneStaysNearNeutral(t *testing.T) {
	for _, l := range Builtins() {
		if l.Name == "azo" {
			continue
		}
		r, g, b := at1(t, l, 128, 128, 128)
		spread := max3(r, g, b) - min3(r, g, b)
		t.Logf("%-8s mid 128 -> (%3d,%3d,%3d) spread %d", l.Name, r, g, b, spread)
		if spread > 14 {
			t.Errorf("%s: neutral 128 rendered (%d,%d,%d), spread %d exceeds 14 — that is a cast, not a crossover; check the Dmin/Dmax pairing (spec 5.1)",
				l.Name, r, g, b, spread)
		}
	}
}

// TestClippedShadowIsBlueward pins the cyan-blue shadow drift.
//
// wet is exempt and deliberately so: at Dmax 3.00/2.92 both channels land on
// 4/255, so the tint is real in density and below the output format's
// resolution. deep and flat are near that floor too; classic and azo are the
// looks where 8 bits can show it.
func TestClippedShadowIsBlueward(t *testing.T) {
	for _, name := range []string{"classic", "azo"} {
		l, _ := Lookup(name)
		r, g, b := at1(t, l, 0, 0, 0)
		if b <= r {
			t.Errorf("%s: clipped shadow rendered (%d,%d,%d), want B > R — the shadow tint is gone", name, r, g, b)
		}
	}
}

// TestSpecularHighlightsClip pins the short-scale trait. A positive Exposure
// pushes the brightest values past the top of the window, where the clamp
// collapses them onto one density: sRGB 250 and 255 must render identically.
//
// flat is the discriminator, not an exception: it is the only look with
// Exposure 0, so in flat those two inputs must render DIFFERENTLY. Without
// that half, setting every Exposure to 0 would still pass.
func TestSpecularHighlightsClip(t *testing.T) {
	for _, l := range Builtins() {
		r1, g1, b1 := at1(t, l, 250, 250, 250)
		r2, g2, b2 := at1(t, l, 255, 255, 255)
		same := r1 == r2 && g1 == g2 && b1 == b2
		if l.Name == "flat" {
			if same {
				t.Errorf("flat: 250 and 255 both rendered (%d,%d,%d) — with Exposure 0 nothing should clip", r1, g1, b1)
			}
			continue
		}
		if !same {
			t.Errorf("%s: 250 rendered (%d,%d,%d) but 255 rendered (%d,%d,%d) — speculars are not clipping",
				l.Name, r1, g1, b1, r2, g2, b2)
		}
	}
}

// TestDeepShadowsClip is the same property at the other end of the window.
func TestDeepShadowsClip(t *testing.T) {
	l, _ := Lookup("classic")
	a := [3]int{}
	a[0], a[1], a[2] = at1(t, l, 0, 0, 0)
	b := [3]int{}
	b[0], b[1], b[2] = at1(t, l, 3, 3, 3)
	if a != b {
		t.Errorf("classic: 0 rendered %v but 3 rendered %v — the bottom of the window is not clipping", a, b)
	}
}

// TestSaturationIncreases pins the azo-dye trait. It measures the spread
// between the largest and smallest channel, which is chroma in the only sense
// this pipeline can change it.
//
// The mechanism is overall steepness (a narrow Scale plus Curve magnitude
// interacting with the log-exposure/reflectance nonlinearity), not per-channel
// spread: a look with Curve and Pivot equalised across channels still
// saturates this patch, because the S-shaped response pushes differing input
// values apart regardless of whether the curve differs by channel. Per-channel
// spread is what produces hue CROSSOVER (see TestPerChannelCurvesRotateHue),
// a distinct trait from the plain saturation this test measures. Proven by
// mutation with Scale widened to 4 and Curve zeroed together — either alone
// still saturates this patch; the whole steepness of the response has to
// drop for the increase to disappear.
func TestSaturationIncreases(t *testing.T) {
	in := [3]int{60, 110, 175} // a muted blue, well inside the window at both ends
	for _, l := range Builtins() {
		if l.Name == "flat" {
			continue // the straight-line control is not supposed to saturate
		}
		r, g, b := at1(t, l, uint8(in[0]), uint8(in[1]), uint8(in[2]))
		before := in[2] - in[0]
		after := max3(r, g, b) - min3(r, g, b)
		t.Logf("%-8s chroma %d -> %d", l.Name, before, after)
		if after <= before {
			t.Errorf("%s: chroma went %d -> %d, want an increase — the density curves are not saturating",
				l.Name, before, after)
		}
	}
}

// TestBlueSkyDensifies pins the contrast half of the sky trait: a plain
// steepened response (Scale narrowed, Curve strengthened, whether or not
// per-channel) makes a mid-blue sky render darker. sky sits on the shadow
// side of each channel's own pivot crossover for every look — a sample
// straddling the crossover, such as {92,140,205}, can brighten under a
// contrasty look despite its darkest channel darkening, because Rec.709
// weights green at 0.7152, so this fixture was chosen to avoid that trap.
//
// flat is a stated exemption, not skipped: Exposure 0 means nothing in
// flat's window clips, so this same sky renders slightly BRIGHTER (111->113)
// under flat — the same role flat plays as the achromatic, non-clipping
// control in TestSpecularHighlightsClip. This test only measures overall
// density; TestPerChannelCurvesRotateHue is what isolates crossover, because
// a look with per-channel Curve/Pivot/Dmin/Dmax
// all equalised to channel 0 densifies this same sky identically to the real
// look — contrast and crossover are independent traits here, and conflating
// them in one assertion left crossover completely unguarded.
func TestBlueSkyDensifies(t *testing.T) {
	sky := [3]uint8{75, 115, 185}
	lumaIn := (2126*int(sky[0]) + 7152*int(sky[1]) + 722*int(sky[2])) / 10000
	for _, l := range Builtins() {
		r, g, b := at1(t, l, sky[0], sky[1], sky[2])
		lumaOut := (2126*r + 7152*g + 722*b) / 10000
		denser := lumaOut < lumaIn
		t.Logf("%-8s sky luma %d -> %d (denser: %v)", l.Name, lumaIn, lumaOut, denser)
		if l.Name == "flat" {
			if denser {
				t.Errorf("flat: sky luma went %d -> %d, want NOT denser — with Exposure 0 nothing should clip or densify", lumaIn, lumaOut)
			}
			continue
		}
		if !denser {
			t.Errorf("%s: sky luma went %d -> %d, want denser", l.Name, lumaIn, lumaOut)
		}
	}
}

// TestPerChannelCurvesRotateHue pins the design's central claim (spec §2):
// the hue crossover is not a separate stage, it is what UNEQUAL per-channel
// density curves do. The only way to isolate that from plain contrast is to
// compare a look against a variant of itself with Curve, Pivot, Dmin and
// Dmax all equalised to channel 0 — an otherwise-identical pipeline with no
// per-channel behaviour at all. Anything that differs between those two
// renders of the same neutral input is attributable to the per-channel
// spread and nothing else.
//
// This replaces an earlier assertion that could not fail:
// TestBlueSkyDensifiesAndCools asserted density and a B-R gap together, but a
// fully equalised classic
// densifies and cools an already-blue sky exactly like the real one —
// B-R widens under any steepened response, equalised or not, so that
// assertion could not detect the absence of crossover. Measuring the
// per-channel delta against the look's own equalised twin cannot pass
// vacuously: it is zero by construction whenever the channels don't differ.
//
// flat is not exempt, it is the control: its Curve, Pivot, Dmin and Dmax are
// already equal across channels, so comparing it to its own equalised twin
// must yield exactly 0. A non-zero flat result would mean this test is
// measuring something other than what it claims.
func TestPerChannelCurvesRotateHue(t *testing.T) {
	for _, l := range Builtins() {
		equalised := l
		equalised.Curve = [3]float64{l.Curve[0], l.Curve[0], l.Curve[0]}
		equalised.Pivot = [3]float64{l.Pivot[0], l.Pivot[0], l.Pivot[0]}
		equalised.Dmin = [3]float64{l.Dmin[0], l.Dmin[0], l.Dmin[0]}
		equalised.Dmax = [3]float64{l.Dmax[0], l.Dmax[0], l.Dmax[0]}

		r1, g1, b1 := at1(t, l, 128, 128, 128)
		r2, g2, b2 := at1(t, equalised, 128, 128, 128)
		delta := max3(abs(r1-r2), abs(g1-g2), abs(b1-b2))
		t.Logf("%-8s (%3d,%3d,%3d) vs equalised (%3d,%3d,%3d) -> delta %d", l.Name, r1, g1, b1, r2, g2, b2, delta)

		if l.Name == "flat" {
			if delta != 0 {
				t.Errorf("flat: delta against its own equalised twin is %d, want exactly 0 — flat's channels are already equal, so this measure is broken", delta)
			}
			continue
		}
		if delta < 5 {
			t.Errorf("%s: delta against its own equalised twin is %d, want >= 5 — the per-channel curves are not rotating hue", l.Name, delta)
		}
	}
}

// TestNoGrain pins a deliberate absence. Dye-bleach on a clear polyester base
// was essentially grainless, and adding grain is the commonest way this look
// gets faked backwards. A uniform input must render perfectly uniform.
//
// This calls Render directly on the unmodified look, not at1, so it exercises
// the whole pipeline including both spatial stages on a uniform field. That
// coverage is deliberate.
func TestNoGrain(t *testing.T) {
	for _, l := range Builtins() {
		out := Render(solid(64, color.RGBA{R: 120, G: 90, B: 60, A: 255}), l)
		first := [4]uint8{out.Pix[0], out.Pix[1], out.Pix[2], out.Pix[3]}
		for i := 0; i+3 < len(out.Pix); i += 4 {
			got := [4]uint8{out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3]}
			if got != first {
				t.Fatalf("%s: uniform input rendered %v at pixel %d and %v at 0 — something is adding noise",
					l.Name, got, i/4, first)
			}
		}
	}
}

// TestPremultipliedAlphaIsUnpremultiplied pins that a semi-transparent PNG
// renders as its own colour rather than as itself composited over black.
func TestPremultipliedAlphaIsUnpremultiplied(t *testing.T) {
	l, _ := Lookup("classic")
	opaque := Render(solid(8, color.RGBA{R: 200, G: 120, B: 60, A: 255}), l)
	// color.RGBA is already premultiplied, so this is the same colour at half alpha.
	half := Render(solid(8, color.RGBA{R: 100, G: 60, B: 30, A: 128}), l)
	i := (4*8 + 4) * 4
	for c := 0; c < 3; c++ {
		if d := int(opaque.Pix[i+c]) - int(half.Pix[i+c]); d < -2 || d > 2 {
			t.Errorf("channel %d: opaque rendered %d, half-alpha rendered %d — alpha was not recovered",
				c, opaque.Pix[i+c], half.Pix[i+c])
		}
	}
}

// TestOutputIsOpaque guards against a rendered photograph carrying alpha into
// the JPEG encoder, which would silently composite it over black.
func TestOutputIsOpaque(t *testing.T) {
	l, _ := Lookup("classic")
	out := Render(solid(8, color.RGBA{R: 100, G: 60, B: 30, A: 128}), l)
	for i := 3; i < len(out.Pix); i += 4 {
		if out.Pix[i] != 255 {
			t.Fatalf("pixel %d has alpha %d, want 255", i/4, out.Pix[i])
		}
	}
}

// TestCurveZeroIsStraightInDensity pins the property that makes Curve a
// meaningful parameter at all: at Curve 0, tone.Sigmoid is the identity, so
// recovered density must be an AFFINE function of log exposure. flat depends on
// this, and no endpoint assertion can see it — Dmin and Dmax pin both ends
// whatever shape runs between them.
//
// It fits a straight line by least squares over ~220 samples and asserts the
// worst residual, rather than checking second differences at a handful of
// points. Measured: the correct case residual is 0.013 (8-bit quantisation
// alone), while a power curve or a real sigmoid in place of the identity
// measures 0.18-0.43. Second differences at five points separated the same
// cases by only 2.6x, which is not enough margin to rely on.
func TestCurveZeroIsStraightInDensity(t *testing.T) {
	l, _ := Lookup("flat")
	base := math.Min(l.Dmin[0], math.Min(l.Dmin[1], l.Dmin[2]))

	type pt struct{ e, d float64 }
	var pts []pt
	for v := 20; v <= 250; v++ {
		e := tone.Clamp01((math.Log10(math.Max(tone.SRGBToLinear(float64(v)/255), 1e-6)) + l.Scale + l.Exposure) / l.Scale)
		if e <= 0.02 || e >= 0.98 {
			continue // on a clamp, where density is pinned rather than curved
		}
		r, _, _ := at1(t, l, uint8(v), uint8(v), uint8(v))
		if r <= 1 || r >= 254 {
			continue // a clamped output carries no recoverable density
		}
		pts = append(pts, pt{e, -math.Log10(tone.SRGBToLinear(float64(r)/255)) + base})
	}
	if len(pts) < 100 {
		t.Fatalf("only %d usable samples; the fixture no longer exercises the curve", len(pts))
	}

	var n, se, sd, see, sed float64
	for _, p := range pts {
		n++
		se += p.e
		sd += p.d
		see += p.e * p.e
		sed += p.e * p.d
	}
	slope := (n*sed - se*sd) / (n*see - se*se)
	intercept := (sd - slope*se) / n

	var worst float64
	for _, p := range pts {
		if r := math.Abs(p.d - (intercept + slope*p.e)); r > worst {
			worst = r
		}
	}
	t.Logf("flat: %d samples, slope %.4f, worst residual %.5f", len(pts), slope, worst)
	if worst > 0.05 {
		t.Errorf("worst residual %.5f exceeds 0.05 — density is not affine in log exposure at Curve 0, so something other than tone.Sigmoid's identity is running", worst)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
func max3(a, b, c int) int { return max(a, max(b, c)) }
func min3(a, b, c int) int { return min(a, min(b, c)) }

// scene draws a synthetic landscape: a blue sky deepening upward, a soft
// bright cloud, and a hard-edged dark peak. The hard edge is what the clarity
// and scale-invariance tests need — a smooth gradient alone cannot exercise
// unsharp masking.
func scene(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fw, fh := float64(w), float64(h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u, v := float64(x)/fw, float64(y)/fh
			c := color.RGBA{R: uint8(30 + 60*v), G: uint8(80 + 70*v), B: uint8(200 - 30*v), A: 255}
			dx, dy := u-0.30, v-0.30
			if d := math.Hypot(dx, dy); d < 0.18 {
				k := 1 - d/0.18
				c = color.RGBA{
					R: uint8(float64(c.R) + k*(250-float64(c.R))),
					G: uint8(float64(c.G) + k*(250-float64(c.G))),
					B: uint8(float64(c.B) + k*(252-float64(c.B))),
					A: 255,
				}
			}
			if v > 0.55 && math.Abs(u-0.55) < (v-0.55)*1.4 {
				c = color.RGBA{R: 60, G: 54, B: 48, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// downTo scales a rendered image to size x size for comparison.
func downTo(src *image.RGBA, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

// meanAbsDiff compares two equally sized RGBA images over their colour
// channels only, in 0-255 units.
func meanAbsDiff(a, b *image.RGBA) float64 {
	var sum float64
	var n int
	for i := 0; i+3 < len(a.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			sum += math.Abs(float64(a.Pix[i+c]) - float64(b.Pix[i+c]))
			n++
		}
	}
	return sum / float64(n)
}

// TestClarityRaisesLocalContrast pins that clarity does something. Comparing a
// look against itself with Clarity zeroed isolates the stage.
func TestClarityRaisesLocalContrast(t *testing.T) {
	l, _ := Lookup("wet")
	off := l
	off.Clarity = 0
	src := scene(256, 256)
	with, without := Render(src, l), Render(src, off)
	if d := meanAbsDiff(with, without); d < 0.5 {
		t.Errorf("clarity %.2f changed the render by only %.3f mean units — the stage is not running", l.Clarity, d)
	}
}

// TestClarityDoesNotShiftHue pins that clarity is applied equally to all three
// channels. A per-channel unsharp mask would fringe on saturated edges, which
// after the density stage is most of the frame.
func TestClarityDoesNotShiftHue(t *testing.T) {
	l, _ := Lookup("wet")
	off := l
	off.Clarity = 0
	src := scene(256, 256)
	with, without := Render(src, l), Render(src, off)
	// A channel's clamps are its own: zero at the bottom, and the look's
	// per-channel paper white at the top, which for wet is (255,252,250) — NOT
	// 255 for all three. A pixel sitting on any of those legitimately shows a
	// per-channel difference that is the clamp, not a hue shift, so it is
	// skipped.
	//
	// The band is two units wide rather than exact equality because addAll
	// clamps each channel independently in each of the two spatial stages, so
	// a channel clipped by clarity can be pulled a unit or two back off its
	// clamp by bloom's own separately-clamped delta and land just short of the
	// extreme while still carrying the clamp asymmetry.
	//
	// Measured on this fixture: the band derived from paperWhite leaves 56716
	// of 65536 pixels examined and zero violations of the bound below. A band
	// hardcoded at 253 for every channel instead — which is what this test
	// used while both stages clamped at 1.0 — leaves 3487 violations up to 13
	// units, every one of them at B=250, wet's own blue ceiling.
	pw := paperWhite(l)
	var top [3]int
	for c := range top {
		top[c] = int(math.Round(pw[c] * 255))
	}
	onClamp := func(p []uint8) bool {
		for c := 0; c < 3; c++ {
			if int(p[c]) <= 2 || int(p[c]) >= top[c]-2 {
				return true
			}
		}
		return false
	}
	// Where clarity moved a pixel, it must have moved all three channels by
	// the same amount, bar the one unit rounding can cost.
	var examined int
	for i := 0; i+3 < len(with.Pix); i += 4 {
		if onClamp(with.Pix[i : i+4]) {
			continue
		}
		var d [3]int
		for c := 0; c < 3; c++ {
			d[c] = int(with.Pix[i+c]) - int(without.Pix[i+c])
		}
		examined++
		if abs(d[0]-d[1]) > 1 || abs(d[1]-d[2]) > 1 {
			t.Fatalf("pixel %d: clarity moved channels by %v — it is not hue-neutral", i/4, d)
		}
	}
	// After the density stage most of a saturated frame sits on a clamp; log
	// how much of the 65536-pixel frame the assertion above actually covers
	// rather than leaving that coverage assumed.
	t.Logf("examined %d of %d pixels off the clamp", examined, len(with.Pix)/4)
	if examined == 0 {
		t.Fatalf("every pixel sat on a clamp — this test asserted nothing")
	}
}

// TestBloomLiftIsLocalToHighlights pins the bloom stage, and pins that its
// lift correlates with local brightness: a point next to the bright cloud
// must lift more than a point in the deep-shadow peak. The name says "local",
// not "highlights only", because the near>far clause below is all this body
// pins — see the paragraph after it.
//
// This is weaker than "highlights only": mutation-tested by dropping the
// BloomThresh normalisation (bloom the whole frame proportional to raw luma
// instead of only the excess above the threshold), near still measured 12.00
// against far's 3.00 — near>far survives because the cloud edge is simply
// much brighter than the dark peak regardless of gating, so this comparison
// cannot by itself distinguish "gated to highlights" from "proportional to
// brightness everywhere". TestBloomThresholdIsRespected is what actually
// pins the gating — it is the one that fails under that mutation.
func TestBloomLiftIsLocalToHighlights(t *testing.T) {
	l, _ := Lookup("wet")
	off := l
	off.Bloom = 0
	src := scene(256, 256)
	with, without := Render(src, l), Render(src, off)
	if d := meanAbsDiff(with, without); d < 0.2 {
		t.Errorf("bloom %.2f changed the render by only %.3f mean units — the stage is not running", l.Bloom, d)
	}
	// (120,77) is on the edge of the cloud disc itself (u=0.469, v=0.301,
	// d=0.169 against the 0.18 disc radius, blended at k=0.06 in scene()) —
	// not "two cloud-radii away, well outside the disc" as an earlier draft
	// of this comment claimed. (230,230) is far from the cloud but lands
	// inside the dark peak triangle (v>0.55, within the wedge), which is
	// guaranteed near-zero luminance rather than a generic background
	// sample. Both were checked against a genuine two-cloud-radii sky point
	// (168,77), outside the disc and outside the peak, and a plain far-sky
	// point (230,40): both alternates also lift by 0.000, matching (230,230)
	// exactly, so the near>far result here does not depend on the peak
	// triangle's placement — it is not passing by that accident.
	lift := func(x, y int) float64 {
		i := (y*256 + x) * 4
		return float64(with.Pix[i]) - float64(without.Pix[i])
	}
	near, far := lift(120, 77), lift(230, 230)
	t.Logf("lift near cloud edge (120,77)=%.2f, lift far (230,230, in dark peak)=%.2f", near, far)
	if near <= far {
		t.Errorf("bloom lifted a pixel near the cloud by %.1f and one far from it by %.1f — bloom is not local to highlights", near, far)
	}
}

// TestBloomThresholdIsRespected guards the divisor in the highlight
// normalisation and the "highlights only" contract together.
func TestBloomThresholdIsRespected(t *testing.T) {
	l, _ := Lookup("wet")
	high := l
	high.BloomThresh = 0.999 // almost nothing exceeds it
	src := scene(256, 256)
	if d := meanAbsDiff(Render(src, l), Render(src, high)); d < 0.1 {
		t.Errorf("raising bloomThresh to 0.999 changed the render by %.3f — the threshold is not gating anything", d)
	}
}

// scaleInvarianceBound is TestScaleInvariance's own pass/fail line: mean abs
// difference between the preview path and the full-resolution path, in 0-255
// units, at the 2048px "large" fixture size.
//
// This was originally 2.5, carried over in spirit from internal/bw's 2.0 on
// the assumption that a pixel-radius regression there ("measures several
// units") would look the same here. It does not: bw's clarity acts on the
// whole tonal buffer, ciba's acts additively on luminance at Clarity
// 0.35-0.45, so the same class of bug is smaller in ciba's units. Measured
// directly instead of assumed: correct renders across the five presets are
// 0.00-0.34 (see TestScaleInvariance's own -v log), and the pinned-radius
// regression TestScaleInvarianceHasTeeth manufactures measures 1.14. 0.8
// sits between them — ~2.4x headroom above the worst correct case, 1.4x
// margin under the regression.
//
// Both measured figures move with Clarity, since the unsharp delta scales
// linearly with it: lowering wet's Clarity from 0.55 to 0.45 took the
// regression from 1.38 to 1.14 and wet's own correct case from 0.31 to 0.26.
// Re-measure rather than re-deriving if a preset's Clarity changes again.
const scaleInvarianceBound = 0.8

// TestScaleInvariance is the executable form of the preview contract: what you
// pick on a 256px contact-sheet tile is what you get at full resolution.
//
// It compares the two paths the product actually uses, from ONE source image:
// the preview path (thumbnail, then render) against the full-resolution path
// (render, then downscale). Comparing two independently generated scene()
// images instead would fold the fixture's own point-sampling artefact into the
// measurement and never exercise Thumbnail at all.
//
// See scaleInvarianceBound's own comment for where the threshold comes from;
// TestScaleInvarianceHasTeeth proves it actually separates correct renders
// from a real regression rather than assuming it does.
func TestScaleInvariance(t *testing.T) {
	const small, large = 256, 2048
	src := scene(large, large)
	for _, l := range Builtins() {
		preview := Render(imaging.Thumbnail(src, small), l)
		fullres := downTo(Render(src, l), small)
		d := meanAbsDiff(preview, fullres)
		t.Logf("%-9s %.2f", l.Name, d)
		if d > scaleInvarianceBound {
			t.Errorf("%s: mean abs difference %.2f exceeds %.2f — the preview does not predict the full-resolution render", l.Name, d, scaleInvarianceBound)
		}
	}
}

// cameraResolutionBound is TestScaleInvarianceAtCameraResolution's own
// pass/fail line. Kept as its own named constant rather than reusing
// scaleInvarianceBound, even though both currently land on 0.8, so a future
// retune of either fixture can move independently of the other.
//
// wet's correct case at 4096px measures 0.32 (see the test's own -v log);
// 0.8 leaves comparable headroom to scaleInvarianceBound's, on the same
// reasoning: this is the same class of regression at a larger, more
// realistic size, not a different question needing a different kind of
// margin.
const cameraResolutionBound = 0.50

// TestScaleInvarianceAtCameraResolution guards the range the product is used
// at. Phone output runs past 4000px, so a clamp or narrowing conversion that
// only engages above 2048 would be invisible to TestScaleInvariance while
// breaking every real render. One look suffices: the one with the largest
// clarity amount and the widest bloom.
//
// This is a BACKSTOP, not the primary guard. internal/tone's
// TestRadiusPxIsProportionalAtEveryScale is the primary guard for the radius
// contract: it checks tone.RadiusPx itself, catches a cap at ANY scale to a
// one-pixel tolerance, and tests up to 16384px — a far tighter check than
// this test's whole-frame mean-diff comparison could ever be. What this test
// adds on top is coverage of a defect that would slip PAST that primary
// guard if it were introduced somewhere else in the pipeline (not in
// RadiusPx itself) and only engaged above 2048px, which
// TestScaleInvariance's 2048px fixture cannot see.
//
// Its margin against a synthetic regression is correspondingly thinner than
// the primary guard's, and that thinness is measured rather than assumed:
// correct case (wet @ 4096px) measures 0.27, and a cap of tone.RadiusPx at
// 50px measures 0.74 against the 0.50 bound. 50px is the cap worth citing
// because it truncates BOTH wet's clarity radius (82px at 4096) and its bloom
// radius (287px), where a cap at 200 clips only the lower-weight bloom radius
// and measures 0.38 — under any workable bound, so it does not bite.
//
// The bound is 0.50 and not 0.80 because 0.80 stopped working. It was set when
// wet's Clarity was 0.55, where the cap-50 regression measured 0.85 — 6% clear
// of the bound. Lowering wet's Clarity to 0.45 took the regression to 0.74 and
// the correct case to 0.27, so 0.80 sat ABOVE the regression and this test
// silently stopped detecting the defect it exists for. It was caught by
// re-running the mutation after the preset change rather than by any failure.
// At 0.50 the correct case has ~1.9x headroom and the regression ~1.5x margin,
// which is better balanced than the original ever was.
//
// The margin here is bounded structurally, not by how hard the mutation bites:
// at the 256px preview size the radii are already small (5px clarity, 18px
// bloom, both below any plausible cap), so a cap changes only the
// full-resolution path, and the resulting mean-diff ceiling is set by that
// asymmetry rather than by cap severity.
//
// Anything that changes wet's Clarity, Radius, Bloom or BloomRadius moves both
// of these figures. Re-run the cap-50 mutation and re-measure; do not assume
// the bound still separates them. That is exactly how 0.80 came to be wrong,
// and nothing in the suite went red to announce it.
//
// The pipeline is deterministic, so 0.74 against 0.50 is an exact comparison
// rather than noise near a cliff edge. It is still the thinnest margin in this
// package, and a change that narrows it further should move the bound too.
func TestScaleInvarianceAtCameraResolution(t *testing.T) {
	const small, large = 256, 4096
	l, _ := Lookup("wet")
	src := scene(large, large)
	preview := Render(imaging.Thumbnail(src, small), l)
	fullres := downTo(Render(src, l), small)
	d := meanAbsDiff(preview, fullres)
	t.Logf("wet @%d %.2f", large, d)
	if d > cameraResolutionBound {
		t.Errorf("wet at %dpx: mean abs difference %.2f exceeds %.2f", large, d, cameraResolutionBound)
	}
}

// TestScaleInvarianceHasTeeth proves TestScaleInvariance can actually fail. It
// renders the large image twice — once with the correct fractional radii and
// once with them pinned so they resolve to the same ABSOLUTE pixel radius the
// small render uses, which is exactly what a pixel-count implementation would
// do — and checks the invariance check separates the two with real margin on
// TWO independent measures.
//
// The first check reuses scaleInvarianceBound deliberately, not by mistake:
// "would TestScaleInvariance catch this regression?" is literally the same
// question as TestScaleInvariance's own bound, so comparing the pinned case
// against it directly answers that question. (An earlier version of this
// test read `buggy < 4*correct || buggy < 2.5`, hardcoding the OLD 2.5
// bound as a second, disconnected literal — that WAS a mistake, because it
// meant this test and TestScaleInvariance's threshold could silently drift
// apart. Naming the shared constant instead of copying its value is what
// keeps them from drifting apart again.)
//
// The second check is a RATIO, independent of either bound: clarity acts
// only on edges, so the whole-frame mean error from a wrong radius depends
// on how much edge content the fixture happens to have, which the ratio
// does not. It is 3x rather than 4x deliberately: measured ratio is 4.27,
// which leaves only ~7% margin at a 4x bar — too thin to be reliable
// against ordinary fixture noise. At 3x there is real margin on both
// checks, and both mean something different: the bug is at least 3x worse
// than the correct case, AND it is large enough in absolute terms that
// TestScaleInvariance would actually flag it.
func TestScaleInvarianceHasTeeth(t *testing.T) {
	const small, large = 256, 2048
	l, _ := Lookup("wet")
	pinned := l
	pinned.Radius = l.Radius * float64(small) / float64(large)
	pinned.BloomRadius = l.BloomRadius * float64(small) / float64(large)

	src := scene(large, large)
	preview := Render(imaging.Thumbnail(src, small), l)
	correct := meanAbsDiff(preview, downTo(Render(src, l), small))
	buggy := meanAbsDiff(preview, downTo(Render(src, pinned), small))
	t.Logf("correct=%.2f buggy=%.2f (ratio=%.2f)", correct, buggy, buggy/correct)
	if buggy < scaleInvarianceBound {
		t.Fatalf("pixel-radius bug produced mean diff %.2f, under TestScaleInvariance's own %.2f bound; TestScaleInvariance cannot detect this regression",
			buggy, scaleInvarianceBound)
	}
	if buggy < 3*correct {
		t.Fatalf("pixel-radius bug produced mean diff %.2f, only %.2fx the correct-case %.2f — too thin a margin to rely on",
			buggy, buggy/correct, correct)
	}
}

// TestSpatialRadiiUseTheShortEdge pins the "short edge" half of the radius
// contract.
//
// A first version of this test rendered scene(512, 128) and scene(128,128)
// and compared a column at u=0.30, attributing any
// difference to how the radius is measured. That mechanism is confounded: the
// INPUT values along u=0.30 are identical functions of v in both frames since
// scene() depends only on normalised (u,v), but the BLUR NEIGHBOURHOOD is not
// comparable between them. At radius 3px in both, the wide frame averages
// over a normalised horizontal extent of 3/512 = 0.0059 while the square frame
// averages over 3/128 = 0.023 — a 4x difference — and the cloud is an ellipse
// in the wide frame and a circle in the square one. The two frames genuinely
// differ in normalised-space content sharpness for reasons that have nothing
// to do with which edge the radius is measured against, so no threshold on
// that comparison can cleanly attribute what it measures. This is the same
// failure mode internal/bw's TestClarityRadiusUsesShortEdge documents for its
// own first attempt: a confounded two-frame comparison measured LOWER, not
// higher, under the bug (1.01 correct vs 0.69 mutated), so it could not
// discriminate at any threshold.
//
// This version instead follows internal/bw's working pattern: one wide frame,
// so no content difference can confound it. Render's own pointwise stages are
// reused (calling Render with Clarity and Bloom both zero — under which
// spatial is a no-op — instead of re-deriving stages 1-5 by hand) to get the
// pre-clarity buffer, and the package's own luma/addAll helpers then reproduce
// the clarity stage a second time with the radius that SHOULD apply: a
// fraction of h, the short edge of a deliberately wide 512x128 frame. Bloom is
// zeroed throughout so only the clarity radius is under test.
func TestSpatialRadiiUseTheShortEdge(t *testing.T) {
	const w, h = 512, 128 // short edge is h
	l, _ := Lookup("classic")
	l.Bloom = 0 // isolate clarity; bloom's radius is not this contract

	src := scene(w, h)
	noClarity := l
	noClarity.Clarity = 0
	pre := Render(src, noClarity) // stages 1-5 only: spatial is a no-op here

	// Re-derived here rather than read back from tone.RadiusPx, matching
	// internal/bw's TestClarityRadiusUsesShortEdge: a cap or floor introduced
	// inside RadiusPx would cancel out of both sides of the comparison below
	// and leave this test green while every render became
	// resolution-dependent.
	wantRadius := int(math.Round(l.Radius * float64(h))) // h is the short edge
	y := make([]float64, w*h)
	lumaInto(y, pre)
	blur := tone.BlurBox3(y, w, h, wantRadius)
	want := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(want.Pix, pre.Pix)
	ceiling := paperWhite(l)
	for i := range y {
		addAll(want, i, l.Clarity*(y[i]-blur[i]), ceiling)
	}

	got := Render(src, l)
	if d := meanAbsDiff(got, want); d > 1 {
		t.Fatalf("mean abs difference %.2f between Render's clarity effect and a manually computed short-edge (h=%d) radius of %dpx — the short edge is not what is being used", d, h, wantRadius)
	}
}
