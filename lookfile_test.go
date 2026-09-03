package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tom-hoover/ciba/internal/ciba"
)

// writeLookFile writes l as JSON at path, creating parent directories.
func writeLookFile(t *testing.T, path string, l ciba.Look) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tweaked returns a built-in with one recognisable parameter change, so a test
// can tell which copy of a look it got back.
func tweaked(t *testing.T, name string, clarity float64) ciba.Look {
	t.Helper()
	l, ok := ciba.Lookup(name)
	if !ok {
		t.Fatalf("built-in %q missing", name)
	}
	l.Clarity = clarity
	return l
}

// TestResolveLookPrefersBuiltinOverFile pins the precedence that keeps a stray
// file in the working directory from redefining what a shipped look means.
// Without it, dropping a classic.json into a photo directory would silently
// change every render there.
func TestResolveLookPrefersBuiltinOverFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	builtin, _ := ciba.Lookup("classic")
	writeLookFile(t, "classic.json", tweaked(t, "classic", 1.75))

	got, _, err := resolveLook("classic")
	if err != nil {
		t.Fatal(err)
	}
	if got.Clarity != builtin.Clarity {
		t.Errorf("resolveLook(\"classic\") returned clarity %.2f, want the built-in's %.2f — a file shadowed a shipped look",
			got.Clarity, builtin.Clarity)
	}
}

// TestResolveLookFindsFileInWorkingDirectory covers the per-project case: a
// look file checked in beside a shoot.
func TestResolveLookFindsFileInWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	writeLookFile(t, "mylook.json", tweaked(t, "classic", 1.25))

	got, _, err := resolveLook("mylook")
	if err != nil {
		t.Fatal(err)
	}
	if got.Clarity != 1.25 {
		t.Errorf("clarity = %.2f, want 1.25 — the working-directory file was not read", got.Clarity)
	}
}

// TestResolveLookFindsFileInLooksDir covers the personal library case.
func TestResolveLookFindsFileInLooksDir(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "looks")
	t.Setenv(looksDirEnv, dir)

	writeLookFile(t, filepath.Join(dir, "mylook.json"), tweaked(t, "classic", 0.95))

	got, _, err := resolveLook("mylook")
	if err != nil {
		t.Fatal(err)
	}
	if got.Clarity != 0.95 {
		t.Errorf("clarity = %.2f, want 0.95 — the looks directory was not consulted", got.Clarity)
	}
}

// TestResolveLookPrefersWorkingDirectoryOverLooksDir pins the second half of
// the precedence chain: a look committed beside a project wins over a
// same-named one in the personal library, so a shoot carries its own settings.
func TestResolveLookPrefersWorkingDirectoryOverLooksDir(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "looks")
	t.Setenv(looksDirEnv, dir)

	writeLookFile(t, "mylook.json", tweaked(t, "classic", 1.10))
	writeLookFile(t, filepath.Join(dir, "mylook.json"), tweaked(t, "classic", 0.20))

	got, _, err := resolveLook("mylook")
	if err != nil {
		t.Fatal(err)
	}
	if got.Clarity != 1.10 {
		t.Errorf("clarity = %.2f, want the working directory's 1.10, got what looks like the library's 0.20", got.Clarity)
	}
}

// TestResolveLookUsesTheFilenameAsTheName keeps the name the user typed and the
// name in messages and sheet labels in agreement. A file whose json disagrees
// with its filename would otherwise render under a label nobody can pass back
// to -style.
func TestResolveLookUsesTheFilenameAsTheName(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	l := tweaked(t, "classic", 0.5)
	l.Name = "something-else"
	writeLookFile(t, "mylook.json", l)

	got, _, err := resolveLook("mylook")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "mylook" {
		t.Errorf("name = %q, want \"mylook\" — the file's own name field won over the filename the user typed", got.Name)
	}
}

// TestResolveLookRejectsOutOfRangeFile pins that a hand-edited file goes
// through the same Validate the built-ins do, and that the message names the
// offending field rather than failing later in the pixel loop.
func TestResolveLookRejectsOutOfRangeFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	l := tweaked(t, "classic", 0.35)
	l.Clarity = 9 // Validate caps clarity at 2
	writeLookFile(t, "bad.json", l)

	_, _, err := resolveLook("bad")
	if err == nil {
		t.Fatal("an out-of-range clarity was accepted from a file")
	}
	if !strings.Contains(err.Error(), "clarity") {
		t.Errorf("error %q does not name the offending field", err)
	}
}

// TestResolveLookReportsMalformedJSONWithItsPath separates "you named a look
// that does not exist" from "the file you meant has a stray comma". Reporting
// the former for the latter sends the reader to -list, which cannot help.
//
// Checking only that the message names the file is NOT enough, and a first
// version of this test made exactly that mistake: the not-found message also
// names the file it looked for ("no broken.json in the working directory or
// ..."), so it passed whether or not parse errors were distinguished at all.
// The behavioural difference is where the message sends the reader, so that is
// what this asserts.
func TestResolveLookReportsMalformedJSONWithItsPath(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	if err := os.WriteFile("broken.json", []byte("{\"scale\": 1.7,}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := resolveLook("broken")
	if err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error %q does not name the file that failed to parse", err)
	}
	if strings.Contains(err.Error(), "-list") {
		t.Errorf("a parse error was reported as an unknown look, pointing at -list: %q", err)
	}
	if strings.Contains(err.Error(), "not a built-in") {
		t.Errorf("a parse error was collapsed into the not-found message: %q", err)
	}
}

// TestResolveLookUnknownNameMentionsList checks the one case where -list is
// genuinely the right next step.
func TestResolveLookUnknownNameMentionsList(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	_, _, err := resolveLook("no-such-look")
	if err == nil {
		t.Fatal("an unknown look name was accepted")
	}
	if !strings.Contains(err.Error(), "-list") {
		t.Errorf("error %q should point at -list", err)
	}
}

// TestApplyOverridesLeavesUnsetParametersAlone is the guard against the
// defaults trap: registering seven float flags and applying all of them
// unconditionally would zero every parameter the user did not mention.
//
// classic's clarity is 0.35 and every other overridable parameter is likewise
// non-zero, so a version that applied unset flags would visibly differ. A
// fixture whose values happened to equal the zero value would make this test
// pass either way.
func TestApplyOverridesLeavesUnsetParametersAlone(t *testing.T) {
	base, _ := ciba.Lookup("classic")
	for name, v := range map[string]float64{
		"scale": base.Scale, "exposure": base.Exposure, "clarity": base.Clarity,
		"radius": base.Radius, "bloom": base.Bloom,
		"bloom-radius": base.BloomRadius, "bloom-thresh": base.BloomThresh,
	} {
		if v == 0 {
			t.Fatalf("fixture is toothless: classic's %s is already 0, so this test cannot detect an unset flag being applied", name)
		}
	}

	if got := applyOverrides(base, nil); got != base {
		t.Errorf("applying no overrides changed the look:\n got %+v\nwant %+v", got, base)
	}
	if got := applyOverrides(base, map[string]float64{}); got != base {
		t.Errorf("applying an empty override set changed the look:\n got %+v\nwant %+v", got, base)
	}
}

// TestApplyOverridesAppliesSetParameters covers all seven overridable
// parameters, so a missing entry in the setter table is caught.
func TestApplyOverridesAppliesSetParameters(t *testing.T) {
	base, _ := ciba.Lookup("classic")
	for _, c := range []struct {
		flag string
		want float64
		get  func(ciba.Look) float64
	}{
		{"scale", 1.9, func(l ciba.Look) float64 { return l.Scale }},
		{"exposure", 0.30, func(l ciba.Look) float64 { return l.Exposure }},
		{"clarity", 0.60, func(l ciba.Look) float64 { return l.Clarity }},
		{"radius", 0.030, func(l ciba.Look) float64 { return l.Radius }},
		{"bloom", 0.20, func(l ciba.Look) float64 { return l.Bloom }},
		{"bloom-radius", 0.090, func(l ciba.Look) float64 { return l.BloomRadius }},
		{"bloom-thresh", 0.70, func(l ciba.Look) float64 { return l.BloomThresh }},
	} {
		got := applyOverrides(base, map[string]float64{c.flag: c.want})
		if v := c.get(got); v != c.want {
			t.Errorf("-%s %.3f produced %.3f", c.flag, c.want, v)
		}
		// Nothing else may move: the setter table must touch one field each.
		if c.flag != "clarity" && got.Clarity != base.Clarity {
			t.Errorf("-%s also changed clarity, %.3f -> %.3f", c.flag, base.Clarity, got.Clarity)
		}
	}
}

// TestOverridableCoversEveryScalarAndNoArray pins the design decision that the
// per-channel parameters are deliberately not overridable from the command
// line. Curve, Pivot, Dmin and Dmax are [3]float64, and Dmin/Dmax are coupled:
// lowering one channel's Dmax without raising its Dmin lifts that channel at
// every tone, which is a white-balance error rather than a hue shift. Files are
// the only route to those.
func TestOverridableCoversEveryScalarAndNoArray(t *testing.T) {
	want := []string{"bloom", "bloom-radius", "bloom-thresh", "clarity", "exposure", "radius", "scale"}
	var got []string
	for name := range overridable {
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("overridable has %d entries %v, want %d %v", len(got), got, len(want), want)
	}
	for _, name := range want {
		if _, ok := overridable[name]; !ok {
			t.Errorf("overridable is missing %q", name)
		}
	}
	for _, banned := range []string{"curve", "pivot", "dmin", "dmax"} {
		if _, ok := overridable[banned]; ok {
			t.Errorf("%q is overridable from the command line; per-channel parameters must go through a file", banned)
		}
	}
}

// TestDumpLookRoundTripsThroughAFile is what makes -dump a usable starting
// point: dumping a look and resolving the result must give back the same look.
func TestDumpLookRoundTripsThroughAFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	want := applyOverrides(mustLook(t, "wet"), map[string]float64{"clarity": 0.40})
	want.Name = "mywet"

	var buf bytes.Buffer
	if err := dumpLook(&buf, want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("mywet.json", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _, err := resolveLook("mywet")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("dump then resolve did not round trip:\n got %+v\nwant %+v", got, want)
	}
}

// TestCustomLooksAreOfferedForPreview pins that the contact sheet shows looks
// from the personal library alongside the built-ins. Without it, authoring a
// look means rendering one at a time to compare it against the presets.
func TestCustomLooksAreOfferedForPreview(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "looks")
	t.Setenv(looksDirEnv, dir)

	writeLookFile(t, filepath.Join(dir, "mine.json"), tweaked(t, "classic", 0.42))

	looks, err := previewLooks()
	if err != nil {
		t.Fatal(err)
	}
	if len(looks) != len(ciba.Builtins())+1 {
		t.Fatalf("previewLooks returned %d looks, want %d built-ins plus one custom",
			len(looks), len(ciba.Builtins()))
	}
	// Built-ins first, so the sheet reads the same way every time regardless of
	// what happens to be in the library.
	for i, b := range ciba.Builtins() {
		if looks[i].Name != b.Name {
			t.Errorf("position %d is %q, want the built-in %q", i, looks[i].Name, b.Name)
		}
	}
	last := looks[len(looks)-1]
	if last.Name != "mine" || last.Clarity != 0.42 {
		t.Errorf("custom look came back as %q clarity %.2f, want \"mine\" 0.42", last.Name, last.Clarity)
	}
}

// TestPreviewLooksSkipsAnUnreadableFile keeps one bad file in the library from
// making -preview useless. A stray comma in one look must not stop the other
// looks being compared.
func TestPreviewLooksSkipsAnUnreadableFile(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "looks")
	t.Setenv(looksDirEnv, dir)

	writeLookFile(t, filepath.Join(dir, "good.json"), tweaked(t, "classic", 0.42))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{,}"), 0o644); err != nil {
		t.Fatal(err)
	}

	looks, err := previewLooks()
	if err != nil {
		t.Fatalf("one malformed file failed the whole preview: %v", err)
	}
	names := make([]string, len(looks))
	for i, l := range looks {
		names[i] = l.Name
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "good") {
		t.Errorf("the readable look is missing from %q", joined)
	}
	if strings.Contains(joined, "bad") {
		t.Errorf("the malformed look was included in %q", joined)
	}
}

func mustLook(t *testing.T, name string) ciba.Look {
	t.Helper()
	l, ok := ciba.Lookup(name)
	if !ok {
		t.Fatalf("built-in %q missing", name)
	}
	return l
}

// TestResolveLookReportsItsOrigin covers the answer to "is my file actually
// being used". Without it, a run reports the same thing whether it loaded a
// hand-edited look or silently used a built-in, which leaves breaking the
// values wildly as the only way to find out.
func TestResolveLookReportsItsOrigin(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	writeLookFile(t, "mylook.json", tweaked(t, "classic", 1.25))

	_, origin, err := resolveLook("mylook")
	if err != nil {
		t.Fatal(err)
	}
	if origin != "mylook.json" {
		t.Errorf("origin = %q, want \"mylook.json\"", origin)
	}

	// A built-in reports no origin: naming a file for it would be a lie, and
	// printing "built-in" on every ordinary run is noise.
	_, origin, err = resolveLook("classic")
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		t.Errorf("origin = %q for a built-in, want empty", origin)
	}
}

// TestShadowedLookFilesFindsAFileABuiltinHides is the trap most likely to
// produce "I edited a look and nothing changed": name a file after a built-in
// and it is never read, because built-ins win. Silence there is what costs an
// afternoon.
func TestShadowedLookFilesFindsAFileABuiltinHides(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "looks")
	t.Setenv(looksDirEnv, dir)

	writeLookFile(t, "wet.json", tweaked(t, "wet", 1.5))
	writeLookFile(t, filepath.Join(dir, "wet.json"), tweaked(t, "wet", 1.5))

	got := shadowedLookFiles("wet")
	if len(got) != 2 {
		t.Fatalf("shadowedLookFiles(\"wet\") = %v, want both the working-directory and library copies", got)
	}
	if got[0] != "wet.json" {
		t.Errorf("first shadowed file = %q, want the working-directory copy first", got[0])
	}

	// A name that is not a built-in shadows nothing, however many files exist.
	writeLookFile(t, "mylook.json", tweaked(t, "classic", 1.0))
	if got := shadowedLookFiles("mylook"); len(got) != 0 {
		t.Errorf("shadowedLookFiles(\"mylook\") = %v, want none — nothing shadows a non-built-in name", got)
	}
}

// TestPreviewLooksIncludesWorkingDirectoryLooks closes the gap between what
// -style will apply and what -preview will show. A look being iterated on lives
// in the working directory as often as in the library, and one that can be
// applied but never put on a sheet is the wrong way round.
func TestPreviewLooksIncludesWorkingDirectoryLooks(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	writeLookFile(t, "here.json", tweaked(t, "classic", 0.77))

	looks, err := previewLooks()
	if err != nil {
		t.Fatal(err)
	}
	var found *ciba.Look
	for i := range looks {
		if looks[i].Name == "here" {
			found = &looks[i]
		}
	}
	if found == nil {
		t.Fatal("a look in the working directory was left off the sheet")
	}
	if found.Clarity != 0.77 {
		t.Errorf("clarity = %.2f, want 0.77", found.Clarity)
	}
}

// TestPreviewLooksPrefersWorkingDirectoryOverLibrary keeps the sheet consistent
// with what -style would actually apply for the same name.
func TestPreviewLooksPrefersWorkingDirectoryOverLibrary(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "looks")
	t.Setenv(looksDirEnv, dir)

	writeLookFile(t, "both.json", tweaked(t, "classic", 1.10))
	writeLookFile(t, filepath.Join(dir, "both.json"), tweaked(t, "classic", 0.20))

	looks, err := previewLooks()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	var clarity float64
	for _, l := range looks {
		if l.Name == "both" {
			n++
			clarity = l.Clarity
		}
	}
	if n != 1 {
		t.Fatalf("%q appears %d times on the sheet, want once", "both", n)
	}
	if clarity != 1.10 {
		t.Errorf("clarity = %.2f, want the working directory's 1.10 — the sheet disagrees with what -style would apply", clarity)
	}
}

// TestPreviewLooksIgnoresCompoundExtensions keeps the sheet out of trouble in a
// real photo directory, which holds JSON that has nothing to do with looks —
// a companion tool writes <base>.recipe.json beside the images it renders. A
// look file is <name>.json where <name> carries no further extension.
func TestPreviewLooksIgnoresCompoundExtensions(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	// Exactly what a companion tool leaves behind, and not a look at all.
	if err := os.WriteFile("square.recipe.json", []byte(`{"styles":[{"name":"deep-red"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// And a file that WOULD load cleanly if the extension rule let it through.
	// This is the fixture that isolates the rule: the recipe file above is
	// rejected by validation whether or not the rule exists, so on its own it
	// tests nothing about the rule. A first version of this test used only that
	// file and passed with the rule deleted.
	writeLookFile(t, "valid.compound.json", tweaked(t, "classic", 0.99))
	writeLookFile(t, "real.json", tweaked(t, "classic", 0.31))

	looks, err := previewLooks()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range looks {
		if strings.Contains(l.Name, "recipe") || strings.Contains(l.Name, "compound") {
			t.Errorf("%q has a compound extension and was treated as a look", l.Name+lookExt)
		}
	}
	if len(looks) != len(ciba.Builtins())+1 {
		t.Errorf("got %d looks, want %d built-ins plus real.json only",
			len(looks), len(ciba.Builtins())+1)
	}
}

// TestPreviewLooksSkipsWorkingDirectoryFilesShadowedByBuiltins mirrors the
// library behaviour: a file a built-in hides is not silently shown under a name
// that would resolve to something else.
func TestPreviewLooksSkipsWorkingDirectoryFilesShadowedByBuiltins(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	writeLookFile(t, "classic.json", tweaked(t, "classic", 1.9))

	looks, err := previewLooks()
	if err != nil {
		t.Fatal(err)
	}
	if len(looks) != len(ciba.Builtins()) {
		t.Fatalf("got %d looks, want just the %d built-ins", len(looks), len(ciba.Builtins()))
	}
	builtin, _ := ciba.Lookup("classic")
	for _, l := range looks {
		if l.Name == "classic" && l.Clarity != builtin.Clarity {
			t.Errorf("the sheet's classic tile has clarity %.2f, the shadowed file's, not the built-in's %.2f",
				l.Clarity, builtin.Clarity)
		}
	}
}
