// Face images of device models (GET /api/v1/device-types/{slug}/{face}).
//
// The API wants the bearer token, which an <image href> cannot send, and the
// Content-Security-Policy allows images from 'self' only: a blob: or data: URL
// of the bytes would be refused. So the page fetches the bytes with the token,
// decodes them into an ImageBitmap (no URL at all), and paints the bitmap on a
// canvas: on screen inside the rack's SVG, and on the export sheet. One fetch
// per image for the life of the page; the browser's HTTP cache (private, one
// hour, ETag) covers reloads.

import { useEffect, useState } from "react";
import { API_BASE } from "../api/client";
import { getToken } from "../api/token";

/** The only URLs this module fetches: the token goes nowhere else. */
const IMAGE_PATH = /^\/api\/v1\/device-types\/[a-z0-9][a-z0-9._-]*\/(front|rear)$/;

export const isFaceImagePath = (url: string): boolean => IMAGE_PATH.test(url) && url.startsWith(API_BASE + "/");

const cache = new Map<string, Promise<ImageBitmap | null>>();

/**
 * The decoded image, or null when it cannot be had (no such image, a network
 * failure, a browser without createImageBitmap): the caller then draws the
 * generic face. A failure is not kept, so a later render tries again.
 */
export function loadFaceImage(url: string): Promise<ImageBitmap | null> {
  if (!isFaceImagePath(url) || typeof createImageBitmap !== "function") return Promise.resolve(null);
  let p = cache.get(url);
  if (!p) {
    p = fetchBitmap(url);
    cache.set(url, p);
    void p.then((b) => {
      if (!b) cache.delete(url);
    });
  }
  return p;
}

async function fetchBitmap(url: string): Promise<ImageBitmap | null> {
  const headers: Record<string, string> = { Accept: "image/png, image/jpeg, image/webp" };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  try {
    const res = await fetch(url, { headers, credentials: "omit" });
    if (!res.ok) return null;
    return await createImageBitmap(await res.blob());
  } catch {
    return null;
  }
}

/** Every image of the list, decoded, for the export: url -> bitmap, missing ones left out. */
export async function loadFaceImages(urls: Iterable<string>): Promise<Map<string, ImageBitmap>> {
  const unique = [...new Set(urls)];
  const out = new Map<string, ImageBitmap>();
  const bitmaps = await Promise.all(unique.map((u) => loadFaceImage(u)));
  unique.forEach((u, i) => {
    const b = bitmaps[i];
    if (b) out.set(u, b);
  });
  return out;
}

/** The bitmap of url once it has loaded; null before, on failure, or without url. */
export function useFaceImage(url: string | undefined): ImageBitmap | null {
  const [state, setState] = useState<{ url?: string; bitmap: ImageBitmap | null }>({ bitmap: null });
  useEffect(() => {
    if (!url) return;
    let live = true;
    void loadFaceImage(url).then((bitmap) => {
      if (live) setState({ url, bitmap });
    });
    return () => {
      live = false;
    };
  }, [url]);
  return url && state.url === url ? state.bitmap : null;
}

/** Tests only: forget what was loaded. */
export function clearFaceImages(): void {
  cache.clear();
}
