package ciba

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestBuiltinsAreValid(t *testing.T) {
	for _, l := range Builtins() {
		if err := l.Validate(); err != nil {
			t.Errorf("built-in %q is invalid: %v", l.Name, err)
		}
		if l.Desc == "" {
			t.Errorf("built-in %q has no description; -list prints it", l.Name)
		}
	}
}

func TestDefaultLookExists(t *testing.T) {
	if _, ok := Lookup(DefaultLook); !ok {
		t.Fatalf("DefaultLook %q is not a built-in", DefaultLook)
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("no-such-look"); ok {
		t.Fatal("Lookup accepted a name that is not a built-in")
	}
}

// TestValidateRejectsNaN pins the reason an explicit NaN sweep exists at all:
// every range check is a < or > comparison, false for NaN in both directions,
// so without the sweep a NaN field sails through every check and reaches the
// pixel loop.
func TestValidateRejectsNaN(t *testing.T) {
	nan := math.NaN()
	base, _ := Lookup("classic")
	mutate := map[string]func(*Look){
		"scale":       func(l *Look) { l.Scale = nan },
		"exposure":    func(l *Look) { l.Exposure = nan },
		"curve[0]":    func(l *Look) { l.Curve[0] = nan },
		"curve[2]":    func(l *Look) { l.Curve[2] = nan },
		"pivot[1]":    func(l *Look) { l.Pivot[1] = nan },
		"dmin[0]":     func(l *Look) { l.Dmin[0] = nan },
		"dmax[2]":     func(l *Look) { l.Dmax[2] = nan },
		"clarity":     func(l *Look) { l.Clarity = nan },
		"radius":      func(l *Look) { l.Radius = nan },
		"bloom":       func(l *Look) { l.Bloom = nan },
		"bloomRadius": func(l *Look) { l.BloomRadius = nan },
		"bloomThresh": func(l *Look) { l.BloomThresh = nan },
	}
	for field, m := range mutate {
		l := base
		m(&l)
		err := l.Validate()
		if err == nil {
			t.Errorf("%s = NaN was accepted", field)
			continue
		}
		if !strings.Contains(err.Error(), "NaN") {
			t.Errorf("%s = NaN rejected with %q, which does not name NaN", field, err)
		}
	}
}

func TestValidateRejectsOutOfRange(t *testing.T) {
	base, _ := Lookup("classic")
	for name, m := range map[string]func(*Look){
		"no name":              func(l *Look) { l.Name = "" },
		"zero scale":           func(l *Look) { l.Scale = 0 },
		"negative scale":       func(l *Look) { l.Scale = -1 },
		"huge scale":           func(l *Look) { l.Scale = 4.1 },
		"exposure too high":    func(l *Look) { l.Exposure = 2.1 },
		"exposure too low":     func(l *Look) { l.Exposure = -2.1 },
		"curve negative":       func(l *Look) { l.Curve[1] = -0.1 },
		"curve too strong":     func(l *Look) { l.Curve[1] = 30.1 },
		"pivot below zero":     func(l *Look) { l.Pivot[0] = -0.01 },
		"pivot above one":      func(l *Look) { l.Pivot[0] = 1.01 },
		"dmin above one":       func(l *Look) { l.Dmin[2] = 1.01 },
		"dmin negative":        func(l *Look) { l.Dmin[2] = -0.01 },
		"dmax above five":      func(l *Look) { l.Dmax[0] = 5.01 },
		"clarity too strong":   func(l *Look) { l.Clarity = 2.01 },
		"radius too wide":      func(l *Look) { l.Radius = 0.26 },
		"bloom too strong":     func(l *Look) { l.Bloom = 1.01 },
		"bloom radius wide":    func(l *Look) { l.BloomRadius = 0.51 },
		"thresh at one":        func(l *Look) { l.BloomThresh = 1 },
		"clarity negative":     func(l *Look) { l.Clarity = -0.01 },
		"radius negative":      func(l *Look) { l.Radius = -0.01 },
		"bloom negative":       func(l *Look) { l.Bloom = -0.01 },
		"bloomRadius negative": func(l *Look) { l.BloomRadius = -0.01 },
		"bloomThresh negative": func(l *Look) { l.BloomThresh = -0.01 },
	} {
		l := base
		m(&l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestValidateAcceptsItsOwnBoundaries pins the inclusive side of every bound
// that has one. A rejection table alone cannot: no preset comes near most of
// these limits — Scale tops out at 2.20 against a bound of 4 — so turning a
// `>` into a `>=` narrows the documented range with nothing to notice.
//
// BloomThresh is deliberately absent: its bound is exclusive (`>= 1` is
// rejected, because 1.0 divides by zero in the highlight normalisation), so
// there is no valid value at the boundary to assert.
func TestValidateAcceptsItsOwnBoundaries(t *testing.T) {
	base, _ := Lookup("classic")
	for name, m := range map[string]func(*Look){
		"scale at the maximum":        func(l *Look) { l.Scale = 4.0 },
		"exposure at the maximum":     func(l *Look) { l.Exposure = 2.0 },
		"exposure at the minimum":     func(l *Look) { l.Exposure = -2.0 },
		"curve at the maximum":        func(l *Look) { l.Curve = [3]float64{30, 30, 30} },
		"curve at zero":               func(l *Look) { l.Curve = [3]float64{0, 0, 0} },
		"pivot at zero":               func(l *Look) { l.Pivot = [3]float64{0, 0, 0} },
		"pivot at one":                func(l *Look) { l.Pivot = [3]float64{1, 1, 1} },
		"dmin at zero":                func(l *Look) { l.Dmin = [3]float64{0, 0, 0} },
		"dmin at one":                 func(l *Look) { l.Dmin = [3]float64{1, 1, 1} },
		"dmax at the maximum":         func(l *Look) { l.Dmax = [3]float64{5, 5, 5} },
		"clarity at zero":             func(l *Look) { l.Clarity = 0 },
		"clarity at the maximum":      func(l *Look) { l.Clarity = 2 },
		"radius at zero":              func(l *Look) { l.Radius = 0 },
		"radius at the maximum":       func(l *Look) { l.Radius = 0.25 },
		"bloom at zero":               func(l *Look) { l.Bloom = 0 },
		"bloom at the maximum":        func(l *Look) { l.Bloom = 1 },
		"bloom radius at zero":        func(l *Look) { l.BloomRadius = 0 },
		"bloom radius at the maximum": func(l *Look) { l.BloomRadius = 0.5 },
		"bloom thresh at zero":        func(l *Look) { l.BloomThresh = 0 },
	} {
		l := base
		m(&l)
		if err := l.Validate(); err != nil {
			t.Errorf("%s was rejected: %v", name, err)
		}
	}
}

// TestValidateRejectsInvertedDensity guards the one range check that is not a
// constant bound: a channel whose Dmax does not exceed its Dmin renders that
// channel inverted or flat, and no fixed bound catches it.
func TestValidateRejectsInvertedDensity(t *testing.T) {
	base, _ := Lookup("classic")
	for _, c := range []int{0, 1, 2} {
		l := base
		l.Dmax[c] = l.Dmin[c]
		if err := l.Validate(); err == nil {
			t.Errorf("channel %d with Dmax == Dmin was accepted", c)
		}
		l.Dmax[c] = l.Dmin[c] - 0.1
		if err := l.Validate(); err == nil {
			t.Errorf("channel %d with Dmax < Dmin was accepted", c)
		}
	}
}

// TestLookJSONRoundTrip matters because a Look is what a future recipe sidecar
// or -export would carry. A field without a tag serialises under its Go name
// and silently stops round-tripping.
func TestLookJSONRoundTrip(t *testing.T) {
	want, _ := Lookup("azo")
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Look
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round trip changed the look:\n got %+v\nwant %+v", got, want)
	}
	for _, tag := range []string{`"scale"`, `"exposure"`, `"curve"`, `"pivot"`,
		`"dmin"`, `"dmax"`, `"clarity"`, `"radius"`, `"bloom"`,
		`"bloomRadius"`, `"bloomThresh"`} {
		if !strings.Contains(string(b), tag) {
			t.Errorf("marshalled JSON has no %s field: %s", tag, b)
		}
	}
}
