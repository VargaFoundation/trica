import type { ReactElement } from "react";

/*
 * The application's icons: drawn here on a 24 × 24 grid, stroked with
 * currentColor (app.css .icon), so they take the colour of their text and of
 * the theme. No icon library, no font, nothing fetched (ADR 0018, ADR 0040).
 * An icon is decoration: it is hidden from assistive technology, and the
 * word next to it says what it means.
 */

const PATHS = {
  server: (
    <>
      <rect x="3" y="3.5" width="18" height="7" rx="1.5" />
      <rect x="3" y="13.5" width="18" height="7" rx="1.5" />
      <path d="M7 7h.01M7 17h.01M11 7h6M11 17h6" />
    </>
  ),
  switch: (
    <>
      <rect x="2" y="9" width="20" height="8" rx="1.5" />
      <path d="M5.5 13h1M9 13h1M12.5 13h1M16 13h1M7 3.5h10M14.5 1.5 17 3.5l-2.5 2M9.5 6.5 7 4.5" />
    </>
  ),
  router: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v10M7 12h10M12 7l-2 2M12 7l2 2M12 17l-2-2M12 17l2-2M7 12l2-2M7 12l2 2M17 12l-2-2M17 12l-2 2" />
    </>
  ),
  firewall: (
    <>
      <path d="M12 2.5 4 5.5v6c0 5 3.4 8.6 8 10 4.6-1.4 8-5 8-10v-6z" />
      <path d="M4.5 10h15M8 10v5M16 10v5M12 15v5.5M6 15h12" />
    </>
  ),
  storage: (
    <>
      <ellipse cx="12" cy="5.5" rx="8" ry="3" />
      <path d="M4 5.5v13c0 1.7 3.6 3 8 3s8-1.3 8-3v-13M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3" />
    </>
  ),
  nas: (
    <>
      <rect x="4" y="2.5" width="16" height="19" rx="2" />
      <path d="M8 7h8M8 11h8M8 15h8M12 18.5h.01" />
    </>
  ),
  pdu: (
    <>
      <rect x="8" y="1.5" width="8" height="21" rx="1.5" />
      <path d="M11 5.5h2M11 9.5h2M11 13.5h2M11 17.5h2" />
    </>
  ),
  ups: (
    <>
      <rect x="2.5" y="7" width="17" height="10" rx="1.5" />
      <path d="M21.5 10.5v3M11.5 8.5l-2.5 4h4l-2.5 4" />
    </>
  ),
  patch_panel: (
    <>
      <rect x="2" y="8" width="20" height="8" rx="1" />
      <circle cx="6" cy="12" r="1" />
      <circle cx="10" cy="12" r="1" />
      <circle cx="14" cy="12" r="1" />
      <circle cx="18" cy="12" r="1" />
    </>
  ),
  sensor: <path d="M14 14.5V4a2 2 0 0 0-4 0v10.5a4 4 0 1 0 4 0zM12 9v8" />,
  kvm: (
    <>
      <rect x="2.5" y="3.5" width="19" height="12" rx="1.5" />
      <path d="M8 20.5h8M12 15.5v5" />
    </>
  ),
  device: (
    <>
      <rect x="3" y="6" width="18" height="12" rx="1.5" />
      <path d="M7 12h.01M11 12h6" />
    </>
  ),
  rack: (
    <>
      <rect x="5" y="2" width="14" height="20" rx="1" />
      <path d="M5 7h14M5 12h14M5 17h14M8 4.5h.01M8 9.5h.01M8 14.5h.01" />
    </>
  ),
  room: (
    <>
      <rect x="3" y="3" width="18" height="18" rx="1" />
      <path d="M3 9h18M3 15h18M9 3v18M15 3v18" />
    </>
  ),
  building: (
    <>
      <rect x="5" y="2.5" width="14" height="19" rx="1" />
      <path d="M9 6.5h.01M15 6.5h.01M9 10.5h.01M15 10.5h.01M9 14.5h.01M15 14.5h.01M10 21.5v-3h4v3" />
    </>
  ),
  site: (
    <>
      <path d="M12 21.5s-7-6.2-7-11.5a7 7 0 0 1 14 0c0 5.3-7 11.5-7 11.5z" />
      <circle cx="12" cy="10" r="2.5" />
    </>
  ),
  interface: (
    <>
      <path d="M4 5h16v11h-4v3H8v-3H4z" />
      <path d="M8 9v2M11 9v2M14 9v2M17 9v2" />
    </>
  ),
  psu: <path d="M13 2 4.5 13.5H12L11 22l8.5-11.5H12z" />,
  fan: (
    <>
      <circle cx="12" cy="12" r="9.5" />
      <circle cx="12" cy="12" r="1.5" />
      <path d="M12 10.5C11 7 12 4.5 14.5 4.5c1.5 2 .5 4.5-2.5 6M13.4 12.6c3.4.9 5.2 2.9 4.2 5.2-2.5.5-4.3-1.5-4.6-4.7M10.6 12.9c-2.5 2.5-5.2 3-6.4.9.8-2.4 3.6-2.9 6.2-1.6" />
    </>
  ),
  disk: (
    <>
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <circle cx="9" cy="12" r="3" />
      <path d="M16 15h2M9 12h.01" />
    </>
  ),
  memory: (
    <>
      <path d="M2.5 6.5h19v9h-19z" />
      <path d="M6 9.5v3M10 9.5v3M14 9.5v3M18 9.5v3M4.5 15.5v3M8 15.5v3M16 15.5v3M19.5 15.5v3" />
    </>
  ),
  cpu: (
    <>
      <rect x="5.5" y="5.5" width="13" height="13" rx="1.5" />
      <rect x="9" y="9" width="6" height="6" />
      <path d="M9.5 2v3.5M14.5 2v3.5M9.5 18.5V22M14.5 18.5V22M2 9.5h3.5M2 14.5h3.5M18.5 9.5H22M18.5 14.5H22" />
    </>
  ),
  module: (
    <>
      <rect x="2.5" y="4" width="19" height="12" rx="1" />
      <path d="M6 16v4M18 16v4M6 20h12M6.5 8h5v4h-5zM14.5 8h3M14.5 11h3" />
    </>
  ),
  transceiver: (
    <>
      <rect x="2.5" y="8" width="13" height="8" rx="1" />
      <path d="M15.5 10h4l2 2-2 2h-4M6 12h5" />
    </>
  ),
  k8s: (
    <>
      <path d="M12 2.5 20.2 6.5 22 15.5 16.3 22H7.7L2 15.5 3.8 6.5z" />
      <circle cx="12" cy="12.5" r="3" />
      <path d="M12 6v3.5M12 15.5V19M6 10.8l3.2 1M14.8 13.2l3.2 1M8.2 17.5l2-2.4M13.8 9.9l2-2.4" />
    </>
  ),
  vm: (
    <>
      <rect x="7" y="7" width="14" height="12" rx="1.5" />
      <path d="M17 7V4.5A1.5 1.5 0 0 0 15.5 3h-11A1.5 1.5 0 0 0 3 4.5v9A1.5 1.5 0 0 0 4.5 15H7" />
    </>
  ),
  network: (
    <>
      <circle cx="12" cy="4.5" r="2" />
      <circle cx="4.5" cy="19" r="2" />
      <circle cx="19.5" cy="19" r="2" />
      <path d="M12 6.5V12M12 12l-6.3 5.5M12 12l6.3 5.5" />
    </>
  ),
  cable: <path d="M4 3v4a2 2 0 0 0 2 2h1a2 2 0 0 1 2 2v2a2 2 0 0 0 2 2h2a2 2 0 0 1 2 2v1a2 2 0 0 0 2 2h3M2 3h4M18 22v-3" />,
  application: (
    <>
      <path d="M12 2.5 21.5 7.5 12 12.5 2.5 7.5z" />
      <path d="M2.5 12 12 17l9.5-5M2.5 16.5 12 21.5l9.5-5" />
    </>
  ),
  person: (
    <>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21.5c0-4.4 3.6-7 8-7s8 2.6 8 7" />
    </>
  ),
  document: (
    <>
      <path d="M14 2.5H6.5a2 2 0 0 0-2 2v15a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V8z" />
      <path d="M14 2.5V8h5.5M8.5 13h7M8.5 17h5" />
    </>
  ),
  object: (
    <>
      <path d="M12 2.5 21 7v10l-9 4.5L3 17V7z" />
      <path d="M3 7l9 4.5L21 7M12 11.5v10" />
    </>
  ),
  /* Navigation. */
  overview: (
    <>
      <rect x="3" y="3" width="7.5" height="9" rx="1" />
      <rect x="13.5" y="3" width="7.5" height="5" rx="1" />
      <rect x="13.5" y="11" width="7.5" height="10" rx="1" />
      <rect x="3" y="15" width="7.5" height="6" rx="1" />
    </>
  ),
  health: <path d="M2.5 12h4l3-8 5 16 3-8h4" />,
  collector: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.6 5.6l2.1 2.1M16.3 16.3l2.1 2.1M18.4 5.6l-2.1 2.1M7.7 16.3l-2.1 2.1" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="7" />
      <path d="m20.5 20.5-5-5" />
    </>
  ),
  topology: (
    <>
      <circle cx="5.5" cy="12" r="2.5" />
      <circle cx="18.5" cy="5.5" r="2.5" />
      <circle cx="18.5" cy="18.5" r="2.5" />
      <path d="M7.8 10.9l8.4-4.2M7.8 13.1l8.4 4.2" />
    </>
  ),
  discrepancy: (
    <>
      <path d="M10.3 3.6 1.9 18a2 2 0 0 0 1.7 3h16.8a2 2 0 0 0 1.7-3L13.7 3.6a2 2 0 0 0-3.4 0z" />
      <path d="M12 9v4.5M12 17.5h.01" />
    </>
  ),
  proposal: (
    <>
      <path d="M21.5 12.5h-6l-2 3h-3l-2-3h-6" />
      <path d="M5.5 5.6 2.5 12.5v6a2 2 0 0 0 2 2h15a2 2 0 0 0 2-2v-6l-3-6.9A2 2 0 0 0 16.7 4.5H7.3a2 2 0 0 0-1.8 1.1z" />
    </>
  ),
  collapse: (
    <>
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M9 3v18M16 9l-3 3 3 3" />
    </>
  ),
  expand: (
    <>
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M9 3v18M13 9l3 3-3 3" />
    </>
  ),
  signOut: <path d="M9 21.5H5a2 2 0 0 1-2-2v-15a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" />,
  close: <path d="M5.5 5.5l13 13M18.5 5.5l-13 13" />,
  inbox: <path d="M3 12h5l2 3h4l2-3h5M3 12v6.5a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V12l-3-7.5H6z" />,
} satisfies Record<string, ReactElement>;

export type IconName = keyof typeof PATHS;

export function Icon({ name, className }: { name: IconName; className?: string }) {
  return (
    <svg className={className ? `icon ${className}` : "icon"} viewBox="0 0 24 24" aria-hidden="true" focusable="false" data-icon={name}>
      {PATHS[name]}
    </svg>
  );
}

export const ICON_NAMES = Object.keys(PATHS) as IconName[];
