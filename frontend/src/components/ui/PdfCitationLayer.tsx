import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { citationAtPoint, citationForPointerActivation, citationOccurrences, type PdfCitationTarget } from "./pdfCitationReferences";
import type { createPdfCitationPreviewCache, PdfCitationPreview, PdfCitationPreviewDocument } from "./pdfCitationPreview";
import "./PdfCitationLayer.css";

interface Props {
  targets: PdfCitationTarget[];
  getDocument: () => PdfCitationPreviewDocument | undefined;
  previewCache: ReturnType<typeof createPdfCitationPreviewCache>;
  onNavigate: (target: PdfCitationTarget) => void;
  sourceKey?: unknown;
}
type Occurrence = ReturnType<typeof citationOccurrences>[number];

function pageLocalOccurrences(page: HTMLElement, targets: PdfCitationTarget[]): Occurrence[] {
  const pageRect = page.getBoundingClientRect();
  return citationOccurrences(page, targets).map(hit => ({
    target: hit.target,
    rects: hit.rects.map(rect => ({
      left: rect.left - pageRect.left,
      right: rect.right - pageRect.left,
      top: rect.top - pageRect.top,
      bottom: rect.bottom - pageRect.top,
      width: rect.width,
      height: rect.height,
    } as DOMRect)),
  }));
}

export function PdfCitationLayer({ targets, getDocument, previewCache, onNavigate, sourceKey }: Props) {
  const { t } = useTranslation();
  const root = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const opener = useRef<{ token: number; element: HTMLButtonElement }>();
  const restoringFocus = useRef(false);
  const [active, setActive] = useState<PdfCitationTarget>();
  const [preview, setPreview] = useState<PdfCitationPreview>();
  const [loading, setLoading] = useState(false);
  const [position, setPosition] = useState({ left: 8, top: 8 });
  const [occurrences, setOccurrences] = useState<Occurrence[]>([]);
  const openTimer = useRef<ReturnType<typeof setTimeout>>();
  const closeTimer = useRef<ReturnType<typeof setTimeout>>();
  const request = useRef(0);
  const pointerInCard = useRef(false);
  const cancelClose = useCallback(() => clearTimeout(closeTimer.current), []);
  const close = useCallback(() => {
    clearTimeout(openTimer.current);
    clearTimeout(closeTimer.current);
    request.current++;
    opener.current = undefined;
    pointerInCard.current = false;
    setActive(undefined);
    setPreview(undefined);
    setLoading(false);
  }, []);
  const scheduleClose = useCallback(() => {
    clearTimeout(openTimer.current);
    cancelClose();
    if (!pointerInCard.current && !card.current?.contains(document.activeElement)) {
      closeTimer.current = setTimeout(close, 250);
    }
  }, [cancelClose, close]);
  const open = useCallback((target: PdfCitationTarget, x: number, y: number, keyboardOpener?: HTMLButtonElement) => {
    cancelClose();
    const token = ++request.current;
    opener.current = keyboardOpener ? { token, element: keyboardOpener } : undefined;
    setActive(target);
    setPreview(undefined);
    setPosition({ left: Math.max(8, Math.min(x, window.innerWidth - 392)), top: Math.max(8, Math.min(y + 12, window.innerHeight - 320)) });
    const pdf = getDocument();
    setLoading(!!pdf && !!target.regions.length);
    if (!pdf) return;
    void previewCache.get(pdf, target).then(value => {
      if (request.current === token) { setPreview(value); setLoading(false); }
    }, () => {
      if (request.current === token) setLoading(false);
    });
  }, [cancelClose, getDocument, previewCache]);
  const navigate = useCallback((target: PdfCitationTarget) => { close(); onNavigate(target); }, [close, onNavigate]);
  const dismiss = useCallback(() => {
    const currentOpener = card.current && opener.current?.token === request.current ? opener.current.element : undefined;
    close();
    if (!currentOpener?.isConnected) return;
    restoringFocus.current = true;
    try {
      currentOpener.focus({ preventScroll: true });
    } finally {
      restoringFocus.current = false;
    }
  }, [close]);

  useEffect(() => {
    close();
    const page = root.current?.parentElement;
    if (!page) return;
    let hovered: PdfCitationTarget | undefined;
    let pointerStart: { x: number; y: number } | undefined;
    const targetsByKey = new Map(targets.map(target => [target.referenceKey, target]));
    const resolveOccurrences = () => setOccurrences(pageLocalOccurrences(page, targets));
    resolveOccurrences();
    const observer = new MutationObserver(mutations => {
      if (mutations.some(mutation => !root.current?.contains(mutation.target))) resolveOccurrences();
    });
    observer.observe(page, { childList: true, subtree: true, characterData: true });
    const resizeObserver = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(resolveOccurrences);
    resizeObserver?.observe(page);
    const move = (event: PointerEvent) => {
      // Annotation anchors often sit above the PDF text layer. Hover may look
      // through them to a known citation, while click remains native below.
      if (event.buttons || window.getSelection()?.toString() || (event.target as Element).closest("button,input,textarea,select")) { hovered = undefined; close(); return; }
      const nativeLink = event.target instanceof Element ? event.target.closest(".annotationLayer .linkAnnotation a[title]") : null;
      const nativeColor = nativeLink?.closest(".linkAnnotation") ? getComputedStyle(nativeLink.closest(".linkAnnotation")!).borderColor : "";
      const channels = nativeColor.match(/\d+(?:\.\d+)?/g)?.map(Number);
      const greenLink = channels && channels.length >= 3 && channels[1] - channels[0] >= 40 && channels[1] - channels[2] >= 40;
      const nativeKey = nativeLink?.getAttribute("title")?.trim();
      const nativeHit = greenLink && nativeKey && /^\d{1,4}$/.test(nativeKey) ? targetsByKey.get(String(Number(nativeKey))) : undefined;
      const hit = nativeHit || citationAtPoint(page, targets, event.clientX, event.clientY);
      if (!hit) { hovered = undefined; scheduleClose(); return; }
      cancelClose();
      if (hovered !== hit) {
        hovered = hit;
        clearTimeout(openTimer.current);
        openTimer.current = setTimeout(() => open(hit, event.clientX, event.clientY), 300);
      }
    };
    const down = (event: PointerEvent) => { pointerStart = { x: event.clientX, y: event.clientY }; };
    const click = (event: MouseEvent) => {
      const start = pointerStart;
      pointerStart = undefined;
      const hit = citationForPointerActivation(page, targets, event.target as Element, event, window.getSelection()?.toString() || "", start);
      if (hit) { event.preventDefault(); event.stopPropagation(); navigate(hit); }
    };
    const leave = () => { hovered = undefined; scheduleClose(); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { hovered = undefined; dismiss(); } };
    const scroll = (event: Event) => { if (!(event.target instanceof Node) || !card.current?.contains(event.target)) { hovered = undefined; close(); } };
    const resize = () => { hovered = undefined; close(); resolveOccurrences(); };
    const selection = () => { if (window.getSelection()?.toString() && !card.current?.contains(window.getSelection()?.anchorNode || null)) { hovered = undefined; close(); } };
    page.addEventListener("pointermove", move);
    page.addEventListener("pointerdown", down);
    page.addEventListener("pointerleave", leave);
    page.addEventListener("click", click);
    document.addEventListener("keydown", escape);
    document.addEventListener("selectionchange", selection);
    window.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", resize);
    return () => {
      request.current++;
      opener.current = undefined;
      clearTimeout(openTimer.current);
      cancelClose();
      observer.disconnect();
      resizeObserver?.disconnect();
      page.removeEventListener("pointermove", move);
      page.removeEventListener("pointerdown", down);
      page.removeEventListener("pointerleave", leave);
      page.removeEventListener("click", click);
      document.removeEventListener("keydown", escape);
      document.removeEventListener("selectionchange", selection);
      window.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", resize);
    };
  }, [targets, sourceKey, open, close, scheduleClose, cancelClose, navigate, dismiss]);

  return <div ref={root} className="pdf-citation-layer">
    {occurrences.map((hit, index) => {
      const rect = hit.rects[0];
      const resolve = () => {
        const page = root.current?.parentElement;
        if (!page) return;
        const current = citationOccurrences(page, targets);
        const occurrence = current[index];
        if (occurrence?.target !== hit.target) return;
        const box = occurrence.rects[0];
        const target = citationAtPoint(page, targets, (box.left + box.right) / 2, (box.top + box.bottom) / 2);
        setOccurrences(pageLocalOccurrences(page, targets));
        return target ? { target, rect: box } : undefined;
      };
      return <button key={`${hit.target.referenceKey}:${index}`} className="pdf-citation-trigger" style={{ left: rect.left, top: rect.top, width: rect.width, height: rect.height }} aria-label={t("pdfCitation.view", { number: hit.target.referenceKey })} aria-haspopup="dialog" aria-expanded={active === hit.target}
        onFocus={event => { if (restoringFocus.current) return; const current = resolve(); if (current) open(current.target, current.rect.left, current.rect.bottom, event.currentTarget); }}
        onBlur={event => { if (!card.current?.contains(event.relatedTarget as Node)) scheduleClose(); }}
        onKeyDown={event => {
          if (event.key === "Enter" || event.key === " ") { event.preventDefault(); const current = resolve(); if (current) navigate(current.target); }
          if (event.key === "ArrowDown") { event.preventDefault(); card.current?.querySelector("button")?.focus(); }
        }} onClick={() => { const current = resolve(); if (current) navigate(current.target); }}/>;
    })}
    {active && createPortal(<div ref={card} className="pdf-citation-card" role="dialog" aria-label={t("pdfCitation.title", { number: active.referenceKey })} style={position}
      onMouseUp={event => event.stopPropagation()}
      onPointerEnter={() => { pointerInCard.current = true; cancelClose(); }} onPointerLeave={() => { pointerInCard.current = false; scheduleClose(); }}
      onFocus={cancelClose} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node)) scheduleClose(); }}>
      <div className="pdf-citation-heading"><strong>{t("pdfCitation.title", { number: active.referenceKey })}</strong><button aria-label={t("pdfCitation.close")} onClick={dismiss}>×</button></div>
      <p className="pdf-citation-raw">{active.rawText}</p>
      {preview ? <img className="pdf-citation-preview" src={preview.url} width={preview.width} height={preview.height} alt={t("pdfCitation.image", { number: active.referenceKey })}/> : <div className="pdf-citation-placeholder" role="status">{t(loading ? "pdfCitation.loading" : "pdfCitation.unavailable")}</div>}
      <div className="pdf-citation-footer"><button onClick={() => navigate(active)}>{t("pdfCitation.jump", { number: active.referenceKey })}</button></div>
    </div>, document.body)}
  </div>;
}
