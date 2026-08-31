import { useEffect, useRef, useState } from 'react';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from '@/modules/knowledge/utils/imageUrl';
import { extractHtmlFromArtifact } from './exportHtmlToPptx';
import {
  RASTER_EXPORT_PIXEL_RATIO,
  ensureRasterPng,
  fingerprintArtifact,
  fingerprintText,
  getRasterPng,
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
    `alias:${fingerprintArtifact(slot.artifact_value)}|r${slot.revision ?? 0}`,
  );
}

/**
 * Filmstrip thumb: capture export-quality PNG once (global serial queue), show
 * static <img>. Shared cache with raster PPTX export. Edit / regenerate bumps
 * revision or HTML fingerprint → replaces the shot. Reorder keeps the same PNG.
 */
export function SlideThumb({
  slot,
  sessionId,
}: {
  slot: SlotRevision;
  sessionId: string;
}) {
  const [previewSrc, setPreviewSrc] = useState<string | null>(() => getRasterPng(aliasKey(sessionId, slot)));
  const [failed, setFailed] = useState(false);
  const genRef = useRef(0);

  useEffect(() => {
    const gen = ++genRef.current;
    setFailed(false);

    const aKey = aliasKey(sessionId, slot);
    const aliased = getRasterPng(aKey);
    if (aliased) {
      setPreviewSrc(aliased);
      return;
    }
    setPreviewSrc(null);

    let cancelled = false;
    (async () => {
      const text = await loadArtifactText(slot.artifact_value);
      if (cancelled || gen !== genRef.current) return;
      const html = extractHtmlFromArtifact(text) || extractHtmlFromArtifact(slot.artifact_value);
      if (!html) {
        setFailed(true);
        return;
      }

      const contentKey = rasterCacheKey(sessionId, fingerprintText(html));
      const png = await ensureRasterPng(contentKey, html);
      // Index under alias so the next render hits before re-fetching HTML.
      setRasterPng(aKey, png, RASTER_EXPORT_PIXEL_RATIO);
      if (cancelled || gen !== genRef.current) return;
      setPreviewSrc(png);
    })().catch(() => {
      if (!cancelled && gen === genRef.current) setFailed(true);
    });

    return () => {
      cancelled = true;
    };
  }, [sessionId, slot.artifact_value, slot.slot_id, slot.revision, slot.sort_order]);

  if (previewSrc) {
    return (
      <div className='slide-thumb slide-thumb--shot'>
        <img className='slide-thumb__img' src={previewSrc} alt='' draggable={false} />
      </div>
    );
  }

  return (
    <div className='slide-thumb slide-thumb--pending'>
      <div className='slide-thumb__body'>
        <div className='slide-thumb__title'>{failed ? '—' : '…'}</div>
      </div>
    </div>
  );
}
