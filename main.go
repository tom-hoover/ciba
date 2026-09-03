// Command ciba renders colour photographs with the look of an early-1980s
// Cibachrome print: dense glossy blacks, a short tonal scale that clips hard
// at both ends, saturated hue-shifted colour, high acutance, and no grain.
package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tom-hoover/ciba/internal/ciba"
	"github.com/tom-hoover/darkroom/jobplan"
)

// cmd names this tool's output conventions for the shared job planner.
var cmd = jobplan.Command{Name: "ciba", Suffix: "-ciba"}

func main() {
	preview := flag.Bool("preview", false, "write a labelled contact sheet instead of rendering")
	lookName := flag.String("style", ciba.DefaultLook, "look to apply; see -list")
	px := flag.Int("px", 256, "contact sheet tile size along the short edge")
	outDir := flag.String("out", "", "write results to this directory instead of beside the originals")
	quality := flag.Int("q", 95, "JPEG quality (1-100)")
	workers := flag.Int("j", runtime.NumCPU(), "number of files to render in parallel")
	recursive := flag.Bool("r", false, "descend into subdirectories")
	force := flag.Bool("f", false, "overwrite existing output")
	dryRun := flag.Bool("n", false, "list what would be rendered without writing anything")
	verbose := flag.Bool("v", false, "report each file as it is rendered")
	list := flag.Bool("list", false, "print the available looks and exit")
	dump := flag.Bool("dump", false, "print the resolved look as JSON and exit; redirect it into a file to start a custom look")

	// The parameter overrides all default to zero and are applied only when
	// actually typed, which flag.Visit below is what establishes. Registering
	// them from the same table that applies them keeps the two in step.
	overrides := make(map[string]*float64, len(overridable))
	for _, name := range overridableNames() {
		overrides[name] = flag.Float64(name, 0, overridable[name].help)
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ciba renders photographs with an early-1980s wet-Cibachrome look.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <file|directory>\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "Originals are never modified.\n\n")
		fmt.Fprintf(os.Stderr, "A -style name resolves against the built-in looks first, then <name>.json in\n")
		fmt.Fprintf(os.Stderr, "the working directory, then %s.\n", looksDirDescription())
		fmt.Fprintf(os.Stderr, "Per-channel parameters (curve, pivot, dmin, dmax) are file-only: dmin and dmax\n")
		fmt.Fprintf(os.Stderr, "are coupled and have to move together.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Only the overrides the user actually typed. Applying the rest would set
	// every unmentioned parameter to a flag default of zero.
	set := map[string]float64{}
	flag.Visit(func(f *flag.Flag) {
		if p, ok := overrides[f.Name]; ok {
			set[f.Name] = *p
		}
	})

	if *list {
		looks, err := previewLooks()
		if err != nil {
			fatalf("%v", err)
		}
		// Width from the actual names rather than a constant, so adding a look
		// with a longer name than the rest cannot silently break the columns.
		width := 0
		for _, l := range looks {
			if n := len(l.Name); n > width {
				width = n
			}
		}
		for _, l := range looks {
			desc := l.Desc
			if _, builtin := ciba.Lookup(l.Name); !builtin {
				// Ask resolveLook where the look comes from rather than
				// assuming the library: previewLooks scans the working
				// directory first, so reconstructing a path here reported a
				// file that need not exist, in the one output whose job is to
				// say which definition -style will use.
				if _, origin, err := resolveLook(l.Name); err == nil && origin != "" {
					desc = "custom look from " + origin
				} else {
					desc = "custom look"
				}
			}
			fmt.Printf("  %-*s  %s\n", width, l.Name, desc)
		}
		return
	}

	// -dump answers a question about a look rather than doing anything to an
	// image, so it runs before the target argument is required.
	if *dump {
		look, _, err := resolveLook(*lookName)
		if err != nil {
			fatalf("%v", err)
		}
		look = applyOverrides(look, set)
		if err := look.Validate(); err != nil {
			fatalf("%v", err)
		}
		if err := dumpLook(os.Stdout, look); err != nil {
			fatalf("%v", err)
		}
		return
	}

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if *quality < 1 || *quality > 100 {
		fatalf("quality %d out of range (1-100)", *quality)
	}
	if *workers < 1 {
		fatalf("worker count must be at least 1")
	}
	if *px < 32 {
		fatalf("tile size %d is too small to judge", *px)
	}

	target := flag.Arg(0)
	info, err := os.Stat(target)
	if err != nil {
		fatalf("%v", err)
	}

	// Preview renders every available look and ignores -style entirely, so
	// -style is never resolved on this path.
	if *preview {
		if info.IsDir() {
			fatalf("-preview needs a single image, not a directory")
		}
		// A parameter override has no meaning across a whole sheet, and
		// silently dropping a flag the user typed is worse than refusing it.
		if len(set) > 0 {
			fatalf("-preview compares looks as they are defined, so %s cannot apply to it; "+
				"use -dump to save a tweaked look, then -preview will include it",
				strings.Join(setFlagNames(set), ", "))
		}
		if err := runPreview(target, *outDir, *px); err != nil {
			fatalf("%v", err)
		}
		return
	}

	look, origin, err := resolveLook(*lookName)
	if err != nil {
		fatalf("%v", err)
	}
	// Say where a look came from when it came from a file. A built-in is
	// unsurprising and saying so on every run would be noise, but a file being
	// loaded — or quietly not being loaded — is the thing there is otherwise no
	// way to check short of wrecking its values to see if anything moves.
	if origin != "" {
		fmt.Printf("using %s from %s\n", look.Name, origin)
	}
	look = applyOverrides(look, set)
	if err := look.Validate(); err != nil {
		fatalf("%v", err)
	}

	var jobs []jobplan.Job
	var dupes []string
	if info.IsDir() {
		jobs, dupes, err = cmd.Scan(target, *outDir, *recursive)
		if err != nil {
			fatalf("%v", err)
		}
	} else {
		j, err := cmd.ScanOne(target, *outDir)
		if errors.Is(err, os.ErrInvalid) {
			fatalf("%s: unsupported image type", target)
		} else if err != nil {
			fatalf("%v", err)
		}
		jobs = []jobplan.Job{j}
	}
	if len(jobs) == 0 {
		fmt.Printf("no images found in %s\n", target)
		return
	}
	for _, d := range dupes {
		if *verbose || *dryRun {
			fmt.Printf("skipping %s (another file shares its name)\n", d)
		}
	}

	todo, skipped := jobplan.Partition(jobs, *force)
	if *dryRun {
		for _, j := range todo {
			fmt.Printf("%s -> %s [%s]\n", j.Src, j.Dst, look.Name)
		}
		fmt.Printf("%d to render, %d skipped, %d duplicate\n", len(todo), skipped, len(dupes))
		if notice := skipNotice(skipped, look.Name); notice != "" {
			fmt.Println(notice)
		}
		return
	}

	failed := cmd.RenderAll(todo, func(img image.Image) image.Image { return ciba.Render(img, look) },
		*quality, *workers, *verbose)
	fmt.Printf("%d rendered, %d skipped, %d duplicate, %d failed\n",
		len(todo)-failed, skipped, len(dupes), failed)
	if notice := skipNotice(skipped, look.Name); notice != "" {
		fmt.Println(notice)
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// skipNotice returns the line that follows a summary when existing output was
// skipped, or the empty string when nothing was.
//
// Skipping files whose output already exists is deliberate — it makes re-running
// over a directory after adding photos cost nothing — but it hides the worst
// mistake this command invites: edit a look, re-run, and every file rendered
// earlier keeps its old appearance because nothing was written. The bare counts
// in the summary are technically complete and easy to read past, especially when
// some files do render and the mixture looks like the look half-applied.
//
// So the notice names the look that was not applied and the flag that would
// apply it. Neither is recoverable from the counts, and both are what the reader
// needs next.
func skipNotice(skipped int, lookName string) string {
	if skipped == 0 {
		return ""
	}
	files := "files"
	if skipped == 1 {
		files = "file"
	}
	return fmt.Sprintf("%d %s already had output and kept it; pass -f to re-render them with %s",
		skipped, files, lookName)
}

// warnf reports a non-fatal condition to stderr.
//
// It lived in apply.go beside renderFile and the worker pool until those moved
// to darkroom/jobplan, which now warns for itself. The callers left in this
// command are the four in lookfile.go, so it sits here beside fatalf.
func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ciba: warning: "+format+"\n", args...)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ciba: "+format+"\n", args...)
	os.Exit(2)
}
