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
  // Sample head / mid / tail so large HTML stays cheap.
  const step = Math.max(1, Math.floor(n / 4096));
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
  return `${sessionId || '_'}::v4::${fingerprint}`;
}

export function getRasterPng(key: string): string | null {
  return cache.get(key)?.pngDataUrl ?? null;
}

export function setRasterPng(key: string, pngDataUrl: string, pixelRatio = RASTER_EXPORT_PIXEL_RATIO): void {
  cache.set(key, { pngDataUrl, capturedAt: Date.now(), pixelRatio });
}

/** Drop one entry (e.g. before forced re-capture). */
export function invalidateRasterKey(key: string): void {
  cache.delete(key);
  inflight.delete(key);
}

/** Drop all shots for a plugin session (session switch / close). */
export function invalidateRasterSession(sessionId: string): void {
  const prefix = `${sessionId || '_'}::`;
  for (const key of [...cache.keys()]) {
    if (key.startsWith(prefix)) cache.delete(key);
  }
  for (const key of [...inflight.keys()]) {
    if (key.startsWith(prefix)) inflight.delete(key);
  }
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
  } else {
    invalidateRasterKey(key);
  }

  const existing = inflight.get(key);
  if (existing) return existing;

  const promise = enqueueCapture(async () => {
    const png = await captureHtmlSlidePng(html, {
      pixelRatio,
      waitMs: options?.waitMs ?? 320,
    });
    setRasterPng(key, png, pixelRatio);
    return png;
  }).finally(() => {
    inflight.delete(key);
  });

  inflight.set(key, promise);
  return promise;
}
