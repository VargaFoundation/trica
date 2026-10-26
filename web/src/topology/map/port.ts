// Port names as a badge can carry them: `Ethernet49/1` becomes `Et49/1`,
// `GigabitEthernet0/1` becomes `Gi0/1`; a name with no known long form
// (`Port 24`, `eno1`) is kept. Pure, so that it is tested without a DOM.

import type { MapMember } from "../../api/types";

/** Long interface prefixes and the short form network engineers write; longest first, so that Ten* is not taken for Gigabit*. */
const PREFIXES: readonly (readonly [RegExp, string])[] = [
  [/^HundredGigabitEthernet\s*/i, "Hu"],
  [/^HundredGigE\s*/i, "Hu"],
  [/^FortyGigabitEthernet\s*/i, "Fo"],
  [/^TwentyFiveGigE\s*/i, "Twe"],
  [/^TenGigabitEthernet\s*/i, "Te"],
  [/^GigabitEthernet\s*/i, "Gi"],
  [/^FastEthernet\s*/i, "Fa"],
  [/^Port-?channel\s*/i, "Po"],
  [/^Management\s*/i, "Ma"],
  [/^Ethernet\s*/i, "Et"],
  [/^Loopback\s*/i, "Lo"],
];

/** The longest a badge's text gets, in characters. */
export const PORT_MAX = 9;

export function shortPort(name: string, max = PORT_MAX): string {
  let out = name.trim();
  for (const [re, short] of PREFIXES) {
    if (re.test(out)) {
      out = out.replace(re, short);
      break;
    }
  }
  return out.length > max ? `${out.slice(0, max - 1)}…` : out;
}

/**
 * The text of the badge at one end of a line: the port of the first cabled member,
 * and `+n` when more members end on that device (a bundle). Empty when no member
 * names a port (a device uplink has none).
 */
export function endBadge(members: readonly MapMember[], end: "a" | "b"): string {
  const cabled = members.filter((m) => m.kind !== "uplinked_to" && (end === "a" ? m.a_port.display : m.b_port.display) !== "");
  const first = cabled[0];
  if (!first) return "";
  const port = shortPort(end === "a" ? first.a_port.display : first.b_port.display);
  return cabled.length > 1 ? `${port} +${cabled.length - 1}` : port;
}

/** The mark of a bundle: `×3`; nothing for a single link. */
export const bundleMark = (links: number): string => (links > 1 ? `×${links}` : "");
