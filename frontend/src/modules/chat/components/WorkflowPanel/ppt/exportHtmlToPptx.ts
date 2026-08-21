import { toPng } from 'html-to-image';
import PptxGenJS from 'pptxgenjs';
import { jsPDF } from 'jspdf';

const SLIDE_W_PX = 1600;
const SLIDE_H_PX = 900;
const SLIDE_W_IN = 13.333;
const SLIDE_H_IN = 7.5;

export interface HtmlSlideInput {
  html: string;
  notes?: string;
  pageNo?: number;
}

function looksLikeHtmlDocument(value: string): boolean {
  const s = value.trim().toLowerCase();
  return s.includes('<html') || (s.includes('<!doctype html') && s.includes('<body'));
}

export function extractHtmlFromArtifact(value: unknown): string | null {
  if (value == null) return null;
  const raw =
    typeof value === 'string'
      ? value
      : typeof value === 'object' && typeof (value as { text?: unknown }).text === 'string'
        ? String((value as { text: string }).text)
        : null;
  if (!raw || !looksLikeHtmlDocument(raw)) return null;
  // Strip model think traces / markdown fences before export or preview.
  let s = raw.replace(/<think\b[^>]*>[\s\S]*?<\/think>/gi, '').trim();
  s = s.replace(/^<think\b[^>]*>[\s\S]*?(?=<!doctype|<html\b|```)/i, '').trim();
  const fence = s.match(/```(?:html)?\s*\n([\s\S]*?)```/i);
  if (fence) s = fence[1].trim();
  const doc = s.match(/(<!doctype\s+html\b[\s\S]*?<\/html>|<html\b[\s\S]*?<\/html>)/i);
  return doc ? doc[1].trim() : (looksLikeHtmlDocument(s) ? s : null);
}

export function isHtmlSlideArtifact(value: unknown): boolean {
  return extractHtmlFromArtifact(value) != null;
}

/** Kill CSS motion so clicking a thumb does not replay entrance sweeps. */
const PREVIEW_STATIC_STYLE =
  '<style data-lazymind-preview-static>' +
  '*,*::before,*::after{' +
  'animation:none!important;' +
  'animation-delay:0s!important;' +
  'animation-duration:0s!important;' +
  'transition:none!important;' +
  'scroll-behavior:auto!important;' +
  '}' +
  'html,body,body *{cursor:pointer!important;}' +
  '</style>';

/** Appended at end of body so CSS wins over late page <style> blocks. */
const PREVIEW_STATIC_TAIL =
  '<style data-lazymind-preview-static-tail>' +
  '*,*::before,*::after{' +
  'animation:none!important;' +
  'animation-duration:0s!important;' +
  'transition:none!important;' +
  '}' +
  'html,body,body *{cursor:pointer!important;}' +
  '</style>';

export function htmlForStaticPreview(html: string): string {
  if (!html) return html;
  let s = html;
  if (!s.includes('data-lazymind-preview-static')) {
    if (/<\/head>/i.test(s)) {
      s = s.replace(/<\/head>/i, `${PREVIEW_STATIC_STYLE}</head>`);
    } else if (/<html\b[^>]*>/i.test(s)) {
      s = s.replace(/<html\b[^>]*>/i, (m) => `${m}<head>${PREVIEW_STATIC_STYLE}</head>`);
    } else {
      s = `${PREVIEW_STATIC_STYLE}${s}`;
    }
  }
  if (!s.includes('data-lazymind-preview-static-tail')) {
    if (/<\/body>/i.test(s)) {
      s = s.replace(/<\/body>/i, `${PREVIEW_STATIC_TAIL}</body>`);
    } else if (/<\/html>/i.test(s)) {
      s = s.replace(/<\/html>/i, `${PREVIEW_STATIC_TAIL}</html>`);
    } else {
      s = `${s}${PREVIEW_STATIC_TAIL}`;
    }
  }
  return s;
}

/**
 * html-to-image hangs / fails on Google Fonts @import, cross-origin CSS,
 * broken relative images (../images/…), and echarts script 404s.
 */
const RASTER_SAFE_STYLE =
  '<style data-lazymind-raster-safe>' +
  '*,*::before,*::after{' +
  'animation:none!important;' +
  'transition:none!important;' +
  '}' +
  'html,body,.wrapper{' +
  "font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Noto Sans SC'," +
  "'PingFang SC','Microsoft YaHei',sans-serif!important;" +
  '}' +
  'img{object-fit:cover;}' +
  '</style>';

/** 1×1 transparent GIF — used when relative/remote images 404 during capture. */
const TRANSPARENT_PIXEL =
  'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7';

/** Prepare slide HTML for html-to-image (static fonts/images; keep chart scripts). */
export function htmlForRasterCapture(html: string): string {
  let s = htmlForStaticPreview(html);
  // Drop @import (esp. fonts.googleapis) — common hang cause for toPng.
  s = s.replace(/@import\s+(?:url\([^)]*\)|["'][^"']+["'])\s*;?/gi, '');
  // Drop external stylesheet links.
  s = s.replace(/<link\b[^>]*rel=["']stylesheet["'][^>]*>/gi, '');
  // Drop external script src (echarts path 404). Keep inlined data-lazymind-echarts
  // and page chart init <script> blocks so bars render before capture.
  s = s.replace(
    /<script\b(?![^>]*data-lazymind-echarts)[^>]*\bsrc=["'][^"']+["'][^>]*>\s*<\/script>/gi,
    '',
  );
  // Broken relative / remote images abort html-to-image — swap to transparent pixel.
  s = s.replace(
    /(<img\b[^>]*\bsrc=["'])(?!data:)([^"']+)(["'])/gi,
    `$1${TRANSPARENT_PIXEL}$3`,
  );
  s = s.replace(
    /url\(\s*(['"]?)(?!data:)(\.\.\/|https?:\/\/|\/)[^)'"]+\1\s*\)/gi,
    `url(${TRANSPARENT_PIXEL})`,
  );
  if (!s.includes('data-lazymind-raster-safe')) {
    if (/<\/head>/i.test(s)) {
      s = s.replace(/<\/head>/i, `${RASTER_SAFE_STYLE}</head>`);
    } else if (/<html\b[^>]*>/i.test(s)) {
      s = s.replace(/<html\b[^>]*>/i, (m) => `${m}<head>${RASTER_SAFE_STYLE}</head>`);
    } else {
      s = `${RASTER_SAFE_STYLE}${s}`;
    }
  }
  return s;
}

/** Normalize speaker notes into a single prose paragraph (no bullet lists). */
export function notesToParagraph(notes: string): string {
  const raw = (notes || '').trim();
  if (!raw) return '';
  const lines = raw
    .split(/\r?\n/)
    .map((line) => line.replace(/^\s*([-*•]|\d+[.)])\s+/, '').trim())
    .filter(Boolean);
  if (lines.length <= 1) return lines[0] || raw;
  return lines.join(' ');
}

function withTimeout<T>(promise: Promise<T>, ms: number, label: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms);
    promise.then(
      (v) => {
        window.clearTimeout(timer);
        resolve(v);
      },
      (err) => {
        window.clearTimeout(timer);
        reject(err);
      },
    );
  });
}

function loadHtmlIframe(
  html: string,
  options?: { allowScripts?: boolean },
): Promise<{ iframe: HTMLIFrameElement; doc: Document; wrapper: HTMLElement }> {
  return new Promise((resolve, reject) => {
    const iframe = document.createElement('iframe');
    // Raster capture: same-origin only (no scripts). Preview paths may enable scripts.
    iframe.setAttribute(
      'sandbox',
      options?.allowScripts ? 'allow-same-origin allow-scripts' : 'allow-same-origin',
    );
    iframe.style.cssText = 'position:fixed;left:-10000px;top:0;width:1600px;height:900px;border:0;opacity:0;pointer-events:none;';
    document.body.appendChild(iframe);

    const cleanupReject = (err: unknown) => {
      iframe.remove();
      const msg = err instanceof Error
        ? err.message
        : (typeof err === 'string' ? err : 'iframe load failed');
      reject(new Error(msg && !/^\[object\s+\w+\]$/i.test(msg) ? msg : 'iframe load failed'));
    };

    iframe.onload = () => {
      try {
        const doc = iframe.contentDocument;
        if (!doc) {
          cleanupReject(new Error('iframe document unavailable'));
          return;
        }
        const wrapper = (doc.querySelector('.wrapper') || doc.body) as HTMLElement;
        // Normalize canvas size so html-to-image has a stable box.
        if (wrapper && wrapper !== doc.body) {
          wrapper.style.width = `${SLIDE_W_PX}px`;
          wrapper.style.height = `${SLIDE_H_PX}px`;
          wrapper.style.overflow = 'hidden';
        }
        doc.body.style.margin = '0';
        doc.body.style.width = `${SLIDE_W_PX}px`;
        doc.body.style.height = `${SLIDE_H_PX}px`;
        doc.body.style.overflow = 'hidden';
        resolve({ iframe, doc, wrapper });
      } catch (err) {
        cleanupReject(err);
      }
    };
    iframe.onerror = () => cleanupReject(new Error('iframe failed to load slide HTML'));

    const doc = iframe.contentDocument;
    if (!doc) {
      cleanupReject(new Error('iframe document unavailable'));
      return;
    }
    doc.open();
    doc.write(html);
    doc.close();
  });
}

async function sleep(ms: number): Promise<void> {
  await new Promise((r) => setTimeout(r, ms));
}

async function waitForAnimationFrames(win: Window | null, frames = 2): Promise<void> {
  if (!win) {
    await sleep(32);
    return;
  }
  for (let i = 0; i < frames; i += 1) {
    await new Promise<void>((resolve) => {
      win.requestAnimationFrame(() => resolve());
    });
  }
}

function countChartContainers(doc: Document): number {
  return doc.querySelectorAll('[id^="chart_"]').length;
}

/** True when each chart_* has a painted SVG/canvas of non-trivial size. */
function chartsHavePainted(doc: Document): boolean {
  const charts = doc.querySelectorAll('[id^="chart_"]');
  if (!charts.length) return true;
  for (const el of Array.from(charts)) {
    const svg = el.querySelector('svg');
    const canvas = el.querySelector('canvas');
    if (!svg && !canvas) return false;
    const box = (svg || canvas)!.getBoundingClientRect();
    if (box.width < 8 || box.height < 8) return false;
    // Empty SVG shells often have no series paths yet.
    if (svg && svg.querySelectorAll('path, rect, circle, text').length < 1) return false;
  }
  return true;
}

function resizeAllCharts(win: Window & { echarts?: { getInstanceByDom: (el: Element) => { resize: () => void } | undefined } }): void {
  const echarts = win.echarts;
  if (!echarts?.getInstanceByDom) return;
  const doc = win.document;
  doc.querySelectorAll('[id^="chart_"]').forEach((el) => {
    try {
      echarts.getInstanceByDom(el)?.resize();
    } catch {
      // ignore
    }
  });
}

/**
 * Wait until the slide is fully ready for raster capture:
 * fonts (bounded) → echarts loaded → __pptxChartsReady / painted SVG → settle frames.
 */
async function waitForSlideReady(
  doc: Document,
  options?: { timeoutMs?: number; settleMs?: number },
): Promise<void> {
  const win = doc.defaultView as (Window & {
    echarts?: unknown;
    __pptxChartsReady?: number;
  }) | null;
  const timeoutMs = options?.timeoutMs ?? 8000;
  const settleMs = options?.settleMs ?? 200;
  const deadline = Date.now() + timeoutMs;
  const expectedCharts = countChartContainers(doc);

  // Fonts: never hang on remote @import.
  await Promise.race([
    (async () => {
      const fonts = (doc as Document & { fonts?: FontFaceSet }).fonts;
      if (fonts?.ready) {
        try {
          await fonts.ready;
        } catch {
          // ignore
        }
      }
    })(),
    sleep(400),
  ]);

  if (expectedCharts > 0 && win) {
    // 1) Wait until echarts global exists (script src rewrite).
    while (Date.now() < deadline) {
      if (win.echarts) break;
      await sleep(50);
    }
    // 2) Wait for page contract counter and/or painted chart DOM.
    while (Date.now() < deadline) {
      const readyCount = Number(win.__pptxChartsReady || 0);
      if (readyCount >= expectedCharts && chartsHavePainted(doc)) break;
      if (chartsHavePainted(doc) && readyCount > 0) break;
      // If setOption ran but SVG still empty, nudge resize.
      if (readyCount >= expectedCharts) {
        resizeAllCharts(win as Parameters<typeof resizeAllCharts>[0]);
      }
      await sleep(80);
    }
    // Final resize + paint after ready.
    resizeAllCharts(win as Parameters<typeof resizeAllCharts>[0]);
  }

  await waitForAnimationFrames(win, 2);
  await sleep(settleMs);
  // One more frame after settle so html-to-image clones the final SVG.
  await waitForAnimationFrames(win, 1);

  // Soft warning path: if charts still empty, wait a bit more once.
  if (expectedCharts > 0 && !chartsHavePainted(doc) && Date.now() < deadline) {
    resizeAllCharts(win as Parameters<typeof resizeAllCharts>[0]);
    await sleep(400);
    await waitForAnimationFrames(win, 2);
  }
}

function captureOptions(pixelRatio: number) {
  return {
    width: SLIDE_W_PX,
    height: SLIDE_H_PX,
    cacheBust: true,
    skipFonts: true,
    backgroundColor: '#0f172a',
    pixelRatio,
    imagePlaceholder: TRANSPARENT_PIXEL,
    // Broken imgs used to reject the whole capture as a DOM Event → "[object Event]".
    onImageErrorHandler: (() => undefined) as OnErrorEventHandler,
    style: {
      transform: 'none',
      margin: '0',
    },
  };
}

async function toPngSafe(
  wrapper: HTMLElement,
  options: { pixelRatio: number },
): Promise<string> {
  const run = (pixelRatio: number) =>
    withTimeout(toPng(wrapper, captureOptions(pixelRatio)), 20000, 'slide capture');

  try {
    return await run(options.pixelRatio);
  } catch (firstErr) {
    try {
      return await run(1);
    } catch {
      const msg = firstErr instanceof Error
        ? firstErr.message
        : (typeof firstErr === 'string' ? firstErr : 'slide capture failed');
      throw new Error(
        msg && !/^\[object\s+\w+\]$/i.test(msg)
          ? msg
          : 'slide capture failed (image/font embed)',
      );
    }
  }
}

/**
 * Capture a slide HTML document as a PNG data URL (thumbs + raster PPTX export).
 */
export async function captureHtmlSlidePng(
  html: string,
  options?: { pixelRatio?: number; waitMs?: number; timeoutMs?: number },
): Promise<string> {
  const pixelRatio = options?.pixelRatio ?? 2;
  // Rewrite echarts src before raster prep so chart_* containers can paint.
  const { htmlWithInlinedEcharts } = await import('./echartsInline');
  const withCharts = await htmlWithInlinedEcharts(html);
  const prepared = htmlForRasterCapture(withCharts);
  const needsJs = /data-lazymind-echarts=|echarts\.init|id=["']chart_/i.test(prepared);
  const { iframe, doc, wrapper } = await loadHtmlIframe(prepared, { allowScripts: needsJs });
  try {
    await waitForSlideReady(doc, {
      timeoutMs: options?.timeoutMs ?? (needsJs ? 8000 : 2000),
      settleMs: options?.waitMs ?? (needsJs ? 280 : 120),
    });
    return await toPngSafe(wrapper, { pixelRatio });
  } finally {
    iframe.remove();
  }
}

async function buildRasterSlideFromPng(
  pptx: PptxGenJS,
  pngDataUrl: string,
  notes?: string,
): Promise<void> {
  const slide = pptx.addSlide();
  slide.addImage({
    data: pngDataUrl,
    x: 0,
    y: 0,
    w: SLIDE_W_IN,
    h: SLIDE_H_IN,
  });
  const noteText = notesToParagraph(notes || '');
  if (noteText) {
    slide.addNotes(noteText);
  }
}

async function buildRasterSlideFromHtml(
  pptx: PptxGenJS,
  slideInput: HtmlSlideInput,
): Promise<void> {
  const png = await captureHtmlSlidePng(slideInput.html, { pixelRatio: 2, timeoutMs: 8000, waitMs: 280 });
  await buildRasterSlideFromPng(pptx, png, slideInput.notes);
}

/**
 * Browser raster export — each slide is a full-page screenshot image.
 * Default product path when LAZYMIND_OUTPUT_EDITABLE_PPT is off.
 *
 * Pass sessionId to reuse filmstrip captures from slideRasterCache (skips
 * re-screenshot when thumbs already ran). Missing cache entries are captured
 * on demand and stored for later.
 */
export async function exportHtmlSlidesAsRasterPptx(
  slides: HtmlSlideInput[],
  fileName = 'deck.pptx',
  options?: { sessionId?: string },
): Promise<void> {
  if (!slides.length) {
    throw new Error('No HTML slides to export');
  }
  const pptx = new PptxGenJS();
  pptx.defineLayout({ name: 'LAYOUT_16x9', width: SLIDE_W_IN, height: SLIDE_H_IN });
  pptx.layout = 'LAYOUT_16x9';

  const sessionId = options?.sessionId || '';
  // Lazy import avoids circular init with slideRasterCache → captureHtmlSlidePng.
  const cache = sessionId
    ? await import('./slideRasterCache')
    : null;

  for (const slide of slides) {
    let png: string | null = null;
    if (cache) {
      const key = cache.rasterCacheKey(sessionId, cache.fingerprintText(slide.html));
      png = await cache.ensureRasterPng(key, slide.html);
    }
    if (png) {
      await buildRasterSlideFromPng(pptx, png, slide.notes);
    } else {
      await buildRasterSlideFromHtml(pptx, slide);
    }
  }

  await pptx.writeFile({ fileName });
}

/** @deprecated Use exportHtmlSlidesAsRasterPptx — name was misleading. */
export const exportHtmlSlidesToEditablePptx = exportHtmlSlidesAsRasterPptx;

export async function exportHtmlSlidesAsRasterPdf(
  slides: HtmlSlideInput[],
  fileName = 'deck.pdf',
  options?: { sessionId?: string },
): Promise<void> {
  if (!slides.length) {
    throw new Error('No HTML slides to export');
  }
  let pdf: jsPDF | null = null;
  const sessionId = options?.sessionId || '';
  const cache = sessionId
    ? await import('./slideRasterCache')
    : null;

  for (const slide of slides) {
    let png: string | null = null;
    if (cache) {
      const key = cache.rasterCacheKey(sessionId, cache.fingerprintText(slide.html));
      png = await cache.ensureRasterPng(key, slide.html);
    }
    if (!png) {
      png = await captureHtmlSlidePng(slide.html, { pixelRatio: 2, timeoutMs: 8000, waitMs: 280 });
    }
    if (!pdf) {
      pdf = new jsPDF({
        orientation: 'landscape',
        unit: 'pt',
        format: [SLIDE_W_IN * 72, SLIDE_H_IN * 72],
      });
    } else {
      pdf.addPage([SLIDE_W_IN * 72, SLIDE_H_IN * 72], 'landscape');
    }
    pdf.addImage(png, 'PNG', 0, 0, SLIDE_W_IN * 72, SLIDE_H_IN * 72);
  }
  if (!pdf) {
    throw new Error('No slides available to export as PDF');
  }
  pdf.save(fileName.endsWith('.pdf') ? fileName : `${fileName}.pdf`);
}
