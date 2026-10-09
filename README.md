# convert

**Pictures into a PDF, and a PDF back into pictures.** Pure Go, CGO-free,
including `GOOS=js`.

```sh
pdfconv to-pdf   -dpi 300 -o scan.pdf page1.jpg page2.jpg page3.jpg
pdfconv from-pdf -dpi 150 -format png -pages 1,4-6 -o ./pages scan.pdf
pdfconv formats
```

```go
pdf, err := convert.ToPDF(images, convert.Options{DPI: 300})
err = convert.WriteTo(f, images, convert.Options{DPI: 300, Page: pdfkit.A4})
pages, err := convert.FromPDF(pdf, convert.RasterOptions{DPI: 150, Format: "png"})
```

## Why it is small

Both ends were already here. [`go-pdfkit/pdfkit`](https://github.com/go-pdfkit/pdfkit)
writes a PDF and can place an image on a page;
[`go-pdfkit/render`](https://github.com/go-pdfkit/render) draws a page into a
raster. What was missing was the join — so this is wiring and codecs, not a new
engine.

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
