import { useState } from "react";
import { useI18n } from "../i18n";
import { blobBytes, saveBlob, type Raster, type RasterFormat } from "./download";
import { pdfFromJpeg } from "./pdf";
import { pdfFromVector, PT_PER_PX, type Figure } from "./vectorPdf";

type State = { kind: "idle" | "busy" | "done" | "failed"; what?: string; detail?: string };

/**
 * PNG and PDF of a figure. `render` draws the figure in the format asked for;
 * the PDF is drawn from `vector` when the figure has one (lines and text),
 * else it holds a JPEG. Nothing is sent anywhere: the file is built in the
 * page and saved by the browser.
 */
export function ExportButtons({
  basename,
  title,
  render,
  vector,
}: {
  /** File name without extension. */
  basename: string;
  /** The title stored in the PDF. */
  title: string;
  render: (format: RasterFormat) => Promise<Raster>;
  /** The figure as shapes: when given, the PDF is drawn from it, with text that can be selected. */
  vector?: () => Promise<Figure>;
}) {
  const { t } = useI18n();
  const [state, setState] = useState<State>({ kind: "idle" });

  const run = async (what: "PNG" | "PDF") => {
    setState({ kind: "busy", what });
    try {
      if (what === "PNG") {
        const r = await render("png");
        saveBlob(r.blob, `${basename}.png`);
      } else if (vector) {
        const pdf = pdfFromVector(await vector(), { title });
        saveBlob(new Blob([pdf as BlobPart], { type: "application/pdf" }), `${basename}.pdf`);
      } else {
        const r = await render("jpeg");
        const bytes = await blobBytes(r.blob);
        const pdf = pdfFromJpeg(bytes, { title, widthPt: (r.width / r.ratio) * PT_PER_PX });
        saveBlob(new Blob([pdf as BlobPart], { type: "application/pdf" }), `${basename}.pdf`);
      }
      setState({ kind: "done", what });
    } catch (e) {
      setState({ kind: "failed", what, detail: e instanceof Error ? e.message : String(e) });
    }
  };

  const busy = state.kind === "busy";
  return (
    <div className="export-buttons">
      <button type="button" disabled={busy} onClick={() => void run("PNG")}>
        {t("export.png")}
      </button>
      <button type="button" disabled={busy} onClick={() => void run("PDF")}>
        {t("export.pdf")}
      </button>
      <span role="status" className="export-status">
        {state.kind === "busy" ? t("export.busy") : null}
        {state.kind === "done" ? t("export.done", { format: state.what ?? "" }) : null}
        {state.kind === "failed" ? t("export.failed", { format: state.what ?? "", detail: state.detail ?? "" }) : null}
      </span>
    </div>
  );
}
