# ciba — a wet-Cibachrome look for colour photographs

Design, 2026-08-28.

> **On this document.** Written 2026-08-28 against the private `imagetools`
> monorepo, before this code was split into separate repositories. It is
> preserved as written, unedited, because it is the design record — the
> silver dye-bleach reasoning and the numerically-solved preset derivations
> below exist nowhere else. Its present tense describing `internal/...`
> packages was accurate at the time; read it against the mapping below.
> Section numbers cited from comments elsewhere in this repository (`§2`,
> `§4`, `§4.1`, `§5.1`, `§6`, `§9`, ...) refer to this document's own
> numbering.
>
> | Then | Now |
> |---|---|
> | `internal/imaging` | `github.com/tom-hoover/darkroom/imaging` — public |
> | `internal/tone` | `github.com/tom-hoover/darkroom/tone` — public |
> | `internal/sheet` | `github.com/tom-hoover/darkroom/sheet` — public |
> | `internal/jobplan` | `github.com/tom-hoover/darkroom/jobplan` — public |
> | `internal/bw` | belongs to a separate, unpublished tool — not part of this repository |
> | `tasks/lessons .md` | an internal engineering log kept with that private tool — not published here |

## 1. What this is

`ciba` is a third command beside `skyburn` and `heic2jpg`. It renders a colour
photograph the way a Cibachrome (later Ilfochrome Classic) print looked in the
early 1980s: dense glossy blacks, a short tonal scale that clips hard at both
ends, intense hue-shifted saturation, high acutance, and no grain.

It shares `internal/imaging` for decode/encode and a newly extracted
`internal/tone` for the pixel-math kernel it has in common with `internal/bw`.

## 2. What the look actually is

Cibachrome was a *silver dye-bleach* (dye-destruction) process: three azo dyes
were built into the emulsion and selectively destroyed in proportion to
exposure. Prints were made from transparencies onto glossy polyester. Each
visible trait comes from a specific property of that process, and each maps
onto exactly one parameter of the pipeline in §4:

| Trait | Cause | Parameter |
|---|---|---|
| Very high contrast, short scale | print of a positive; contrast compounds | `Scale` (exposure scale, log₁₀ units) |
| Hard clipping at both ends | the print reaches D-max over ~1.6–1.8 log units where RA-4 needs ~2.2 | `Scale` sets the window's width, `Exposure` its placement |
| Dense blacks, glossy sheen | high D-max on a clear polyester base | `Dmax[3]` |
| Deep shadows drift cyan-blue | uneven per-channel D-max | `Dmax[2]` slightly below `Dmax[0]` |
| Paper white slightly warm | no optical brighteners | `Dmin[3]` |
| Intense saturation | unusually pure azo dyes | `Scale` + `Curve[3]` magnitude |
| Hue crossover — hot reds, yellow-green greens, navy skies | per-channel curves of unequal shape | `Curve[3]` + `Pivot[3]` |
| Very high acutance | grainless emulsion on a clear base | `Clarity`, `Radius` |
| Wet-looking surface | specular gloss | `Bloom`, `BloomRadius`, `BloomThresh` |
| **No grain** | dye-bleach destroys dye, it does not clump silver | *deliberately absent* |

The last row is the one most emulations get wrong. Adding grain is the
commonest way to fake a "film look" and it is precisely backwards here.
No grain parameter exists, and a test pins its absence (§7).

The saturation increase and the hue crossover are **not** separate stages.
Overall curve steepness — a narrow `Scale` plus `Curve` magnitude — produces
the saturation increase; unequal per-channel curves specifically produce the
hue crossover on top of it. That is the whole argument for working in the
density domain rather than reaching for a saturation slider: it is the only
formulation in which "what did the process do" maps onto parameters directly,
which matters because we are deriving the presets from the process rather
than from reference scans.

## 3. Architecture

```
main.go      flags, target resolution, dispatch
cmd/ciba/scan.go      directory walk, Job pairing, src==dst guards
cmd/ciba/apply.go     decode → Render → atomic write, worker pool
preview.go   contact sheet of the built-in looks

internal/ciba/look.go      Look, Validate, Builtins, Lookup, DefaultLook
internal/ciba/pipeline.go  Render(image.Image, Look) *image.RGBA

github.com/tom-hoover/darkroom/tone     SRGBToLinear, LinearToSRGB, Clamp01,
                       Sigmoid, BlurBox3, RadiusPx
github.com/tom-hoover/darkroom/sheet    contact-sheet layout, renderer-agnostic
github.com/tom-hoover/darkroom/imaging  Decode, WriteJPEG, Thumbnail, MaxExifPayload
```

Deliberately **not** carried over from `skyburn`: the `.recipe.json` sidecar
and the Claude advisor. Both exist there to let a per-image tuned style be
applied to a whole shoot. `ciba` ships only built-in looks, so `-style <name>`
resolves against `Builtins()` directly and the 168 lines of `recipe.go` would
have nothing to resolve. Also not carried over: `Vignette` — edge darkening is
a lens and enlarger artefact, not a property of the print.

### 3.1 The two extractions

**`internal/tone` is mandatory, not a tidiness exercise.** `clarityRadiusPx`
carries the scale-invariance contract that makes a 256px preview predict a
full-resolution render, and `tasks/lessons .md` records that a re-derivation of
this line silently broke resolution independence at 4096px in a way a
whole-image difference test could not detect. A second copy in
`internal/ciba` would be a second place for that to happen. `Sigmoid` and
`BlurBox3` move with it because they are already tested and because `ciba`
needs them unchanged.

Moving these out of `internal/bw` means `internal/bw` is edited: the local
definitions are deleted and calls are re-pointed. Its existing test suite is
the regression net, and the sigmoid and blur tests move to `internal/tone`
with them.

**`internal/sheet` is recommended but severable.** `internal/bw/sheet.go` is
already RGBA-based and only two lines of it are B&W-specific: the call to
`Render` and the read of `s.Name`. Parameterising it on
`render func(image.Image) image.Image` and `names []string` makes
`bw.ContactSheet` a five-line wrapper and gives `ciba` the label-fit and
cell-geometry logic for free. That logic matters: the lessons file records
that the label-fit assertion was one of five tests that guarded nothing, so
re-deriving the geometry from scratch in a second package is a known-hazardous
move. If this extraction is dropped, `internal/ciba/sheet.go` duplicates
~90 lines and must re-prove the geometry properties itself.

## 4. The pipeline

`Render(img image.Image, l Look) *image.RGBA`. Stages 1–5 are pointwise;
6 and 7 need neighbourhoods.

**1. Unpremultiply and linearize.** As `bw.Render` does today: `RGBA()` returns
alpha-premultiplied values, so straight colour is recovered when
`0 < a < 0xffff` and a fully transparent pixel is left at zero. Then
`tone.SRGBToLinear` per channel.

**2. Log exposure.** Per channel, with a fixed `floor = 1e-6` that exists only
to keep `log10` finite and sits far below any usable window:

```
e_c = clamp01( (log10(max(L_c, floor)) + Scale + Exposure) / Scale )
```

`Scale` is the width of the exposure window in log₁₀ units and `Exposure` is
its placement — the enlarger's printing time. A *smaller* `Scale` maps the
scene into a narrower window, which both steepens the response and clips more
of it: the short-scale trait, in one number.

`Exposure` is what makes the clipping real rather than merely steep. With the
window pinned at `[10^(-Scale), 1]`, the brightest input in an 8-bit file lands
exactly at `e=1` and nothing is ever above the window — highlights would be
*compressed* by the sigmoid's shoulder but never *clipped*, and Cibachrome's
near-absent highlight latitude would be missing from a design that claims it.
A positive `Exposure` pushes a band of the brightest values past `e=1`, where
`clamp01` collapses them to a single density: the specular clip. At
`Scale 1.7, Exposure 0.15` the bottom clamp likewise engages below linear
0.014, about sRGB 34, crushing the deepest shadows to `Dmax`.

**3. Per-channel characteristic curve.** Reusing the already-tested sigmoid:

```
D_c = Dmin[c] + (Dmax[c] - Dmin[c]) · (1 - tone.Sigmoid(e_c, Curve[c], Pivot[c]))
```

At `e=1` this is `Dmin[c]`, at `e=0` it is `Dmax[c]`. `Curve[c]` is the
*sigmoidal strength* — how pronounced the toe and shoulder are — and
`Sigmoid(x, 0, p) = x` gives a straight line, so zero is a meaningful default.
Overall slope comes from `Scale`; `Curve` and `Pivot` shape the ends and set
the crossover.

`Curve` is named `Curve` and not `Gamma` on purpose. It is not a power-law
exponent, and the lessons file records that a stated rationale contradicting
the design is worse than no rationale.

**4. Density back to reflectance**, normalised against the lowest of the three
`Dmin` values:

```
r_c = 10^(-(D_c - min(Dmin)))
```

Normalising against the *minimum* rather than per channel is what makes paper
white read as warm while still reaching full white somewhere: with
`Dmin = [.04, .05, .07]` the red channel hits 1.0 and green and blue sit just
below, so a specular highlight clips to roughly `(255, 252, 248)` — paper
white without optical brighteners, rather than a muddy off-white that never
reaches 255. Symmetrically, `Dmax = [2.5, 2.5, 2.35]` puts the deepest shadow
near `(12, 12, 16)`: a dense black with a whisper of blue.

**5. Back to perceptual space.** `tone.LinearToSRGB(r_c)`, written into the
output `*image.RGBA` with alpha 255.

**6. Clarity (acutance).** On luminance only, so hue is untouched. Rec.709
weights over the sRGB-encoded values give `Y`; then

```
delta = Clarity · (Y - tone.BlurBox3(Y, w, h, tone.RadiusPx(Radius, short)))
out_c = clamp(out_c + delta, 0, paperWhite_c)
```

Additive and channel-equal, matching `bw`'s `buf + Clarity·(buf-blur)` form.
Per-channel unsharp masking would fringe on saturated edges, which after
stage 3 is most of the frame.

**7. Bloom (the wet surface).** Highlights above a threshold, blurred wide,
added back:

```
h     = max(0, Y - BloomThresh) / (1 - BloomThresh)
out_c = clamp(out_c + Bloom · BlurBox3(h, w, h, RadiusPx(BloomRadius, short)),
              0, paperWhite_c)
```

Most of the glossy impression comes from D-max and micro-contrast; bloom
sells the specular sheen on top. It is one parameter and one buffer, and if it
does not earn its place on inspection it drops out by deleting three fields.

**Both spatial stages clamp to the print's own paper white, not to 1.0:**

```
paperWhite_c = LinearToSRGB( 10^-(Dmin[c] - min(Dmin)) )
```

which is stage 5 evaluated at `D_c = Dmin[c]` — the value a clipped highlight
already carries out of the pointwise stages, so the clamp can only ever undo
what stages 6 and 7 added. Bloom models veiling flare, which adds light, but a
print cannot be brighter than the paper it is on, and paper white is
per-channel: it is where the warm-white trait of §2 lives. Clamping to 1.0
instead erases that trait from every clipped highlight — `classic` needs only
+0.0196 on blue to close its gap, which `Bloom 0.06` does at any threshold that
lets bloom touch a specular at all. Putting the ceiling in the model rather
than in the parameter values is what keeps a later preset retune from silently
undoing the trait.

Both spatial radii are fractions of the short edge, via the same
`tone.RadiusPx`. That is the scale-invariance contract and it is the reason
the contact sheet predicts the full-resolution render.

### 4.1 Memory, precision, and stage order

Clarity and bloom are inherently **sequential**: bloom is computed from
luminance recomputed *after* clarity, because clarity brightens highlight edges
and those edges are exactly what should bloom. So each stage applies its own
delta and rounds its own result. The resulting double rounding costs at most
1/255, below the quantisation of the output format; buying a single rounding
would cost a fourth float plane to accumulate deltas across both stages, which
is not worth ~190 MB per worker at 24 MP.

The pointwise stages write straight into the 8-bit output rather than holding
three float planes, and the two spatial stages **share one set of working
planes**: `spatial` allocates luminance, blur and scratch once and passes them
to both `tone.BlurBox3Into` calls. Peak working set is therefore the output
RGBA (4 bytes/px) plus three float64 planes — 28 bytes/px, roughly 670 MB for
a 24 MP frame, multiplied by `-j`.

Measured, not derived: on a 3000×2000 render of `wet`, peak `HeapInuse` above
a settled baseline is **28.0 bytes/px** at the default `GOGC` and 32.1 with the
collector switched off. The 4-byte gap between the two is the per-pixel
`color.Color` that `image.Image.At` boxes on the heap, which is dead the
instant `RGBA()` has read it. `bw.Render` measures 25.0 bytes/px by the same
harness.

Letting each spatial stage allocate its own planes — which is what an
unreflective `tone.BlurBox3` call per stage does — measures 52.0 and 56.1
instead, because at the default `GOGC` the clarity stage's planes are still
live when bloom's are allocated: six planes, not three, and ~1.34 GB per
worker at 24 MP. That is why `BlurBox3Into` exists. No change to the `-j`
default; recorded here so the next reader does not have to re-derive it.

## 5. The Look type

```go
type Look struct {
    Name        string     `json:"name"`
    Desc        string     `json:"desc"`
    Scale       float64    `json:"scale"`       // width of the exposure window, log10 units; smaller = steeper
    Exposure    float64    `json:"exposure"`    // placement of the window, log10 units; positive = brighter, clips speculars
    Curve       [3]float64 `json:"curve"`       // per-channel sigmoidal strength; 0 = straight line
    Pivot       [3]float64 `json:"pivot"`       // per-channel curve centre, 0..1
    Dmin        [3]float64 `json:"dmin"`        // per-channel base density (paper-white tint)
    Dmax        [3]float64 `json:"dmax"`        // per-channel maximum density
    Clarity     float64    `json:"clarity"`
    Radius      float64    `json:"radius"`      // fraction of the short edge
    Bloom       float64    `json:"bloom"`
    BloomRadius float64    `json:"bloomRadius"` // fraction of the short edge
    BloomThresh float64    `json:"bloomThresh"`
}
```

`Validate` follows `bw.Style.Validate` exactly, including the NaN sweep over
every float field first — every range check below is a `<` or `>` comparison,
false for NaN in both directions, so a NaN would otherwise reach the pixel
loop untouched.

| Field | Range | Why the bound |
|---|---|---|
| `Name` | non-empty | it is the `-style` argument and the sheet label |
| `Scale` | `0 < s ≤ 4` | zero divides by zero; 4 log units exceeds any print process |
| `Exposure` | `-2 ≤ e ≤ 2` | beyond ±2 log units the whole frame is on one clamp |
| `Curve[c]` | `0 ≤ c ≤ 30` | the range `tone.Sigmoid` is already tested across |
| `Pivot[c]` | `0..1` | it is a position in the normalised exposure window |
| `Dmin[c]` | `0..1` | a base density above 1.0 is not a printable paper |
| `Dmax[c]` | `0..5` and `> Dmin[c]` | equal or inverted flips that channel negative |
| `Clarity` | `0..2` | as `bw` |
| `Radius` | `0..0.25` | above a quarter of the short edge it stops being local contrast |
| `Bloom` | `0..1` | above 1.0 the highlight adds more than it contains |
| `BloomRadius` | `0..0.5` | bloom is legitimately wider than clarity |
| `BloomThresh` | `0 ≤ t < 1` | at 1.0 the divisor in §4 stage 7 is zero |

`Render` expects a validated `Look` and does not re-validate, as `bw.Render`
does not.

### 5.1 `Dmin` and `Dmax` are coupled — tune them as a pair

Because the multiplier in §4 stage 3 is `(Dmax[c] - Dmin[c])`, lowering one
channel's `Dmax` shrinks that channel's whole density *range* and so lifts it
at every tone, not only in shadow. Left uncompensated this is a white-balance
error rather than a hue crossover: lowering `classic`'s `Dmax[2]` alone by
0.30 (to 2.12, `Dmin[2]` left at its current 0.06) puts a neutral sRGB 128 at
`(132, 131, 151)` — a spread of 20, none of it the intended crossover.

Raising that channel's `Dmin` compensates, to first order by

```
ΔDmin[c] ≈ (1 - S_mid) · ΔDmax[c]
```

where `S_mid` is the curve's value at the midtone (~0.8 for these presets, so
about a fifth of the `Dmax` change). Re-pairing to `Dmax[2] = 2.42`,
`Dmin[2] = 0.06` brings the same midtone to `(132, 131, 140)`, where the
remaining +8 is the intended crossover from the `Pivot` spread.

The endpoints are unaffected by this coupling and stay cleanly separated from
the middle: `tone.Sigmoid` is normalised so `f(0) = 0` and `f(1) = 1` exactly,
so the clipped highlight is *always* `Dmin[c]` and the clipped shadow *always*
`Dmax[c]`, whatever `Curve` and `Pivot` do. `Dmin` and `Dmax` own the ends;
`Curve` and `Pivot` own the middle.

## 6. Built-in looks

Six, spanning the space so the contact sheet is worth reading. `Pivot` was
solved numerically per §5.1 so each look places a neutral sRGB 128 where the
"mid" column says; the `Dmin`/`Dmax` pairs follow the coupling relation. All
six share `Radius 0.020` (`deep`: `0.018`), and `BloomRadius 0.060` /
`BloomThresh 0.80` except `wet` and `wetportrait` (`0.070` / `0.78`) and `deep`
(`0.82`).

| | `classic` | `wet` | `wetportrait` | `deep` | `azo` | `flat` |
|---|---|---|---|---|---|---|
| intent | the reference | maximum gloss | wet's ends, flatter middle | shortest scale | crossover exaggerated | straight-line control |
| `Scale` | 1.70 | 1.65 | 1.95 | 1.50 | 1.70 | 2.20 |
| `Exposure` | 0.15 | 0.20 | 0.20 | 0.25 | 0.15 | 0.00 |
| `Curve` | 6, 6, 6 | 7, 7, 7 | 5, 5, 5 | 8, 8, 8 | 7.5, 6, 8.5 | 0, 0, 0 |
| `Pivot` | .552, .552, .532 | .568, .568, .548 | .600, .600, .580 | .616, .616, .596 | .564, .594, .624 | .50, .50, .50 |
| `Dmin` | .04, .05, .06 | .03, .04, .05 | .03, .04, .05 | .04, .05, .06 | .03, .05, .08 | .04, .04, .04 |
| `Dmax` | 2.50, 2.50, 2.42 | 3.00, 3.00, 2.92 | 3.00, 3.00, 2.92 | 2.80, 2.80, 2.72 | 2.60, 2.50, 2.34 | 2.20, 2.20, 2.20 |
| `Clarity` | 0.35 | 0.45 | 0.35 | 0.40 | 0.35 | 0 |
| `Bloom` | 0.06 | 0.12 | 0.12 | 0.05 | 0.06 | 0 |
| white in → out | 255, 252, 250 | 255, 252, 250 | 255, 252, 250 | 255, 252, 250 | 255, 250, 242 | 255, 255, 255 |
| mid 128 → out | 132, 131, 140 | 126, 125, 136 | 127, 126, 133 | 118, 117, 129 | 133, 115, 113 | 130, 130, 130 |
| shadow 8 → out | 11, 11, 13 | 4, 4, 4 | 4, 4, 4 | 6, 6, 7 | 9, 11, 15 | 20, 20, 20 |
| 250 clips to white? | yes | yes | yes | yes | yes | **no** |

### 6.1 Why `wetportrait` exists, and what it cannot do

Skin is a strongly red-dominant midtone, and a monotone curve applied
independently to each channel cannot raise contrast without widening the gaps
between channels. That is the same mechanism §2 credits for the saturation, so
it applies to skin at full strength: on a midtone cheek measured from a real
portrait (input `154, 106, 97`, red-green separation `+48`), `wet` renders
`186, 70, 60` — separation `+116`, more than double.

`wetportrait` is a partial remedy, and its parameters were chosen by measuring
the alternatives rather than by eye. None of the three available levers is
strong:

| lever | range swept | best separation reached |
|---|---|---|
| curve steepness | `Scale` 1.65-2.10, `Curve` 7-4 | +82, at the cost of most of the character |
| density range | `Dmax` 3.0-2.0, `Dmin` compensated | +94, and the dense blacks are gone (shadow 4 → 35) |
| curve position | `Exposure` +0.35 to -0.25 | +106 |

`Scale 1.95` with `Curve 5.0` is the best return for character lost: separation
`+91` on that patch, and about a third of the excess over a neutral rendering
removed across a whole face. `Dmin` and `Dmax` are `wet`'s exactly, so paper
white stays `255, 252, 250` and the clipped shadow stays `4, 4, 4` — by
construction, since `tone.Sigmoid` pins the ends whatever the curve does.

Getting genuinely natural skin would need contrast applied to luminance with
chroma held back, or a hue-selective desaturation stage. Either contradicts §2's
thesis that the saturation and crossover come from the density curves alone
rather than a bolted-on stage, so neither is in scope here. Worth recording that
the limitation is also faithful: Cibachrome was a landscape and product medium,
and portraits on it were known to be harsh.

Every row of that table is the output of the **full** pipeline, spatial stages
included, on a uniform field of the stated input — verified as such, not only
through the pointwise stages. The "white in → out" row is the reason both
spatial stages clamp to the look's own per-channel paper white rather than to
1.0: clamping to 1.0 lifts a clipped highlight to a neutral `(255,255,255)` in
`classic`, `wet` and `deep`, and the row is then wrong for three of the five
presets while every pointwise assertion still passes. See §4 stages 6 and 7.

`flat` exists for the same reason `bw` has one: without a straight-line tile on
the sheet there is no way to see what the curve is contributing. It is also the
only look with `Exposure 0`, which is why 250 does not clip to white in it —
that column is the specular clip, visible as a discriminator.
`DefaultLook = "classic"`.

Two consequences worth knowing before writing the tests in §7:

- **`flat` has no paper-white tint** (`Dmin` is uniform) and no clip. It is the
  achromatic control, so it is exempt from the warm-white and clip assertions
  and is the *only* look that must render a neutral input perfectly neutral.
- **`wet` and `wetportrait` cannot show their shadow tint in 8 bits.** At
  `Dmax 3.00/2.92` both channels land on 4/255; the tint is real in density and
  below the output format's resolution. The shadow-tint assertion therefore runs
  on `classic` and `azo`, not on those two.

## 7. Testing

`tasks/lessons .md` records five tests on the `skyburn` branch that looked like
they guarded a property and guarded nothing. **Every test below is unproven
until the thing it names is mutated and the suite goes red.** The mutation is
part of the task, not a follow-up.

Properties, and the mutation that must break each:

| Property | Mutation that must fail it |
|---|---|
| Preview predicts full-res | cap `RadiusPx` at any constant |
| `RadiusPx` is proportional to the short edge | make it use the long edge |
| Each output channel is monotonic in its input | swap a `Pivot` sign |
| Blue sky densifies | widen `Scale` and zero `Curve` together (removes contrast) |
| A neutral input rotates hue against its own equalised twin (crossover) | equalise that look's own `Curve`, `Pivot`, `Dmin` and `Dmax` to channel 0 |
| Chroma of a known patch increases | widen `Scale` and zero `Curve` together (removes contrast) — per-channel spread alone (equalising `Curve`/`Pivot`) is the crossover mutation above, not this one |
| Pure white → `R = 255`, `R ≥ G ≥ B`, `B < 255` (all but `flat`) | equalise `Dmin` |
| Paper white survives the **full** pipeline: the brightest value each channel reaches is `paperWhite_c` | clamp stages 6 and 7 to 1.0 |
| A neutral input renders neutral in `flat`, within 3/255 | give `flat` a `Dmin` or `Pivot` spread |
| A neutral midtone stays within 14/255 of neutral in `classic` | lower `Dmax[2]` alone by 0.30, `Dmin[2]` unchanged |
| sRGB 250 and 255 render identically (all but `flat`), and differently in `flat` | set every `Exposure` to 0 |
| Deepest shadow has `B > R` in `classic` and `azo` | equalise `Dmax` |
| Input above the window all maps to one value | remove the `Exposure` term from §4 stage 2 |
| Input below the window all maps to one value | remove the bottom clamp |
| `Curve = 0` is a straight line in density | replace `Sigmoid` with a power curve |
| A flat patch stays flat (no grain) | add any noise term |
| Premultiplied alpha is unpremultiplied | drop the `a < 0xffff` branch |
| Dimensions and bounds preserved | off-by-one the output rect |
| EXIF survives decode → render → write | drop the exif argument to `WriteJPEG` |
| `-out` aliasing the source is refused | make `samePath` lexical |
| A *symlinked* `-out` is refused | drop `EvalSymlinks` from `resolveDir` |

Three constraints on how these are written, all from the lessons file:

- **Never reuse a tolerance across two questions.** The scale-invariance test
  and its teeth-guard must assert a *ratio* between the correct and mutated
  cases, not share an absolute threshold — that specific mistake let a
  threshold of 8 pass a defect of 4.85.
- **Assert the property, not a statistic that implies it.** `RadiusPx`
  proportionality is one direct line; inferring it from image statistics
  missed a clamp at 4096px entirely.
- **Endpoint and ordering assertions are satisfied by the identity function.**
  Where a stage is supposed to change shape, assert the shape.

The last two rows are not new work: `samePath`, `resolveDir` and
`checkNotSelf` are copied from `cmd/skyburn/scan.go`, where the symlink case
took two attempts to get right. The guard goes in **both** the planner
(`Scan`/`ScanOne`) and the writer (`renderFile`), because a whole-branch review
found that a planner-only check was routed around.

`ciba` writes `<base>-ciba.jpg`, and `-preview` writes `<base>-contact.png`;
both suffixes join the scan's own-output skip list so a second run does not
re-render its own output.

## 8. Out of scope

- Grain, in any form. §2 explains why.
- Vignette. A lens and enlarger artefact, not a print one.
- The `.recipe.json` sidecar and the `-ai` advisor (§3).
- A `.cube` LUT export. Worth adding later — stages 1–5 are pointwise, so they
  bake into a 3D LUT exactly, and the look becomes portable to Resolve or
  Lightroom. Stages 6 and 7 are spatial and cannot be baked, so the export
  would carry the tonal and colour half only. Not in this plan.
- 16-bit output. Output is JPEG, as `skyburn`'s is.

## 9. Success criteria

1. `go test ./...` passes, and every test in §7 has been shown to fail against
   its named mutation.
2. `ciba -preview photo.jpg` writes a five-tile labelled sheet, and each tile
   is pixel-identical to `ciba -style <that name>` run on the same thumbnail.
3. A blue-sky frame renders with the sky denser and rotated toward navy, reds
   hotter, and no visible grain.
4. Originals are byte-identical after every run, including under
   `-out` pointing at a symlink to the source directory.
5. EXIF survives a full-resolution render.
6. The presets are judged by eye and adjusted. This is the open-ended part:
   with no reference scans, §6's values are derived from the process and the
   sheet is how they get corrected.
