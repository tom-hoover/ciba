package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tom-hoover/ciba/internal/ciba"
	"github.com/tom-hoover/darkroom/imaging"
)

// main() reads flags and exits, so the only honest way to test its behaviour
// is to run it. TestMain re-executes this test binary with CIBA_TEST_MAIN
// set and calls main() directly, which needs no toolchain on PATH and no
// separate build step.
const (
	mainEnv     = "CIBA_TEST_MAIN"
	mainArgsEnv = "CIBA_TEST_ARGS"
	argSep      = "\x1f"
)

// writeJPEGWithExif writes a JPEG to path carrying a real EXIF block — a
// minimal JPEG with genuine APP1/EXIF segments, not just JFIF ones — so this
// package can build a fixture that exercises the same metadata path
// jobplan.Command.RenderFile actually uses, without depending on
// darkroom/imaging's unexported test helpers.
//
// It lived in apply_test.go until the job planner moved to darkroom/jobplan;
// main_test.go and preview_test.go both build their fixtures with it, so it
// stays in this package.
func writeJPEGWithExif(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 40, G: 90, B: 190, A: 255})
		}
	}
	var body bytes.Buffer
	if err := jpeg.Encode(&body, img, nil); err != nil {
		t.Fatal(err)
	}
	b := body.Bytes()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xD8 {
		t.Fatal("encoder did not emit an SOI marker")
	}

	// A minimal but real EXIF block: the "Exif\0\0" identifier a JPEG APP1
	// segment carries, followed by a valid little-endian TIFF header holding
	// one Orientation tag.
	tiff := []byte{
		'I', 'I', 0x2A, 0x00, // little-endian byte order, TIFF magic 42
		0x08, 0x00, 0x00, 0x00, // IFD0 starts at offset 8
		0x01, 0x00, // one directory entry
		0x12, 0x01, // tag 0x0112, Orientation
		0x03, 0x00, // type SHORT
		0x01, 0x00, 0x00, 0x00, // count 1
		0x01, 0x00, 0x00, 0x00, // value 1 (normal)
		0x00, 0x00, 0x00, 0x00, // no next IFD
	}
	exif := append([]byte("Exif\x00\x00"), tiff...)

	seg := make([]byte, 4, 4+len(exif))
	seg[0], seg[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(seg[2:4], uint16(len(exif)+2))
	seg = append(seg, exif...)

	out := append([]byte{0xFF, 0xD8}, seg...)
	out = append(out, b[2:]...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv(mainEnv) == "1" {
		args := []string{"ciba"}
		if v := os.Getenv(mainArgsEnv); v != "" {
			args = append(args, strings.Split(v, argSep)...)
		}
		os.Args = args
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMain runs ciba's main in a child process and returns its exit code and
// combined output.
func runMain(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		mainEnv+"=1",
		mainArgsEnv+"="+strings.Join(args, argSep),
	)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running main: %v (output %s)", err, out)
	}
	return code, string(out)
}

// TestBinaryRendersADirectory exercises the whole command, which no unit test
// covers: flag parsing, scan, worker pool, atomic write, and the summary line.
func TestBinaryRendersADirectory(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.jpg", "b.jpg"} {
		writeJPEGWithExif(t, filepath.Join(dir, n))
	}
	code, out := runMain(t, "-style", "wet", "-j", "2", dir)
	if code != 0 {
		t.Fatalf("run failed: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "2 rendered") {
		t.Errorf("summary did not report 2 rendered:\n%s", out)
	}
	for _, n := range []string{"a" + cmd.Suffix + ".jpg", "b" + cmd.Suffix + ".jpg"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("expected output %s: %v", n, err)
		}
	}

	// The output existing is not the same as the look having been applied.
	// main.go hands jobplan a closure over ciba.Render, and that closure is
	// the only place the two are wired together: replace it with
	// func(img image.Image) image.Image { return img } and every other
	// assertion here still passes while ciba writes untransformed copies of
	// every photograph. So decode what the binary actually wrote and compare
	// it against ciba.Render of the same source, computed independently.
	src := filepath.Join(dir, "a.jpg")
	decoded, _, err := imaging.Decode(src)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := ciba.Lookup("wet")
	if !ok {
		t.Fatal(`ciba.Lookup("wet") found no such look`)
	}
	wr, wg, wb, _ := ciba.Render(decoded, l).At(8, 6).RGBA()

	f, err := os.Open(filepath.Join(dir, "a"+cmd.Suffix+".jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("the rendered output is not a valid JPEG: %v", err)
	}
	r, g, b, _ := img.At(8, 6).RGBA()
	// Same tolerance and reasoning as render_test.go: a q95 round trip on a
	// uniform block moves a channel by a level or two, an order of magnitude
	// below the swing "wet" applies to this fixture.
	const tol = 3
	for _, c := range []struct {
		name      string
		got, want uint32
	}{{"R", r >> 8, wr >> 8}, {"G", g >> 8, wg >> 8}, {"B", b >> 8, wb >> 8}} {
		if absDiff8(c.got, c.want) > tol {
			t.Errorf("%s = %d, want %d ± %d — the binary did not apply %s (independently computed via ciba.Render)",
				c.name, c.got, c.want, tol, l.Name)
		}
	}

	// A second run must skip both rather than re-render or feed on its output.
	code, out = runMain(t, "-style", "wet", dir)
	if code != 0 {
		t.Fatalf("second run failed: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "0 rendered") || !strings.Contains(out, "2 skipped") {
		t.Errorf("second run should have skipped both:\n%s", out)
	}
}

func TestBinaryRejectsAnUnknownLook(t *testing.T) {
	dir := t.TempDir()
	writeJPEGWithExif(t, filepath.Join(dir, "a.jpg"))
	code, out := runMain(t, "-style", "cibachrome", dir)
	if code == 0 {
		t.Fatal("an unknown look was accepted")
	}
	if !strings.Contains(out, "-list") {
		t.Errorf("the error should point at -list:\n%s", out)
	}
}

// TestBinaryDumpsAResolvedLookWithoutATarget pins that -dump answers a question
// about a look rather than acting on an image, so it must not demand a file
// argument the way every rendering path does.
func TestBinaryDumpsAResolvedLookWithoutATarget(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	code, out := runMain(t, "-style", "classic", "-dump")
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	var got ciba.Look
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("dump output is not a look: %v\n%s", err, out)
	}
	want, _ := ciba.Lookup("classic")
	if got != want {
		t.Errorf("dumped look does not match the built-in:\n got %+v\nwant %+v", got, want)
	}
}

// TestBinaryDumpCapturesAnOverride is what makes -dump a way to promote a tweak
// you liked into a saved look, rather than only a way to read a preset out.
func TestBinaryDumpCapturesAnOverride(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	code, out := runMain(t, "-style", "wet", "-clarity", "0.40", "-dump")
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	var got ciba.Look
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("dump output is not a look: %v\n%s", err, out)
	}
	if got.Clarity != 0.40 {
		t.Errorf("dumped clarity = %.2f, want the overridden 0.40", got.Clarity)
	}
	base, _ := ciba.Lookup("wet")
	if got.Bloom != base.Bloom {
		t.Errorf("dumped bloom = %.2f, want wet's untouched %.2f — an unset flag reached the look",
			got.Bloom, base.Bloom)
	}
}

// TestBinaryRefusesAnOverrideWithPreview pins that a typed flag is never
// silently dropped. A parameter override has no meaning applied across a whole
// sheet of looks, so the command says so instead of ignoring it.
func TestBinaryRefusesAnOverrideWithPreview(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeJPEGWithExif(t, src)

	code, out := runMain(t, "-preview", "-clarity", "0.40", src)
	if code == 0 {
		t.Fatalf("-preview with an override was accepted:\n%s", out)
	}
	if !strings.Contains(out, "-clarity") {
		t.Errorf("the error does not name the flag that was ignored:\n%s", out)
	}
	if !strings.Contains(out, "-dump") {
		t.Errorf("the error should point at -dump as the way to do this:\n%s", out)
	}
}

// TestBinaryAppliesALookFromAFile covers the end-to-end wiring. The look is
// deliberately named something that is not a built-in, so a silent fallback to
// a preset would fail with "unknown look" rather than quietly rendering the
// wrong thing.
func TestBinaryAppliesALookFromAFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	base, _ := ciba.Lookup("classic")
	base.Clarity = 1.20
	writeLookFile(t, "mylook.json", base)
	writeJPEGWithExif(t, filepath.Join(dir, "photo.jpg"))

	code, out := runMain(t, "-style", "mylook", "photo.jpg")
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "1 rendered") {
		t.Errorf("summary did not report one render:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "photo"+cmd.Suffix+".jpg")); err != nil {
		t.Errorf("expected output: %v", err)
	}
}

// TestBinaryListIncludesACustomLook keeps -list honest about what -style will
// accept. Listing only the built-ins while -style resolves files too would make
// -list actively misleading.
func TestBinaryListIncludesACustomLook(t *testing.T) {
	looks := filepath.Join(t.TempDir(), "looks")
	t.Setenv(looksDirEnv, looks)
	base, _ := ciba.Lookup("classic")
	writeLookFile(t, filepath.Join(looks, "mine.json"), base)

	code, out := runMain(t, "-list")
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "mine") {
		t.Errorf("-list omitted the custom look:\n%s", out)
	}
	if !strings.Contains(out, "classic") {
		t.Errorf("-list omitted the built-ins:\n%s", out)
	}
}

// TestBinaryRejectsAnOutOfRangeOverride pins that a flag value goes through the
// same Validate a file does, and fails before any pixel is written.
func TestBinaryRejectsAnOutOfRangeOverride(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	writeJPEGWithExif(t, src)

	code, out := runMain(t, "-style", "classic", "-clarity", "9", src)
	if code == 0 {
		t.Fatalf("clarity 9 was accepted:\n%s", out)
	}
	if !strings.Contains(out, "clarity") {
		t.Errorf("the error does not name the offending parameter:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "photo"+cmd.Suffix+".jpg")); err == nil {
		t.Error("an output file was written despite the invalid parameter")
	}
}

// TestSkipNoticeNamesTheLookAndTheFlag covers the line that follows a summary
// when output was skipped.
//
// The bare counts in the summary are technically complete and easy to miss, and
// the failure mode they hide is the worst one this command has: you edit a look,
// re-run, and the files you had already rendered keep their old appearance. The
// notice has to name both the look that did NOT get applied and the flag that
// would apply it, because those are the two things the reader needs and neither
// is in the counts.
func TestSkipNoticeNamesTheLookAndTheFlag(t *testing.T) {
	got := skipNotice(2, "mywet")
	for _, want := range []string{"2 files", "-f", "mywet"} {
		if !strings.Contains(got, want) {
			t.Errorf("skipNotice(2, \"mywet\") = %q, missing %q", got, want)
		}
	}

	if got := skipNotice(1, "mywet"); !strings.Contains(got, "1 file already") {
		t.Errorf("skipNotice(1, ...) = %q, want a singular \"1 file already\"", got)
	}

	// Nothing skipped means nothing to say. A notice on every run would train
	// the reader to skip past the one time it matters.
	if got := skipNotice(0, "mywet"); got != "" {
		t.Errorf("skipNotice(0, ...) = %q, want empty", got)
	}
}

// TestBinaryWarnsWhenASecondLookIsSkipped reproduces the way this actually goes
// wrong: render a directory, change your mind about the look, re-run, and every
// file keeps the appearance of the first look because its output already exists.
func TestBinaryWarnsWhenASecondLookIsSkipped(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))
	dir := t.TempDir()
	for _, n := range []string{"a.jpg", "b.jpg"} {
		writeJPEGWithExif(t, filepath.Join(dir, n))
	}

	if code, out := runMain(t, "-style", "wet", dir); code != 0 {
		t.Fatalf("first run failed: exit %d\n%s", code, out)
	}

	code, out := runMain(t, "-style", "classic", dir)
	if code != 0 {
		t.Fatalf("second run failed: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "-f") {
		t.Errorf("the second run did not mention -f, so nothing tells the reader how to apply the new look:\n%s", out)
	}
	if !strings.Contains(out, "classic") {
		t.Errorf("the second run did not name the look that was not applied:\n%s", out)
	}
}

// TestBinaryDoesNotWarnWhenNothingWasSkipped keeps the notice meaningful. A
// message printed on every successful run is one nobody reads.
func TestBinaryDoesNotWarnWhenNothingWasSkipped(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))
	dir := t.TempDir()
	writeJPEGWithExif(t, filepath.Join(dir, "a.jpg"))

	code, out := runMain(t, "-style", "wet", dir)
	if code != 0 {
		t.Fatalf("run failed: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "-f") {
		t.Errorf("a first run with nothing skipped still advertised -f:\n%s", out)
	}
}

// TestBinaryDoesNotWarnUnderForce closes the loop: -f is the answer the notice
// gives, so a run that already passed it must not be told to pass it.
func TestBinaryDoesNotWarnUnderForce(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))
	dir := t.TempDir()
	writeJPEGWithExif(t, filepath.Join(dir, "a.jpg"))

	if code, out := runMain(t, "-style", "wet", dir); code != 0 {
		t.Fatalf("first run failed: exit %d\n%s", code, out)
	}
	code, out := runMain(t, "-f", "-style", "classic", dir)
	if code != 0 {
		t.Fatalf("forced run failed: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "1 rendered") {
		t.Errorf("-f did not re-render:\n%s", out)
	}
	if strings.Contains(out, "pass -f") {
		t.Errorf("a run that already passed -f was told to pass -f:\n%s", out)
	}
}

// TestBinaryReportsALookFileOrigin is the line that answers "is my file
// actually being used" in one run, instead of by wrecking its values to see
// whether anything moves.
func TestBinaryReportsALookFileOrigin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	base, _ := ciba.Lookup("classic")
	base.Clarity = 1.20
	writeLookFile(t, "mylook.json", base)
	writeJPEGWithExif(t, filepath.Join(dir, "photo.jpg"))

	code, out := runMain(t, "-style", "mylook", "photo.jpg")
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "mylook.json") {
		t.Errorf("the run did not say which file the look came from:\n%s", out)
	}
}

// TestBinaryDoesNotReportOriginForABuiltin keeps that line meaningful. Printing
// a provenance note on every ordinary run is noise, and noise is what gets
// skipped on the one run that matters.
func TestBinaryDoesNotReportOriginForABuiltin(t *testing.T) {
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))
	dir := t.TempDir()
	writeJPEGWithExif(t, filepath.Join(dir, "photo.jpg"))

	code, out := runMain(t, "-style", "classic", filepath.Join(dir, "photo.jpg"))
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if strings.Contains(out, "using classic from") {
		t.Errorf("a built-in reported a file origin:\n%s", out)
	}
}

// TestBinaryWarnsAboutAShadowedLookFile covers the trap most likely to produce
// "I edited a look and nothing changed": name the file after a built-in, and it
// is never read at all.
func TestBinaryWarnsAboutAShadowedLookFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(looksDirEnv, filepath.Join(t.TempDir(), "looks"))

	base, _ := ciba.Lookup("wet")
	base.Clarity = 1.90
	writeLookFile(t, "wet.json", base)
	writeJPEGWithExif(t, filepath.Join(dir, "photo.jpg"))

	code, out := runMain(t, "-style", "wet", "photo.jpg")
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "wet.json") {
		t.Errorf("nothing said the shadowed file exists:\n%s", out)
	}
	if !strings.Contains(out, "never read") {
		t.Errorf("nothing said the file is never read:\n%s", out)
	}
}

// TestBinaryListReportsWhereACustomLookActuallyCameFrom pins that -list does
// not fabricate a provenance path.
//
// It used to print the library path for every custom look regardless of where
// the look was found, so a look resolved from the working directory was listed
// against a file that need not exist at all. That is worse than printing
// nothing: it is the -list output that is supposed to answer "which definition
// will -style use", and it answered with a fiction.
func TestBinaryListReportsWhereACustomLookActuallyCameFrom(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// A library that exists but does NOT hold this look.
	looks := filepath.Join(t.TempDir(), "looks")
	if err := os.MkdirAll(looks, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(looksDirEnv, looks)

	base, _ := ciba.Lookup("classic")
	writeLookFile(t, "here.json", base)

	code, out := runMain(t, "-list")
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "here.json") {
		t.Errorf("-list did not name the file the look came from:\n%s", out)
	}
	if strings.Contains(out, filepath.Join(looks, "here.json")) {
		t.Errorf("-list claimed the look came from the library, where it does not exist:\n%s", out)
	}
}
