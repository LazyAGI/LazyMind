import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { citationAtPoint, citationForPointerActivation, citationOccurrences, type PdfCitationTarget } from "./pdfCitationReferences";
import type { createPdfCitationPreviewCache, PdfCitationPreview, PdfCitationPreviewDocument } from "./pdfCitationPreview";
import type { PdfCitationRegion } from "./pdfReferenceGeometry";
import { createNativeCitationPageResolver, createNativeCitationResolver, type NativeCitationDocument } from "./pdfNativeCitationResolver";
import "./PdfCitationLayer.css";

interface Props {
  targets: PdfCitationTarget[];
  getDocument: () => PdfCitationPreviewDocument | undefined;
  previewCache: ReturnType<typeof createPdfCitationPreviewCache>;
  onNavigate: (target: PdfCitationTarget) => void;
  onAddToLibrary?: (target: PdfCitationTarget) => Promise<void>;
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

export function PdfCitationLayer({ targets, getDocument, previewCache, onNavigate, onAddToLibrary, sourceKey }: Props) {
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
  const [pendingReferences, setPendingReferences] = useState<Set<string>>(() => new Set());
  const pendingAdds = useRef(new Set<string>());
  const [addedReferences, setAddedReferences] = useState<Set<string>>(() => new Set());
  const openTimer = useRef<ReturnType<typeof setTimeout>>();
  const closeTimer = useRef<ReturnType<typeof setTimeout>>();
  const request = useRef(0);
  const previewRegion = useRef<{ referenceId: string; region: PdfCitationRegion }>();
  const pointerInCard = useRef(false);
  const cancelClose = useCallback(() => clearTimeout(closeTimer.current), []);
  const close = useCallback(() => {
    clearTimeout(openTimer.current);
    clearTimeout(closeTimer.current);
    request.current++;
    previewRegion.current = undefined;
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
  const open = useCallback((target: PdfCitationTarget, x: number, y: number, keyboardOpener?: HTMLButtonElement, destinationPage?: number) => {
    cancelClose();
    const token = ++request.current;
    const openedTarget = destinationPage ? { ...target, fallbackPage: destinationPage } : target;
    previewRegion.current = undefined;
    opener.current = keyboardOpener ? { token, element: keyboardOpener } : undefined;
    setActive(openedTarget);
    setPreview(undefined);
    setPosition({ left: Math.max(8, Math.min(x, window.innerWidth - 392)), top: Math.max(8, Math.min(y + 12, window.innerHeight - 320)) });
    const pdf = getDocument();
    // A missing stored bbox can still be recovered from the PDF text layer.
    setLoading(!!pdf);
    if (!pdf) return;
    void previewCache.get(pdf, openedTarget).then(value => {
      if (request.current === token) {
        previewRegion.current = value?.region ? { referenceId: target.referenceId, region: value.region } : undefined;
        setPreview(value);
        setLoading(false);
      }
    }, () => {
      if (request.current === token) setLoading(false);
    });
  }, [cancelClose, getDocument, previewCache]);
  const navigate = useCallback((target: PdfCitationTarget) => {
    const region = previewRegion.current?.referenceId === target.referenceId ? previewRegion.current.region : undefined;
    close();
    onNavigate(region ? { ...target, fallbackPage: region.page, regions: [region] } : target);
  }, [close, onNavigate]);
  const addToLibrary = useCallback(async (target: PdfCitationTarget) => {
    if (!onAddToLibrary || pendingAdds.current.has(target.referenceId) || addedReferences.has(target.referenceId)) return;
    pendingAdds.current.add(target.referenceId);
    setPendingReferences(previous => new Set(previous).add(target.referenceId));
    try {
      await onAddToLibrary(target);
      setAddedReferences(previous => new Set(previous).add(target.referenceId));
    } catch {
      // The caller reports the server error; keep this action available to retry.
    } finally {
      pendingAdds.current.delete(target.referenceId);
      setPendingReferences(previous => {
        const next = new Set(previous);
        next.delete(target.referenceId);
        return next;
      });
    }
  }, [onAddToLibrary, addedReferences]);
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
    let alive = true;
    let hovered: PdfCitationTarget | string | undefined;
    let pointerStart: { x: number; y: number } | undefined;
    const targetsByKey = new Map(targets.map(target => [target.referenceKey, target]));
    const pageIndex = Number(page.closest<HTMLElement>("[data-pdf-page-index]")?.dataset.pdfPageIndex);
    const pdf = getDocument() as (PdfCitationPreviewDocument & Partial<NativeCitationDocument>) | undefined;
    const resolveNative = Number.isInteger(pageIndex) && pageIndex >= 0
      && pdf && typeof pdf.getDestination === "function" && typeof pdf.getPageIndex === "function"
      ? createNativeCitationResolver(pdf as NativeCitationDocument, pageIndex + 1, targets)
      : undefined;
    let resolveNativePage: ReturnType<typeof createNativeCitationPageResolver> | undefined;
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
      const nativeId = greenLink && nativeLink?.getAttribute("data-element-id");
      if (nativeHit && !nativeHit.regions.length && nativeId && resolveNative) {
        cancelClose();
        if (hovered !== nativeHit) {
          hovered = nativeHit;
          clearTimeout(openTimer.current);
          const { clientX, clientY } = event;
          openTimer.current = setTimeout(() => {
            resolveNativePage ??= createNativeCitationPageResolver(pdf as NativeCitationDocument, pageIndex + 1);
            void resolveNativePage(nativeId).then(destinationPage => {
              if (alive && hovered === nativeHit) open(nativeHit, clientX, clientY, undefined, destinationPage);
            });
          }, 300);
        }
        return;
      }
      if (!hit && nativeId && resolveNative) {
        if (hovered !== nativeId) {
          close();
          hovered = nativeId;
          const { clientX, clientY } = event;
          openTimer.current = setTimeout(() => {
            void resolveNative(nativeId).then(target => {
              if (!alive || hovered !== nativeId) return;
              if (target) open(target, clientX, clientY);
              else scheduleClose();
            });
          }, 300);
        }
        return;
      }
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
      alive = false;
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

  const numbered = active && /^\d{1,4}$/.test(active.referenceKey) ? active.referenceKey : undefined;
  const canNavigate = Boolean(active && (active.regions.length || active.fallbackPage || preview?.region));
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
    {active && createPortal(<div ref={card} className="pdf-citation-card" role="dialog" aria-label={numbered ? t("pdfCitation.title", { number: numbered }) : t("pdfCitation.unnumberedTitle")} style={position}
      onMouseUp={event => event.stopPropagation()}
      onPointerEnter={() => { pointerInCard.current = true; cancelClose(); }} onPointerLeave={() => { pointerInCard.current = false; scheduleClose(); }}
      onFocus={cancelClose} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node)) scheduleClose(); }}>
      <div className="pdf-citation-heading"><strong>{numbered ? t("pdfCitation.title", { number: numbered }) : t("pdfCitation.unnumberedTitle")}</strong><button aria-label={t("pdfCitation.close")} onClick={dismiss}>×</button></div>
      <p className="pdf-citation-raw">{active.rawText}</p>
      {preview ? <img className="pdf-citation-preview" src={preview.url} width={preview.width} height={preview.height} alt={numbered ? t("pdfCitation.image", { number: numbered }) : t("pdfCitation.unnumberedImage")}/> : <div className="pdf-citation-placeholder" role="status">{t(loading ? "pdfCitation.loading" : "pdfCitation.unavailable")}</div>}
      <div className="pdf-citation-footer">
        {onAddToLibrary && <button type="button" className="pdf-citation-add" disabled={pendingReferences.has(active.referenceId) || addedReferences.has(active.referenceId)} onClick={event => { event.stopPropagation(); void addToLibrary(active); }}>{t(addedReferences.has(active.referenceId) ? "pdfCitation.addedToLibrary" : pendingReferences.has(active.referenceId) ? "pdfCitation.addingToLibrary" : "pdfCitation.addToLibrary")}</button>}
        <button type="button" disabled={!canNavigate} onClick={() => navigate(active)}>{numbered ? t("pdfCitation.jump", { number: numbered }) : t("pdfCitation.unnumberedJump")}</button>
      </div>
    </div>, document.body)}
  </div>;
}
