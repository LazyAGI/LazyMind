/**
 * Shared slide raster cache: capture HTML once (serial queue), reuse for
 * filmstrip thumbs and browser PPTX export. Keyed by content fingerprint so
 * regenerate / edit replaces the shot; reorder alone keeps the same PNG.
 */

import { captureHtmlSlidePng } from './exportHtmlToPptx';

export interface RasterCacheEntry {
  pngDataUrl: string;
  capturedAt: number;
  /** Export-quality pixel ratio used for this shot. */
  pixelRatio: number;
}

/** Export-quality capture so thumbs and PPTX share one shot. */
export const RASTER_EXPORT_PIXEL_RATIO = 2;

const cache = new Map<string, RasterCacheEntry>();
const inflight = new Map<string, Promise<string>>();
let captureQueue: Promise<void> = Promise.resolve();

function enqueueCapture<T>(task: () => Promise<T>): Promise<T> {
  const run = captureQueue.then(task, task);
  captureQueue = run.then(() => undefined, () => undefined);
  return run;
}

/** Fast non-crypto fingerprint for cache keys (HTML body or artifact meta). */
export function fingerprintText(text: string): string {
  let h = 2166136261;
  const n = text.length;
  // Hash every character: a small edit must invalidate the persisted shot.
  const step = 1;
  for (let i = 0; i < n; i += step) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  h ^= n;
  return `t${(h >>> 0).toString(36)}_n${n}`;
}

export function fingerprintArtifact(raw: unknown): string {
  if (raw == null) return 'nil';
  if (typeof raw === 'string') return fingerprintText(raw);
  if (typeof raw !== 'object') return fingerprintText(String(raw));
  const obj = raw as Record<string, unknown>;
  if (typeof obj.text === 'string') return fingerprintText(obj.text);
  if (obj.path) {
    const path = String(obj.path);
    const size = obj.size != null ? String(obj.size) : '';
    const rev = obj.revision != null ? String(obj.revision) : '';
    return fingerprintText(`path:${path}|${size}|${rev}`);
  }
  if (typeof obj.content === 'string') return fingerprintText(obj.content);
  try {
    return fingerprintText(JSON.stringify(obj));
  } catch {
    return 'obj';
  }
}

export function rasterCacheKey(sessionId: string, fingerprint: string): string {
  // Bump prefix when capture pipeline changes so stale/failed shots are not reused.
  return `${sessionId || '_'}::v7::${fingerprint}`;
}

export function getRasterPng(key: string): string | null {
  return cache.get(key)?.pngDataUrl ?? null;
}

// Cache Storage persists across refreshes and local service restarts. These
// synthetic same-origin URLs are cache keys only; no server route is requested.
const DISK_CACHE = 'lazymind-slide-thumbnails-v7';
const MAX_SAVED_SHOTS = 96;
function diskKey(key: string): string {
  return `${location.origin}/__lazymind_slide_thumbnails__/${encodeURIComponent(key)}`;
}

export async function loadRasterPng(key: string, pixelRatio = RASTER_EXPORT_PIXEL_RATIO): Promise<string | null> {
  const memory = cache.get(key);
  if (memory && memory.pixelRatio >= pixelRatio) return memory.pngDataUrl;
  try {
    const saved = await (await caches.open(DISK_CACHE)).match(diskKey(key));
    if (!saved) return null;
    const entry: RasterCacheEntry = await saved.json();
    if (typeof entry.pngDataUrl !== 'string' || !entry.pngDataUrl.startsWith('data:image/png;base64,')
        || !Number.isFinite(entry.pixelRatio) || entry.pixelRatio < pixelRatio) return null;
    cache.set(key, entry);
    return entry.pngDataUrl;
  } catch { return null; } // Unavailable/full browser storage must not block rendering.
}

export async function setRasterPng(key: string, pngDataUrl: string, pixelRatio = RASTER_EXPORT_PIXEL_RATIO): Promise<void> {
  const entry = { pngDataUrl, capturedAt: Date.now(), pixelRatio };
  cache.set(key, entry);
  try {
    const saved = await caches.open(DISK_CACHE);
    await saved.put(diskKey(key), new Response(JSON.stringify(entry), {
      headers: { 'Content-Type': 'application/json' },
    }));
    const keys = await saved.keys();
    await Promise.all(keys.slice(0, Math.max(0, keys.length - MAX_SAVED_SHOTS)).map(k => saved.delete(k)));
  } catch { /* The in-memory image remains usable when storage is unavailable. */ }
}

/** Drop one entry before forced re-capture, including its persistent copy. */
export async function invalidateRasterKey(key: string): Promise<void> {
  cache.delete(key);
  try { await (await caches.open(DISK_CACHE)).delete(diskKey(key)); } catch { /* optional cache */ }
}

/** Drop stored shots for a workflow session. */
export async function invalidateRasterSession(sessionId: string): Promise<void> {
  const prefix = `${sessionId || '_'}::`;
  for (const key of cache.keys()) if (key.startsWith(prefix)) cache.delete(key);
  try {
    const saved = await caches.open(DISK_CACHE);
    for (const request of await saved.keys()) {
      const key = decodeURIComponent(new URL(request.url).pathname.split('/').pop() || '');
      if (key.startsWith(prefix)) await saved.delete(request);
    }
  } catch { /* optional cache */ }
}

/**
 * Return cached PNG or capture once (serial). Concurrent callers for the same
 * key share one in-flight promise. New HTML fingerprint → new key → new shot
 * (edit / regenerate). Same HTML after reorder → cache hit.
 */
export async function ensureRasterPng(
  key: string,
  html: string,
  options?: { pixelRatio?: number; waitMs?: number; force?: boolean },
): Promise<string> {
  const pixelRatio = options?.pixelRatio ?? RASTER_EXPORT_PIXEL_RATIO;
  if (!options?.force) {
    const hit = cache.get(key);
    if (hit && hit.pixelRatio >= pixelRatio) return hit.pngDataUrl;
  }

  const existing = inflight.get(key);
  if (existing) return existing;

  const promise = enqueueCapture(async () => {
    if (options?.force) await invalidateRasterKey(key);
    else {
      const saved = await loadRasterPng(key, pixelRatio);
      if (saved) return saved;
    }
    const png = await captureHtmlSlidePng(html, {
      pixelRatio,
      waitMs: options?.waitMs ?? 320,
    });
    await setRasterPng(key, png, pixelRatio);
    return png;
  }).finally(() => {
    inflight.delete(key);
  });

  inflight.set(key, promise);
  return promise;
}
