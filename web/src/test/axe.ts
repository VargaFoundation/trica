import axe from "axe-core";

/**
 * Runs axe-core (WCAG 2.0/2.1/2.2 A and AA rules) on the whole document and
 * fails with a readable list when something is reported.
 *
 * color-contrast is switched off: jsdom does no layout or painting, so axe
 * cannot compute it. Contrast is covered separately by styles/tokens.test.ts,
 * which computes the ratio of every colour pair of both themes.
 */
export async function expectNoA11yViolations(): Promise<void> {
  const results = await axe.run(document, {
    runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"] },
    rules: { "color-contrast": { enabled: false } },
  });
  if (results.violations.length > 0) {
    const text = results.violations
      .map((v) => `${v.id} (${v.impact}): ${v.help}\n${v.nodes.map((n) => `    ${n.target.join(" ")}  ${n.html.slice(0, 120)}`).join("\n")}`)
      .join("\n");
    throw new Error(`axe found ${results.violations.length} accessibility violation(s):\n${text}`);
  }
}
