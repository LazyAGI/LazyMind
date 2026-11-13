import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from '@/modules/knowledge/utils/imageUrl';
import { extractHtmlFromArtifact } from './exportHtmlToPptx';
import {
  RASTER_EXPORT_PIXEL_RATIO,
  ensureRasterPng,
  fingerprintArtifact,
  fingerprintText,
  getRasterPng,
  loadRasterPng,
  rasterCacheKey,
  setRasterPng,
} from './slideRasterCache';

function isSpaFallbackHtml(text: string): boolean {
  const lower = text.slice(0, 400).toLowerCase();
  return lower.includes('<div id="root"') || lower.includes('id="app"');
}

async function loadArtifactText(raw: unknown): Promise<string> {
  if (raw == null) return '';
  if (typeof raw === 'string') return raw;
  if (typeof raw !== 'object') return String(raw);
  const obj = raw as Record<string, unknown>;
  if (typeof obj.text === 'string') return obj.text;
  if (obj.path && (obj.type === 'text' || obj.type === 'json' || !obj.type)) {
    const pathForSign = String(obj.path ?? obj.url ?? '').trim();
    if (!pathForSign) return '';
    const apiUrlRaw = obj.url ? String(obj.url).trim() : '';
    const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
    const fetchUrl = apiUrl && !isExpiredSignedUrl(apiUrl)
      ? apiUrl
      : await resolveMarkdownImageUrlAsync(pathForSign);
    const response = await fetch(fetchUrl);
    if (!response.ok) throw new Error('failed to load slide artifact');
    const text = await response.text();
    if (isSpaFallbackHtml(text)) throw new Error('invalid artifact content');
    return text;
  }
  return '';
}

/** Alias key: artifact meta + revision (invalidates on regenerate even if path reused). */
function aliasKey(sessionId: string, slot: SlotRevision): string {
  return rasterCacheKey(
    sessionId,
    `alias:${slot.slot_id || slot.slot}:${slot.list_index ?? 0}:${fingerprintArtifact(slot.artifact_value)}|r${slot.revision ?? 0}|d${slot.draft_version ?? 0}`,
  );
}

/**
 * Filmstrip thumb: capture export-quality PNG once (global serial queue), show
 * static <img>. Shared cache with raster PPTX export. Edit / regenerate bumps
 * revision or HTML fingerprint → replaces the shot. Reorder keeps the same PNG.
 */
export interface SlideThumbHandle { retry: () => void }

export const SlideThumb = forwardRef<SlideThumbHandle, { slot: SlotRevision; sessionId: string }>(function SlideThumb({
  slot,
  sessionId,
}, ref) {
  const { t } = useTranslation();
  const elementRef = useRef<HTMLDivElement>(null);
  const autoRetries = useRef(0);
  const [retry, setRetry] = useState(0);
  const aKey = aliasKey(sessionId, slot);
  const [previewSrc, setPreviewSrc] = useState<string | null>(() => getRasterPng(aKey));
  const [failed, setFailed] = useState(false);
  const genRef = useRef(0);

  useImperativeHandle(ref, () => ({ retry: () => {
    if (failed) setRetry(value => value + 1);
  } }), [failed]);

  useEffect(() => { autoRetries.current = 0; }, [aKey]);

  useEffect(() => {
    if (!failed || autoRetries.current >= 2) return;
    let scheduled = false;
    const recover = () => {
      if (scheduled || document.hidden || autoRetries.current >= 2) return;
      scheduled = true;
      autoRetries.current += 1;
      setRetry(value => value + 1);
    };
    // Intersection detects a cross-origin host hiding our iframe; visibility
    // events also recover when the entire browser tab returns to the foreground.
    let intersecting = typeof IntersectionObserver === 'undefined';
    const recoverIfVisible = () => { if (intersecting) recover(); };
    const observer = typeof IntersectionObserver === 'undefined' ? undefined : new IntersectionObserver(entries => {
      intersecting = entries.some(entry => entry.isIntersecting);
      recoverIfVisible();
    });
    if (elementRef.current) observer?.observe(elementRef.current);
    document.addEventListener('visibilitychange', recoverIfVisible);
    window.addEventListener('focus', recoverIfVisible);
    return () => {
      observer?.disconnect();
      document.removeEventListener('visibilitychange', recoverIfVisible);
      window.removeEventListener('focus', recoverIfVisible);
    };
  }, [failed]);

  useEffect(() => {
    const gen = ++genRef.current;
    setFailed(false);

    const aliased = getRasterPng(aKey);
    if (aliased) {
      setPreviewSrc(aliased);
      return;
    }
    setPreviewSrc(null);

    let cancelled = false;
    (async () => {
      const saved = await loadRasterPng(aKey);
      if (cancelled || gen !== genRef.current) return;
      if (saved) { setPreviewSrc(saved); return; }
      const text = await loadArtifactText(slot.artifact_value);
      if (cancelled || gen !== genRef.current) return;
      const html = extractHtmlFromArtifact(text) || extractHtmlFromArtifact(slot.artifact_value);
      if (!html) throw new Error('Slide artifact does not contain HTML');

      const contentKey = rasterCacheKey(sessionId, fingerprintText(html));
      const png = await ensureRasterPng(contentKey, html);
      // Index under alias so the next render hits before re-fetching HTML.
      await setRasterPng(aKey, png, RASTER_EXPORT_PIXEL_RATIO);
      if (cancelled || gen !== genRef.current) return;
      setPreviewSrc(png);
    })().catch(error => {
      if (cancelled || gen !== genRef.current) return;
      console.warn('[workflow] slide thumbnail failed', {
        sessionId, slot: slot.slot, listIndex: slot.list_index,
        message: error instanceof Error ? error.message : 'Slide capture failed',
      });
      setFailed(true);
    });

    return () => {
      cancelled = true;
    };
  }, [aKey, retry, sessionId, slot.artifact_value, slot.slot, slot.list_index, slot.sort_order]);

  if (previewSrc) {
    return (
      <div ref={elementRef} className='slide-thumb slide-thumb--shot'>
        <img className='slide-thumb__img' src={previewSrc} alt='' draggable={false} />
      </div>
    );
  }

  return (
    <div ref={elementRef} className='slide-thumb slide-thumb--pending' title={failed ? t('chat.workflowThumbnailFailed') : undefined}>
      <div className='slide-thumb__body'>
        <div className='slide-thumb__title'>{failed ? t('chat.workflowRetry') : '…'}</div>
      </div>
    </div>
  );
});
