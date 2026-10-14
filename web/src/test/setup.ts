import "@testing-library/jest-dom/vitest";
import { configure } from "@testing-library/react";

// A loaded CI runner can take longer than the 1 s default to render a page.
configure({ asyncUtilTimeout: 5000 });
import { resetAuthForTest } from "../api/token";

beforeEach(() => {
  try {
    sessionStorage.clear();
    localStorage.clear();
  } catch {
    /* jsdom always has storage; kept for symmetry with the app code */
  }
  resetAuthForTest();
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.lang = "en";
  document.title = "";
  // The app moves focus to <main> on navigation; start every test from a clean page.
  if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
});
