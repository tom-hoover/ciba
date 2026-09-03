package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tom-hoover/ciba/internal/ciba"
)

// looksDirEnv names the environment variable that relocates the personal look
// library. It exists mainly so tests can point the library at a temporary
// directory without touching the real one, but it is documented for anyone who
// keeps their looks under version control somewhere else.
const looksDirEnv = "CIBA_LOOKS_DIR"

// lookExt is the extension a look file carries. JSON rather than an ini-style
// format because ciba.Look already round-trips through encoding/json — the
// tags, the validation and the round-trip test all exist — so reading a look
// from disk needs no parser of its own. Three per-channel parameters are
// arrays, which a flat key/value format renders awkwardly.
const lookExt = ".json"

// looksDir returns the directory holding the user's personal look library.
//
// A home directory that cannot be determined yields an empty path rather than
// an error: the library is optional, and failing to resolve it must not stop a
// built-in look from rendering.
func looksDir() string {
	if dir := os.Getenv(looksDirEnv); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ciba", "looks")
}

// lookPaths returns the files consulted for a look name, in order.
//
// The working directory comes first so a look committed beside a shoot travels
// with it and wins over a same-named entry in the personal library.
func lookPaths(name string) []string {
	paths := []string{name + lookExt}
	if dir := looksDir(); dir != "" {
		paths = append(paths, filepath.Join(dir, name+lookExt))
	}
	return paths
}

// readLookFile loads and validates one look file, naming the file in every
// error it returns.
//
// The name is taken from the filename the caller asked for, not from the file's
// own name field. Those can disagree, and the one the user typed is the one
// that has to appear in messages and on contact-sheet labels — a look labelled
// with something -style cannot resolve is worse than useless.
func readLookFile(path, name string) (ciba.Look, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ciba.Look{}, err
	}
	var l ciba.Look
	if err := json.Unmarshal(b, &l); err != nil {
		return ciba.Look{}, fmt.Errorf("%s: %w", path, err)
	}
	l.Name = name
	if err := l.Validate(); err != nil {
		return ciba.Look{}, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// shadowedLookFiles returns the look files that a built-in of the same name
// prevents from ever being read, in the order they would have been consulted.
//
// Built-ins winning is deliberate, but silence about it is not: naming a file
// after a built-in and then editing it produces no change at all, with nothing
// to indicate why. That is the single most likely way to lose an afternoon here,
// so it gets said out loud.
func shadowedLookFiles(name string) []string {
	if _, ok := ciba.Lookup(name); !ok {
		return nil
	}
	var shadowed []string
	for _, path := range lookPaths(name) {
		if _, err := os.Stat(path); err == nil {
			shadowed = append(shadowed, path)
		}
	}
	return shadowed
}

// resolveLook finds a look by name, checking the built-ins first and files
// second. The second return value is the file the look came from, or "" when it
// came from the built-ins.
//
// Callers report that origin so a run makes visible which definition it used.
// Without it the output is identical whether a hand-edited file was loaded or
// silently ignored, which leaves wrecking the values as the only way to find out.
//
// Built-ins win deliberately: a stray classic.json in a photo directory must
// never silently redefine what the shipped look means.
func resolveLook(name string) (ciba.Look, string, error) {
	if l, ok := ciba.Lookup(name); ok {
		for _, path := range shadowedLookFiles(name) {
			warnf("%s is never read: %q is a built-in look and built-ins win; rename the file to use it", path, name)
		}
		return l, "", nil
	}
	for _, path := range lookPaths(name) {
		l, err := readLookFile(path, name)
		if err == nil {
			return l, path, nil
		}
		// A missing file simply means the look is not here; keep looking. Any
		// other failure — unparseable JSON, a value out of range, a permission
		// problem — is reported as itself. Collapsing those into "unknown look"
		// would send the reader to -list, which cannot help with a stray comma.
		if !os.IsNotExist(err) {
			return ciba.Look{}, "", err
		}
	}
	return ciba.Look{}, "", fmt.Errorf("unknown look %q: not a built-in (run with -list to see them), and no %s%s in the working directory or %s",
		name, name, lookExt, looksDirDescription())
}

// looksDirDescription names the library in user-facing errors, falling back to
// a description when no home directory could be resolved.
func looksDirDescription() string {
	if dir := looksDir(); dir != "" {
		return dir
	}
	return "the look library (no home directory found)"
}

// overridable maps a command-line flag name to the Look field it sets.
//
// Only the scalar parameters appear here. Curve, Pivot, Dmin and Dmax are
// per-channel arrays, and Dmin and Dmax are coupled: because the multiplier is
// (Dmax[c] - Dmin[c]), lowering one channel's Dmax without raising its Dmin
// lifts that channel at every tone rather than only in shadow, which reads as a
// white-balance error rather than the intended hue crossover. Editing those as a
// set in a file is safe in a way that turning one of them on a command line is
// not.
//
// One table drives flag registration, help text, override application and the
// test that pins which parameters are exposed, so those four cannot drift apart.
type override struct {
	set  func(*ciba.Look, float64)
	help string
}

var overridable = map[string]override{
	"scale": {func(l *ciba.Look, v float64) { l.Scale = v },
		"override the look's exposure-window width in log10 units (smaller is steeper)"},
	"exposure": {func(l *ciba.Look, v float64) { l.Exposure = v },
		"override the look's exposure offset in log10 units (higher clips more highlights)"},
	"clarity": {func(l *ciba.Look, v float64) { l.Clarity = v },
		"override the look's local-contrast amount (0-2)"},
	"radius": {func(l *ciba.Look, v float64) { l.Radius = v },
		"override the look's clarity radius as a fraction of the short edge"},
	"bloom": {func(l *ciba.Look, v float64) { l.Bloom = v },
		"override the look's highlight bloom amount (0-1)"},
	"bloom-radius": {func(l *ciba.Look, v float64) { l.BloomRadius = v },
		"override the look's bloom radius as a fraction of the short edge"},
	"bloom-thresh": {func(l *ciba.Look, v float64) { l.BloomThresh = v },
		"override the luminance above which highlights bloom (0-1)"},
}

// overridableNames returns the flag names in a stable order, for help text and
// for anything that prints them.
func overridableNames() []string {
	names := make([]string, 0, len(overridable))
	for name := range overridable {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// applyOverrides returns l with the named parameters replaced.
//
// set holds only the flags the user actually typed. That distinction is the
// whole point: seven float flags default to zero, so applying them
// unconditionally would silently flatten every parameter left unmentioned.
// main builds this map from flag.Visit, which reports set flags only.
func applyOverrides(l ciba.Look, set map[string]float64) ciba.Look {
	for name, v := range set {
		if o, ok := overridable[name]; ok {
			o.set(&l, v)
		}
	}
	return l
}

// setFlagNames returns the flag names present in an override set, sorted and
// dash-prefixed, for a message that has to name what the user typed.
func setFlagNames(set map[string]float64) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, "-"+name)
	}
	sort.Strings(names)
	return names
}

// dumpLook writes a look as indented JSON, the form readLookFile accepts, so
// its output is a usable starting point for a custom look rather than a report
// about one.
func dumpLook(w io.Writer, l ciba.Look) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// previewDirs returns the directories scanned for custom looks, in the order
// -style would consult them, so a sheet cannot disagree with what applying a
// name would actually do.
func previewDirs() []string {
	dirs := []string{"."}
	if dir := looksDir(); dir != "" {
		dirs = append(dirs, dir)
	}
	return dirs
}

// isLookFile reports whether a directory entry names a look.
//
// A look file is <name>.json where <name> carries no further extension. That
// rule exists because the working directory is a photo directory holding JSON
// that has nothing to do with looks — a companion tool writes
// <base>.recipe.json beside the images it renders — and treating those as
// looks would fill the sheet with warnings about files nobody meant as looks.
func isLookFile(name string) bool {
	if !strings.EqualFold(filepath.Ext(name), lookExt) {
		return false
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return base != "" && !strings.Contains(base, ".")
}

// looksFromDir returns the readable looks in dir, sorted by name, skipping any
// name already in seen and marking the ones it returns.
//
// A file that will not load is skipped with a warning rather than being fatal:
// one stray comma must not stop the remaining looks being compared, which is the
// sheet's only job.
func looksFromDir(dir string, seen map[string]bool) []ciba.Look {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A directory that is not there yet is the common case, not a problem.
		if !os.IsNotExist(err) {
			warnf("%v; leaving its looks off the sheet", err)
		}
		return nil
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !isLookFile(e.Name()) {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
	}
	sort.Strings(names)

	var looks []ciba.Look
	for _, name := range names {
		if seen[name] {
			// Either a built-in of this name wins, or an earlier directory
			// already supplied it. Both are resolution order working as
			// intended; showing the file anyway would put a tile on the sheet
			// that -style would never produce.
			if _, builtin := ciba.Lookup(name); builtin {
				warnf("%s is never read: %q is a built-in look and built-ins win; rename the file to use it",
					filepath.Join(dir, name+lookExt), name)
			}
			continue
		}
		l, err := readLookFile(filepath.Join(dir, name+lookExt), name)
		if err != nil {
			warnf("%v; leaving it off the sheet", err)
			continue
		}
		seen[name] = true
		looks = append(looks, l)
	}
	return looks
}

// previewLooks returns the looks a contact sheet should show: every built-in,
// then every readable custom look, working directory before library.
//
// Built-ins come first and in their own order so the sheet reads the same way
// every time whatever else is present. Custom looks follow in resolution order,
// so a name appears once and shows what -style would actually apply for it.
func previewLooks() ([]ciba.Look, error) {
	looks := ciba.Builtins()
	seen := make(map[string]bool, len(looks))
	for _, l := range looks {
		seen[l.Name] = true
	}
	for _, dir := range previewDirs() {
		looks = append(looks, looksFromDir(dir, seen)...)
	}
	return looks, nil
}
