import type { ReactNode } from "react";

/**
 * A message in a tinted box. `role` is the caller's: "alert" for an error the
 * reader must hear now, "status" for news, "note" for a lasting remark.
 */
export function Alert({ tone = "warn", role, title, children }: { tone?: "warn" | "danger" | "info" | "ok"; role?: "alert" | "status" | "note"; title?: ReactNode; children?: ReactNode }) {
  return (
    <div role={role} className={`alert alert-${tone}`}>
      {title ? (
        <p>
          <strong>{title}</strong>
        </p>
      ) : null}
      {children}
    </div>
  );
}
