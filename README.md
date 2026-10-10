# convert

**Pictures into a PDF, a PDF back into pictures, a PDF redrawn, and comic
archives both ways.** Pure Go, CGO-free, including `GOOS=js`.

```sh
pdfconv to-pdf       -dpi 300 -o scan.pdf page1.jpg page2.jpg logo.svg
pdfconv from-pdf     -dpi 150 -format png -pages 1,4-6 -o ./pages scan.pdf
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
out, err := convert.Redraw(pdf, convert.RedrawOptions{Greyscale: true})
pdf, err = convert.ArchiveToPDF(cbz, convert.Options{})
err = convert.ToArchive(w, pdf, convert.RasterOptions{DPI: 150})
pdf, err = convert.SVGToPDF(drawing, convert.Options{DPI: 72})
```

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

## SVG is DRAWN, not translated

An SVG handed to `to-pdf` is recognised by its **content** and rasterised at
the chosen DPI. ⛔ Both are vector languages, so a reader could reasonably
expect the paths to survive — and they do not. Turning one into the other means
reimplementing a renderer's worth of semantics (gradients in two unit systems,
clip paths, stroke joins, transforms, text layout), and a half-done translation
produces a file that opens and is quietly wrong. Drawing it is a complete
answer that is honest about its own resolution.

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
| **read** | PNG, JPEG, GIF (standard library); BMP, TIFF, WebP (`golang.org/x/image`) |
| **write** | PNG, JPEG, GIF, BMP, TIFF |

⛔ **WebP is refused by name, not as an unknown format.** There is no WebP
encoder in pure Go. "I do not know that format" would be a lie — it reads here
perfectly well — and writing a PNG under a `.webp` name would be worse than
either. The two refusals are different sentences and a test asserts that
neither reads like the other.

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
