# ADR-0037: The topology PDF is drawn as vectors, in a font every viewer has

- **Status**: accepted (2026-10); amended by ADR 0062 (an uplink's line has an arrowhead)
- **Concerns**: `web/src/export/vectorPdf.ts`, `web/src/export/pdf.ts` (`assemblePdf`),
  `web/src/topology/graph.ts` (`figureOf`, `Drawing.vector`), `web/src/export/ExportButtons.tsx`.
  Replaces, for the topology only, the paragraph "The PDF is a picture" of ADR 0024. Issue #49,
  point 4.

## Context

ADR 0024 made both PDF exports a JPEG in a page, to avoid a PDF library and a font-licensing
question. A topology printed that way has text that cannot be selected or searched, and blurs when
zoomed; issue #49 asked for a vector PDF. The figure itself is simple: rounded boxes, straight or
curved lines (solid, dotted, dashed) and short labels.

## Decision

- **Still no library.** The writer already assembles one-page PDFs by hand; the object and
  cross-reference part is shared (`assemblePdf`), and `pdfFromVector` writes a content stream of
  lines, cubic curves, filled rectangles and text.
- **Helvetica, not embedded.** It is one of the 14 fonts a PDF viewer must provide, so nothing is
  shipped and there is no licence to check. Its advance widths (Adobe's AFM, public metrics) are a
  table in the code, used to size the boxes and centre the labels. The encoding is WinAnsi: Latin-1
  is written as is, any other character as `?`.
- **The figure is computed, not read off the canvas.** `figureOf` takes the positions from the
  layout and sizes the boxes for Helvetica; lines go from centre to centre under the boxes, and
  lines between the same two devices are bent apart by cytoscape's step (40 px). It works on a
  headless graph, so it is tested without a browser.
- **The rack sheet stays a picture.** It is drawn on a canvas by `rackCanvas.ts`; moving it to
  vectors is a separate change.

## Consequences

- The topology PDF's text is selectable and searchable, and the file is small (no image).
- Device names outside Latin-1 (Cyrillic, CJK) print with `?`. Embedding a Unicode font would lift
  that, at the cost of a font file and its licence.
- The PDF is close to the screen, not identical: Helvetica instead of the system font, the boxes'
  widths computed, curves approximating cytoscape's bezier edges, no arrowheads except an uplink's (ADR 0062: two
  short strokes on the box it points at, as on the screen).
