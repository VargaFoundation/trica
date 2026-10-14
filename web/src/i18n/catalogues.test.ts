import { en } from "./en";
import { fr } from "./fr";
import { detectLocale, interpolate } from "./index";

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe("catalogues", () => {
  it("fr has exactly the keys of en", () => {
    expect(Object.keys(fr).sort()).toEqual(Object.keys(en).sort());
  });

  it("fr uses the same {placeholders} as en for every key", () => {
    for (const [key, text] of Object.entries(en)) {
      expect(placeholders(fr[key as keyof typeof fr]), key).toEqual(placeholders(text));
    }
  });

  it("no message is empty", () => {
    for (const cat of [en, fr]) for (const [k, v] of Object.entries(cat)) expect(v.trim(), k).not.toBe("");
  });

  it("every .one has its .other and the reverse", () => {
    const keys = new Set(Object.keys(en));
    for (const k of keys) {
      if (k.endsWith(".one")) expect(keys.has(k.replace(/\.one$/, ".other")), k).toBe(true);
      if (k.endsWith(".other")) expect(keys.has(k.replace(/\.other$/, ".one")), k).toBe(true);
    }
  });

  it("fr is really translated (differs from en for most keys)", () => {
    const same = Object.keys(en).filter((k) => en[k as keyof typeof en] === fr[k as keyof typeof fr]);
    // Brand names, units and "OK" legitimately match; a copy of the English catalogue would not.
    expect(same.length).toBeLessThan(Object.keys(en).length * 0.1);
  });
});

describe("interpolate", () => {
  it("replaces named placeholders and leaves unknown ones visible", () => {
    expect(interpolate("{a} and {b}", { a: 1 })).toBe("1 and {b}");
  });
});

describe("detectLocale", () => {
  it("honours a stored choice over the browser", () => {
    expect(detectLocale(["en-US"], "fr")).toBe("fr");
  });
  it("takes the first supported browser language", () => {
    expect(detectLocale(["de-DE", "fr-CA", "en"], null)).toBe("fr");
  });
  it("falls back to English", () => {
    expect(detectLocale(["de"], null)).toBe("en");
    expect(detectLocale([], "xx")).toBe("en");
  });
});
