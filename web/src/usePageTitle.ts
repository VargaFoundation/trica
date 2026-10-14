import { useEffect } from "react";

/** Sets the tab title (what a screen reader announces on a new page and what history lists). */
export function usePageTitle(title: string | undefined): void {
  useEffect(() => {
    if (title) document.title = `${title} · trica`;
  }, [title]);
}
