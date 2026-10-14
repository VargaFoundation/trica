// Saving a file the page built itself. A blob: URL is same-origin, so it
// needs no change to the content security policy (the page's own scripts make
// the link; nothing is fetched). The test in e2e/ runs it under the real policy.

/** A raster figure: the bytes, the size in pixels, and the ratio it was drawn at (pixels per CSS pixel). */
export interface Raster {
  blob: Blob;
  width: number;
  height: number;
  ratio: number;
}

export type RasterFormat = "png" | "jpeg";

export function canvasBlob(canvas: HTMLCanvasElement, format: RasterFormat): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (b) => (b ? resolve(b) : reject(new Error("the browser could not encode the image"))),
      `image/${format}`,
      format === "jpeg" ? 0.92 : undefined,
    );
  });
}

/** The bytes of a blob (FileReader: Blob.arrayBuffer is missing from older engines and from jsdom). */
export function blobBytes(blob: Blob): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(new Uint8Array(r.result as ArrayBuffer));
    r.onerror = () => reject(new Error("could not read the image"));
    r.readAsArrayBuffer(blob);
  });
}

export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.rel = "noopener";
  document.body.append(a);
  a.click();
  a.remove();
  // The download has started when click() returns; give the browser a moment before releasing the URL.
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** A file name from a free label: letters, digits and dashes only. */
export function fileSlug(label: string): string {
  const s = label
    .normalize("NFKD")
    .replace(/[^\w.-]+/g, "-")
    .replace(/^[-.]+|[-.]+$/g, "")
    .toLowerCase();
  return s || "export";
}
