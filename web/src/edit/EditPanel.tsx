import { useEffect, useId, useRef, type ReactNode } from "react";
import { useCan, useSchema } from "../hooks";
import { editRules } from "./rules";

/**
 * A form opened in place (not a modal: the page stays readable around it).
 * Focus moves to its heading when it opens, so a keyboard or screen-reader
 * user lands on it.
 */
export function EditPanel({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  const ref = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    ref.current?.focus();
  }, []);
  return (
    <section className="edit-panel" aria-labelledby={id}>
      <h2 id={id} ref={ref} tabIndex={-1}>
        {title}
      </h2>
      {children}
    </section>
  );
}

/**
 * Whether to offer the controls that declare something. GET /me decides what
 * is shown; the API checks every batch again, and a grant limited to a type
 * or a site is refused there (403) and the refusal is displayed.
 */
export function useCanDeclare(): boolean {
  return useCan("propose");
}

/** Whether to offer placing devices in racks: the caller may propose and the schema has mounted_in with a position. */
export function useCanMount(): boolean {
  const can = useCanDeclare();
  const schema = useSchema();
  return can && editRules(schema.data).canMount;
}
