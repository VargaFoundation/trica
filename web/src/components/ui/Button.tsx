import type { ReactNode } from "react";
import { Link } from "react-router";

export type ButtonVariant = "secondary" | "primary" | "danger" | "ghost";

/** A link dressed as a button: for an action that is a navigation. */
export function ButtonLink({ to, variant = "secondary", children }: { to: string; variant?: ButtonVariant; children: ReactNode }) {
  return (
    <Link to={to} className={variant === "secondary" ? "button" : `button ${variant}`}>
      {children}
    </Link>
  );
}
