// One-page PDFs written by hand: no library, so nothing to pin, to audit or to
// load under the content security policy. pdfFromJpeg wraps a picture (the
// rack sheet: its text cannot be selected); vectorPdf.ts draws the topology
// as lines, boxes and text (docs/reference/ui.md says which is which).

export interface JpegInfo {
  width: number;
  height: number;
  /** 1 = grey, 3 = YCbCr/RGB. */
  components: number;
}

/** Reads the size out of the start-of-frame marker; throws on anything that is not a baseline or progressive JPEG. */
export function jpegInfo(b: Uint8Array): JpegInfo {
  if (b.length < 4 || b[0] !== 0xff || b[1] !== 0xd8) throw new Error("not a JPEG");
  let i = 2;
  while (i + 9 < b.length) {
    if (b[i] !== 0xff) {
      i++;
      continue;
    }
    const marker = b[i + 1] as number;
    if (marker === 0xff) {
      i++;
      continue;
    }
    // Markers without a length: standalone ones.
    if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd8)) {
      i += 2;
      continue;
    }
    const len = ((b[i + 2] as number) << 8) | (b[i + 3] as number);
    const isSOF = marker >= 0xc0 && marker <= 0xcf && marker !== 0xc4 && marker !== 0xc8 && marker !== 0xcc;
    if (isSOF) {
      const height = ((b[i + 5] as number) << 8) | (b[i + 6] as number);
      const width = ((b[i + 7] as number) << 8) | (b[i + 8] as number);
      const components = b[i + 9] as number;
      if (width === 0 || height === 0 || (components !== 1 && components !== 3)) throw new Error("unsupported JPEG");
      return { width, height, components };
    }
    i += 2 + len;
  }
  throw new Error("JPEG has no frame header");
}

/** The longest side a PDF page may have: 200 inches, the limit viewers agree on. */
export const MAX_PAGE_PT = 14400;

export interface PdfOptions {
  title: string;
  /** Page width in points; the height follows the picture. */
  widthPt: number;
}

const enc = new TextEncoder();
export const ascii = (s: string) => enc.encode(s);

/** A text string as UTF-16BE in hex: any title, with nothing to escape. */
export function pdfTitle(s: string): string {
  let hex = "FEFF";
  for (let i = 0; i < s.length; i++) hex += s.charCodeAt(i).toString(16).padStart(4, "0").toUpperCase();
  return `<${hex}>`;
}

export const num = (n: number) => String(Math.round(n * 100) / 100);

export function pdfFromJpeg(jpeg: Uint8Array, opts: PdfOptions): Uint8Array {
  const info = jpegInfo(jpeg);
  let w = opts.widthPt;
  let h = (w * info.height) / info.width;
  const over = Math.max(w, h) / MAX_PAGE_PT;
  if (over > 1) {
    w /= over;
    h /= over;
  }
  const content = `q ${num(w)} 0 0 ${num(h)} 0 0 cm /Im0 Do Q`;
  const cs = info.components === 1 ? "/DeviceGray" : "/DeviceRGB";

  return assemblePdf([
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${num(w)} ${num(h)}] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>`,
    [
      ascii(
        `<< /Type /XObject /Subtype /Image /Width ${info.width} /Height ${info.height} /ColorSpace ${cs} /BitsPerComponent 8 /Filter /DCTDecode /Length ${jpeg.length} >>\nstream\n`,
      ),
      jpeg,
      ascii("\nendstream"),
    ],
    `<< /Length ${content.length} >>\nstream\n${content}\nendstream`,
    `<< /Title ${pdfTitle(opts.title)} /Producer ${pdfTitle("trica")} >>`,
  ]);
}

/**
 * The file around numbered objects: object n is bodies[n-1], the first is the
 * catalog and the last the document information. Offsets and cross-reference
 * table are computed here, so that no caller counts bytes.
 */
export function assemblePdf(bodies: Array<string | Uint8Array[]>): Uint8Array {
  const chunks: Uint8Array[] = [];
  const offsets: number[] = [];
  let at = 0;
  const push = (c: Uint8Array) => {
    chunks.push(c);
    at += c.length;
  };
  push(new Uint8Array([...ascii("%PDF-1.4\n%"), 0xe2, 0xe3, 0xcf, 0xd3, 0x0a]));
  bodies.forEach((body, i) => {
    const n = i + 1;
    offsets[n] = at;
    push(ascii(`${n} 0 obj\n`));
    if (typeof body === "string") push(ascii(body));
    else body.forEach(push);
    push(ascii("\nendobj\n"));
  });

  const size = bodies.length + 1;
  const xref = at;
  let table = `xref\n0 ${size}\n0000000000 65535 f \n`;
  for (let n = 1; n < size; n++) table += `${String(offsets[n]).padStart(10, "0")} 00000 n \n`;
  table += `trailer\n<< /Size ${size} /Root 1 0 R /Info ${size - 1} 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  push(ascii(table));

  const out = new Uint8Array(at);
  let p = 0;
  for (const c of chunks) {
    out.set(c, p);
    p += c.length;
  }
  return out;
}
