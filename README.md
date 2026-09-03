# ciba

Renders colour photographs with the look of an early-1980s Cibachrome
(later Ilfochrome Classic) print: dense glossy blacks, a short tonal scale
that clips hard at both ends, intense hue-shifted saturation, high
acutance, and no grain.

Originals are never modified. Every render — full resolution or preview
tile — goes through the same pipeline, so what you see in a preview
predicts what applying that look produces.

Depends on [`github.com/tom-hoover/darkroom`](https://github.com/tom-hoover/darkroom)
for decoding, contact-sheet layout, and the shared job-planning/rendering
plumbing.

See [`docs/design.md`](docs/design.md) for the reasoning behind the presets
— each trait of the look is derived from a specific property of the
dye-bleach printing process, not matched against a reference print.

## Build

Requires Go 1.27+ and a C compiler (cgo builds the bundled HEIC decoder).

```sh
go build .
```

## Two modes

**Preview** (`-preview`) takes a single image and writes a labelled contact
sheet: one thumbnail tile per look, named for the exact string to pass to
`-style`.

**Apply** (the default, no `-preview`) takes a file or a directory and
renders it with one named look, writing full-resolution output.

```sh
ciba -preview HEIC/square.jpg          # square-contact.png
ciba -style classic -out out/ HEIC/    # apply a built-in look to a folder
```

## Flags

```
ciba [flags] <file|directory>

  -preview      write a labelled contact sheet instead of rendering
                (single file only; a directory is an error;
                 renders every look, so -style is ignored, not checked)
  -style NAME   look to apply                              (default "classic")
  -px N         contact sheet tile short edge                    (default 256)
  -out D        write results to D instead of beside the originals
  -q N          JPEG quality (1-100)                              (default 95)
  -j N          files to render in parallel                  (default: all CPUs)
  -r            descend into subdirectories
  -f            overwrite existing output               (default: skip)
  -n            list what would be rendered, writing nothing
  -v            report each file as it is rendered
  -list         print available looks and exit
  -dump         print the resolved look as JSON and exit; redirect it into
                a file to start a custom look

Parameter overrides. Each replaces one value in the resolved look, and only
the ones you actually type are applied:

  -scale F         exposure-window width, log10 units (smaller is steeper)
  -exposure F      exposure offset, log10 units (higher clips more highlights)
  -clarity F       local-contrast amount (0-2)
  -radius F        clarity radius, fraction of the short edge
  -bloom F         highlight bloom amount (0-1)
  -bloom-radius F  bloom radius, fraction of the short edge
  -bloom-thresh F  luminance above which highlights bloom (0-1)
```

The per-channel parameters — `curve`, `pivot`, `dmin` and `dmax` — are not
exposed as flags; they are edited as a set, in a look file (see below).

## Looks

Six built-in looks, spanning the space so the contact sheet is worth
reading:

| Name | Description |
|---|---|
| `classic` | the reference Cibachrome: dense blacks, warm paper white |
| `wet` | maximum gloss: highest D-max, hardest acutance, specular sheen |
| `wetportrait` | wet's ends, flatter middle: less lurid skin |
| `deep` | shortest scale: hardest clip at both ends |
| `azo` | crossover exaggerated: hot reds, yellow-green greens, navy skies |
| `flat` | straight-line control: shows what the curve contributes |

Run `ciba -list` to print this from the binary itself. `classic` is the
default.

## Custom looks

A `-style` name resolves in three places, in order: the built-ins above,
`<name>.json` in the working directory, then `<name>.json` in
`~/.ciba/looks/` (override with `CIBA_LOOKS_DIR`). Start from a built-in
and `-dump` it to a file rather than writing one from scratch:

```sh
mkdir -p ~/.ciba/looks
ciba -style wet -clarity 0.40 -dump > ~/.ciba/looks/softwet.json
ciba -style softwet HEIC/               # apply it like any built-in
```

## What it will not do

It never modifies a source image, and it refuses to let `-out` resolve to
the source directory (directly or through a symlink), since that would
overwrite originals with renders. Its presets are derived from the
physics of the dye-bleach process, not calibrated against a real
Cibachrome print — none were available to check against. It adds no film
grain; see `docs/design.md` for why that is deliberate rather than an
oversight.

## Tests

```sh
go test ./...
```
