# convert

**Pictures into a PDF, a PDF back into pictures or SVG, a PDF redrawn, and comic
archives both ways.** Pure Go, CGO-free, including `GOOS=js`.

```sh
pdfconv to-pdf       -dpi 300 -o scan.pdf page1.jpg page2.jpg logo.svg
pdfconv from-pdf     -dpi 150 -format png -pages 1,4-6 -o ./pages scan.pdf
pdfconv to-svg       -pages 1-3 -o ./svg scan.pdf       # scan_svg.zip; one page: scan.svg
pdfconv redraw       -greyscale -invert out.pdf grey.pdf
pdfconv redraw       -scanner -skew 0.6 clean.pdf looks-scanned.pdf
pdfconv from-archive book.cbz book.pdf
pdfconv to-archive   -title "Volume 3" book.pdf book.cbz
pdfconv bundle       send-these.zip a.pdf b.pdf c.pdf
pdfconv formats
```

```go
pdf, err := convert.ToPDF(images, convert.Options{DPI: 300})
err = convert.WriteTo(f, images, convert.Options{DPI: 300, Page: pdfkit.A4})
pages, err := convert.FromPDF(pdf, convert.RasterOptions{DPI: 150, Format: "png"})
svgs, err := convert.PDFToSVG(pdf, convert.SVGOptions{Pages: []int{1, 2}})
err = convert.SVGArchive(w, svgs)                                    // page_1.svg, page_2.svg
out, err := convert.Redraw(pdf, convert.RedrawOptions{Greyscale: true})
pdf, err = convert.ArchiveToPDF(cbz, convert.Options{})
err = convert.ToArchive(w, pdf, convert.RasterOptions{DPI: 150})
pdf, err = convert.SVGToPDF(drawing, convert.Options{DPI: 72})       // vector, or drawn
pdf, err = convert.SVGToPDFVector(drawing, convert.Options{})        // vector or nothing
```

## `to-svg`: a PDF page as SVG

```sh
pdfconv to-svg [-pages 1,3,5-9] [-password-file f] [-o dir] in.pdf
```

⛔ **Passwords never go on the command line.** `-password` is gone from every
verb (`to-svg`, `from-pdf`, `to-archive`, `redraw`): an argument is visible in
`ps` and in the shell's history. `-password-file <path>` reads it from a file,
`-password-file -` from standard input; one trailing newline is stripped. This
is a **breaking CLI change** (a MINOR bump below v1).

Built on `render.PageSVG`: paths, clips, images and transparency groups become
SVG elements, text becomes **outlines** (so the file needs no font), and what
SVG 1.1 cannot say — tiling and shading patterns, soft masks — is drawn by the
pixel renderer and embedded as an `<image>` where it falls. Nothing the PDF
says is copied into the output as text, and `render` checks every document
against an allowlist before handing it over.

**Naming follows BentoPDF's "PDF to SVG"**: one page is `name.svg`, several are
`name_svg.zip` holding `page_N.svg`. The shape follows the pages *selected*, so
`-pages 2` of a long file is a single `name.svg`. `-pages` is an addition:
BentoPDF offers no page selection. Not matched: BentoPDF also takes several
PDFs at once into one `pdf_to_svg.zip`; here one PDF is one run.

⛔ **All or nothing**, as `from-pdf` and `to-archive` are. The first page that
cannot be written stops the run, the error leads with the page number
(`in.pdf: page 4: its clips nest deeper than an SVG can hold`), the exit status
is 1, and **no file is left behind** — a zip with page 4 of 9 missing opens,
counts and looks complete. Two refusals are expected on real files and stay
reachable through `errors.Is`: `render.ErrSVGTooDeep` (clips nested past 64;
[render#143](https://github.com/go-pdfkit/render/issues/143) tracks a raster
fallback) and `render.ErrTimedOut` (ten seconds a page).

The input is untrusted, and everything is bounded **before** it is spent:

| bound | value | where |
| --- | --- | --- |
| pages in one run | 1000 (`convert.MaxSVGPages`) | before the first page is drawn, and before the list of pages is built; choose with `-pages` |
| SVG of one page | 64 MiB (`convert.DefaultSVGPageBytes`) | the smaller of this and what the run has left, handed to `render`, so it is refused rather than built and then found large |
| SVG of the whole run, hence the zip | 256 MiB (`convert.MaxSVGBytes`) | counted as each page is written |
| time for the whole run | 5 min (`convert.MaxSVGRunTime`) | checked between pages |
| time for one page | 10 s (`convert.DefaultSVGPageTime`) | the smaller of this and what the run has left |

**Memory is one page, not the run.** The pages are streamed (`convert.StreamSVG`,
`convert.NewSVGArchive`) into a temporary file in the output directory; the
peak heap while 254 MiB of SVG went through was 41 MiB. `PDFToSVG` keeps every
page in memory, up to 256 MiB, and is for callers that want them in memory.
A page refused at its limit can still cost a large heap, driven by `render`
parsing the content stream, not by the size of the SVG.

⛔ **The time limit is a request, not a promise, while
[render#142](https://github.com/go-pdfkit/render/issues/142) is open**: render's
deadline is only checked now and then, and one page has been seen to overrun it
about seventy times. The run's time is checked between pages, so it inherits
that overrun within a page.

The file is **never written through a link or over a file**: the output is made
under a temporary name, then given its name with a hard link, which refuses a
name that exists (a file, a directory, a symlink, even a dangling one); where
hard links are not available, an exclusive create does the same. A refusal
leaves nothing behind.

Zip entry names are built from the page **number** and nothing else; no byte of
the PDF reaches one. A page number given twice is refused, by the API as by the
command.

### Against MuPDF

Allocations, exact, page 1 of three files (`GOMAXPROCS=1`): a 292 KB SVG took
26 749 allocations and 11.2 MB; a 40 KB one 105 542 and 86.9 MB; a 6 KB one
5 836 and 8.8 MB. **Timing against `mutool draw -F svg` was deferred**: the
machine's one-minute load was 34 when it was measured, and a time taken there
is not a time.

## ⛔ page10 comes after page2

A comic archive names its pages and **nothing else records the order**. Sorted
as bytes, `page10.png` comes before `page2.png`, so a hundred-page book reads
1, 10, 11, … 2, 20, … — and nothing says so: every page is present, the count
is right, and the file opens.

The ordering is **natural**: a run of digits compares as a number, without
being parsed, so a page numbered past what an `int` holds still sorts. It is
**case-insensitive**, because an archive mixes `Page01` and `page02` and a
case-sensitive sort reads the book in two halves.

Going the other way, page names are **zero-padded to the width of the last
page number**, not to a fixed four: padded too narrowly, a thousand-page
document reads 1, 10, 100, 1000, 101 in every reader that sorts by name —
which is every reader of this format.

What an archiver added — `__MACOSX/`, `.DS_Store`, `ComicInfo.xml`, a
thumbnail — is skipped rather than refused. Real archives all carry some;
refusing them would refuse most real files, and counting them puts rubbish in
the middle of the book.

Going out, a **`ComicInfo.xml`** goes in first. It is what Komga, Kavita and
Calibre read to know what a book is called and how many pages it has; without
it a reader shows an untitled pile and has to open every entry to count it. The
title is escaped — it comes from a caller by way of a filename, and an
ampersand in it would otherwise produce metadata no reader can parse, which is
worse than none because it *looks* like metadata.

### What is not here

⛔ **CBR is not read.** A `.cbr` is a RAR archive, and this package reads ZIP
only. [`go-filesystems/rar`](https://github.com/go-filesystems/rar) is the
fleet's pure-Go RAR reader if that is wanted; it is left out here because it
pulls a decoder and two modules in for one format variant.

`bundle` is the other half of what a reader might expect from "PDF to ZIP": it
packs **whole files, unchanged**. Several PDFs that have to travel together are
a packaging problem, not a conversion one. It is written to a temporary beside
the target and renamed, so a failure leaves nothing under the final name — a
half-written ZIP has its directory missing from the end, so it does not even
open, but it is there and dated and somebody will try to send it.

## SVG stays VECTOR where it can

An SVG handed to `to-pdf` is recognised by its **content** and its outlines
become PDF path operators — so the result scales without pixels, and a figure
that would be tens of kilobytes of raster is about a kilobyte of geometry.

⛔ **What makes that safe is the refusal, not the translation.** A converter
between two vector languages that silently drops what it does not understand
produces a file that opens and is quietly wrong, which is worse than a picture.
[`go-gfx/gfx/svg`](https://github.com/go-gfx/gfx)'s `Walk` reports every feature
it could not hand over — a gradient, a clip path, an embedded raster — and a
document that uses one is **drawn whole** at the chosen DPI instead. There is
no third outcome where some of it survived.

This page used to say "SVG is DRAWN, not translated", and the reasoning it gave
still holds: turning one vector language into another means reimplementing a
renderer's worth of semantics, and half a translation is worse than none. What
changed is that *half* became a condition this package can test for rather than
a risk it could only avoid by never trying.

Measured: our renderer's picture of the vector page against `go-gfx`'s own
drawing of the same SVG differs by a mean of **0.03 of 255** per channel — and a
blank page of the same size scores 38.19, so the measure can tell drawing from
nothing. `qpdf --check`, `mutool info` and ghostscript all accept the output.

`-dpi` is consulted only when the document is drawn. On the vector path it has
nothing to say, because the output has no resolution.

The pixel ceiling is **passed through** to
[`go-gfx/gfx/svg`](https://github.com/go-gfx/gfx): an SVG is the kind of thing
people accept from strangers, and its own `width` and `height` decide an
allocation — a hundred bytes declaring `width="40000"` cost 6.1 GiB before that
was bounded.

## Why it is small

Both ends were already here. [`go-pdfkit/pdfkit`](https://github.com/go-pdfkit/pdfkit)
writes a PDF and can place an image on a page;
[`go-pdfkit/render`](https://github.com/go-pdfkit/render) draws a page into a
raster; [`go-images/images`](https://github.com/go-images/images) already had
`Invert`, `Grayscale`, `AdjustBrightness`, `AdjustContrast` and `Rotate`. What
was missing was the join — so this is wiring and codecs, not a new engine.

## `redraw` RASTERISES, and says so everywhere

⛔ What comes out has **no text in it**: no selection, no search, no copy, no
screen reader, and a much larger file. That is not a shortcoming — changing the
colours a page is *painted* in means painting it — but it is a thing a caller
has to have decided on purpose, which is why the verb is named for what it does
rather than for what it is for.

Rotating, cropping, stamping and reordering **keep** the text. Those live in
[`go-pdfkit/ops`](https://github.com/go-pdfkit/ops), not here, and `redraw`
with no change asked for says so on stderr before it does anything.

| | |
| --- | --- |
| `-greyscale` | drop the colour |
| `-invert` | turn light into dark |
| `-brightness` | −1 (black) to 1 (white) |
| `-contrast` | 1 leaves it, 2 doubles it, 0.5 halves it |
| `-background` | `#rgb`, `#rrggbb` or `#rrggbbaa`, painted **by the renderer** |
| `-scanner` | skew, grain and a worn lamp |

The order of the transforms is **written down** rather than left to chance:
colour, then tone, then the scanner's damage last. Greyscale after a contrast
change is not the same picture as contrast after greyscale, and a caller who
sets both would otherwise be guessing.

The scanner's grain is **reproducible**: a zero `Seed` is a *fixed* seed, not a
random one, because two runs over the same file must produce the same bytes or
nothing downstream can be compared, cached or checksummed. ⛔ The page index is
mixed into it, or every page of a document gets identical grain — the one thing
a scanner never does.

### Two defects this found, both invisible to a byte count

| | |
| --- | --- |
| `-background` painted **nothing** | it was composited *under* the finished raster, and the renderer had already filled the page with opaque white. The option was documented, shipped and inert. |
| `-brightness` changed **nothing** | `images.AdjustBrightness` adds its delta in channel units, 0–255, not in the −1..1 this option is documented in. A brightness of 0.4 added 0.4 of a level out of 255. |

Both were found by tests that read **pixels**. The page count, the byte count
and the exit status were right throughout.

## Formats

| | |
| --- | --- |
| **read** | PNG, JPEG, GIF (standard library); BMP, TIFF, WebP (`golang.org/x/image`); PSD ([go-images/psd](https://github.com/go-images/psd)) |
| **write** | PNG, JPEG, GIF, BMP, TIFF, WebP ([go-images/webp](https://github.com/go-images/webp)) |

⛔ **For WebP, quality 100 means the LOSSLESS codec**, not the finest lossy
one. They are two different codecs — VP8L is always exact, VP8 never is — and
which wins depends entirely on the page:

| a 160×160 detailed page | a flat swatch |
|---|---|
| lossless 10 586 B, **lossy at 50: 886 B** | **lossless 102 B**, lossy at 50: 176 B |

A VP8 frame carries a fixed header — 1 056 bits of *"every coefficient
probability is unchanged"* alone — that a nearly empty picture can never pay
back. So the knob has to reach both codecs, and the only honest place for the
boundary is the end of the range. A test reads the container's FourCC
(`VP8L` against `VP8 `, with its trailing space) rather than looking at the
picture, because a high-quality lossy page looks like the original too.

⛔ **A format that can be read and not written is refused BY NAME.** PSD is the
one in that position now: the flattened composite reads perfectly well, and
writing a layered document is a different job. "I do not know that format"
would be a lie, and writing a PNG under a `.psd` name would be worse than
either. The two refusals are different sentences and a test asserts that
neither reads like the other — a test that until recently said this about
WebP, and had to be rewritten when the encoder arrived.

⛔ **The content decides the format, never the name.** A scanner writing JPEG
into `page.png` is ordinary; a converter that believed the extension would hand
the decoder the wrong reader, or refuse a file it can read.

## What decides the page

A picture has pixels, a page has points, and only the **DPI** says which page a
picture is:

```sh
pdfconv to-pdf -dpi 300 scan.jpg   # 2480x3508 comes out A4
pdfconv to-pdf -dpi 72  scan.jpg   # the same file comes out a metre tall
```

With no `-page`, each page is sized to **its own** picture — which is what a
set of scans of different sizes needs, and why an empty page size is an
instruction rather than a missing value. With `-page a4` the picture is
**fitted** inside the paper:

- ⛔ **one scale for both axes.** Scaling them separately fills the page and
  stretches the picture, and a stretched scan is a defect nobody reports
  because the page looks full;
- ⛔ **only ever shrink.** "Fit on this paper" is not "make it as large as this
  paper": a 32-pixel logo blown up to A4 is a poster nobody asked for;
- centred, and a margin wider than the paper gives way rather than erasing the
  picture — the picture is the content, the margin is a preference.

## What it refuses

| | |
| --- | --- |
| no pictures | an empty PDF is a *file*, and a caller who passed an empty slice sees one and believes it worked |
| a picture past `MaxPixels` | the size is decided by the **input**; the default ceiling is a hundred megapixels |
| `-margin` with no `-page` | a page sized to its picture has no room to leave, and ignoring it lets somebody believe their pages have one |
| a page number past the end | and it says how many there are |
| a PDF that opens and holds no pages | said out loud, because an empty result otherwise reads like a selection that matched nothing |

A page that cannot be written stops the run: a directory that **looks** like a
complete conversion and is one page short is not something anybody checks. For
the same reason the output directory is created *after* the conversion
succeeds, so a failure leaves nothing behind that could be mistaken for "no
pages".

## Checks

100 % statement coverage on both packages, `go vet` and `gofmt` clean, and
builds for nine targets including `js/wasm`, `linux/loong64` and `linux/s390x`.

The end-to-end witness is a picture, not a number: a four-quadrant swatch goes
in, comes back through a real rasteriser, and each quadrant is sampled in its
own corner — ⛔ a flat fill survives being drawn upside down, so the test has to
be able to see an orientation it does not expect.

## Licence

BSD-3-Clause.
