import type { ReactNode, TableHTMLAttributes } from "react";

/** A dense table that scrolls sideways on its own instead of the page. */
export function Table({ children, ...props }: TableHTMLAttributes<HTMLTableElement> & { children: ReactNode }) {
  return (
    <div className="table-wrap">
      <table {...props}>{children}</table>
    </div>
  );
}
