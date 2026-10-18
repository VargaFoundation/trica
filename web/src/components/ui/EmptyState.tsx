import type { ReactNode } from "react";
import { Icon, type IconName } from "./icons";

/** "Nothing here": said in words, with what to do next when there is something. */
export function EmptyState({ icon = "inbox", children, action }: { icon?: IconName; children: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty-state">
      <Icon name={icon} />
      <p>{children}</p>
      {action}
    </div>
  );
}
