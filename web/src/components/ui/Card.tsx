import type { ReactNode } from "react";

/** A surface raised above the page, with an optional heading and actions. */
export function Card({
  title,
  titleId,
  level = 2,
  actions,
  children,
  className,
  as: Tag = "section",
}: {
  title?: ReactNode;
  titleId?: string;
  level?: 2 | 3;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  as?: "section" | "div" | "article";
}) {
  const H = level === 2 ? "h2" : "h3";
  return (
    <Tag className={className ? `card ${className}` : "card"} aria-labelledby={Tag === "section" && title && titleId ? titleId : undefined}>
      {title || actions ? (
        <div className="card-head">
          {title ? <H id={titleId}>{title}</H> : <span />}
          {actions}
        </div>
      ) : null}
      {children}
    </Tag>
  );
}
