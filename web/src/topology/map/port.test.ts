import { describe, expect, it } from "vitest";
import type { MapMember, MapPlace } from "../../api/types";
import { bundleMark, endBadge, shortPort } from "./port";

const place = (display: string): MapPlace => ({ id: `id-${display}`, display, type: "interface" });
const member = (a: string, b: string, kind = "cabled_to"): MapMember => ({ a_device: place("sw"), a_port: place(a), b_device: place("srv"), b_port: place(b), confidence: "observed", kind });

describe("short port names", () => {
  it.each([
    ["Ethernet49/1", "Et49/1"],
    ["GigabitEthernet0/1", "Gi0/1"],
    ["TenGigabitEthernet1/0/2", "Te1/0/2"],
    ["FortyGigabitEthernet1/0/1", "Fo1/0/1"],
    ["HundredGigE0/0/1", "Hu0/0/1"],
    ["FastEthernet0/3", "Fa0/3"],
    ["Port-channel12", "Po12"],
    ["Management1", "Ma1"],
    ["Ethernet 1/2", "Et1/2"],
    ["Port 24", "Port 24"],
    ["eno1", "eno1"],
    ["  Ethernet3  ", "Et3"],
    ["", ""],
  ])("%s becomes %s", (long, short) => expect(shortPort(long)).toBe(short));
  it("cuts what is still too long, with an ellipsis", () => {
    expect(shortPort("a-very-long-port-name")).toBe("a-very-l…");
    expect(shortPort("abcdef", 4)).toBe("abc…");
  });
});

describe("the badge at one end of a line", () => {
  it("names the port of the first member, at that end", () => {
    const ms = [member("Ethernet49/1", "eth0")];
    expect(endBadge(ms, "a")).toBe("Et49/1");
    expect(endBadge(ms, "b")).toBe("eth0");
  });
  it("counts the other members of a bundle", () => {
    const ms = [member("Ethernet49/1", "eth0"), member("Ethernet50/1", "eth1"), member("Ethernet51/1", "eth2")];
    expect(endBadge(ms, "a")).toBe("Et49/1 +2");
  });
  it("is empty for a device uplink, which has no port, and for no member", () => {
    expect(endBadge([member("", "", "uplinked_to")], "a")).toBe("");
    expect(endBadge([], "b")).toBe("");
  });
});

describe("the mark of a bundle", () => {
  it.each([[0, ""], [1, ""], [2, "×2"], [8, "×8"]])("%d links give %j", (n, mark) => expect(bundleMark(n)).toBe(mark));
});
