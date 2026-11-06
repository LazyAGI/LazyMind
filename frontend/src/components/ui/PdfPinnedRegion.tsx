import { useCallback, useEffect, useLayoutEffect, useRef, useState, type PointerEvent, type RefObject } from "react";
import { cropPdfBlockPreview } from "./pdfBlockPreview";
import type { PdfRegionPinRecord } from "./pdfPinStore";
import "./PdfPinnedRegion.css";

type Rect = { x: number; y: number; width: number; height: number };
type Bounds = { left: number; top: number; width: number; height: number };
type Handle = "move" | "nw" | "ne" | "sw" | "se";
type ResizeHandle = "n" | "ne" | "e" | "se" | "s" | "sw" | "w" | "nw";
type Gesture = { kind: "draw" | Handle; page: number; startX: number; startY: number; initial: Rect };
type PinGesture = { startX: number; startY: number; initial: Rect; handle?: ResizeHandle };

interface PdfPinnedRegionProps {
  stageRef: RefObject<HTMLDivElement>;
  viewportRef: RefObject<HTMLDivElement>;
  pageRefs: RefObject<Array<HTMLDivElement | null>>;
  sourceKey: unknown;
  active: boolean;
  onActiveChange: (active: boolean) => void;
  language?: "zh" | "en";
  pins?: PdfRegionPinRecord[];
  onAddPin?: (pin: PdfRegionPinRecord) => boolean|string;
  onUpdatePin?: (pin: PdfRegionPinRecord) => void;
  onRemovePin?: (id: string) => void;
  onMinimizePin?: (id: string) => void;
}

const MIN_REGION = 24;
const MIN_PIN_WIDTH = 160;
const SHADE = "rgba(33, 43, 57, .38)";

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

function pageBounds(page: HTMLElement | null, viewport: HTMLElement): Bounds | undefined {
  if (!page) return;
  const pageRect = page.getBoundingClientRect();
  const viewportRect = viewport.getBoundingClientRect();
  if (!pageRect.width || !pageRect.height) return;
  return {
    left: pageRect.left - viewportRect.left,
    top: pageRect.top - viewportRect.top,
    width: pageRect.width,
    height: pageRect.height,
  };
}

function drawingBounds(page: Bounds, viewport: Bounds): Bounds {
  const left = clamp(page.left, 0, viewport.width);
  const top = clamp(page.top, 0, viewport.height);
  return {
    left,
    top,
    width: Math.max(0, Math.min(page.left + page.width, viewport.width) - left),
    height: Math.max(0, Math.min(page.top + page.height, viewport.height) - top),
  };
}

function localPoint(event: PointerEvent, viewport: HTMLElement) {
  const bounds = viewport.getBoundingClientRect();
  return { x: event.clientX - bounds.left, y: event.clientY - bounds.top };
}

function drawRect(startX: number, startY: number, x: number, y: number): Rect {
  return { x: Math.min(startX, x), y: Math.min(startY, y), width: Math.abs(x - startX), height: Math.abs(y - startY) };
}

function cssNumber(value: string, fallback: number) {
  const number = Number.parseFloat(value);
  return Number.isFinite(number) ? number : fallback;
}

function panelGeometry(panel: HTMLDivElement, fallback: Rect): Rect {
  return {
    x: cssNumber(panel.style.left, fallback.x),
    y: cssNumber(panel.style.top, fallback.y),
    width: panel.offsetWidth || cssNumber(panel.style.width, fallback.width),
    height: panel.offsetHeight || cssNumber(panel.style.height, fallback.height),
  };
}

function constrainPanel(rect: Rect, viewport: Bounds): Rect {
  const margin = 8;
  const availableWidth = Math.max(48, viewport.width - margin * 2);
  const availableHeight = Math.max(48, viewport.height - margin * 2);
  const width = clamp(rect.width, Math.min(MIN_PIN_WIDTH, rect.width, availableWidth), availableWidth);
  const height = clamp(rect.height, Math.min(100, rect.height, availableHeight), availableHeight);
  return {
    x: clamp(rect.x, viewport.left + margin, viewport.left + viewport.width - width - margin),
    y: clamp(rect.y, viewport.top + margin, viewport.top + viewport.height - height - margin),
    width,
    height,
  };
}

function sameRect(a: Rect, b: Rect) {
  return a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
}

interface PinnedWindowProps {
  pin: PdfRegionPinRecord;
  viewport: Bounds;
  active: boolean;
  zh: boolean;
  onUpdate: (pin: PdfRegionPinRecord) => void;
  onRemove: (id: string) => void;
  onMinimize: (id: string) => void;
}

function PinnedWindow({ pin, viewport, active, zh, onUpdate, onRemove, onMinimize }: PinnedWindowProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const moving = useRef<PinGesture>();
  const resizing = useRef<PinGesture>();
  const updateRef = useRef(onUpdate);
  updateRef.current = onUpdate;

  const commitGeometry = () => {
    const panel = panelRef.current;
    if (!panel) return;
    const geometry = constrainPanel(panelGeometry(panel, pin.geometry), viewport);
    if (!sameRect(geometry, pin.geometry)) {
      updateRef.current({ ...pin, geometry });
    }
  };

  useLayoutEffect(() => {
    if (!panelRef.current || !viewport.width || !viewport.height) return;
    const geometry = constrainPanel(panelGeometry(panelRef.current, pin.geometry), viewport);
    if (!sameRect(geometry, pin.geometry)) {
      updateRef.current({ ...pin, geometry });
    }
  }, [viewport, pin]);

  const move = (event: PointerEvent<HTMLElement>) => {
    const gesture = moving.current;
    const panel = panelRef.current;
    if (!gesture || !panel) return;
    const { width, height, x, y } = gesture.initial;
    panel.style.left = `${clamp(x + event.clientX - gesture.startX, viewport.left + 8, viewport.left + viewport.width - width - 8)}px`;
    panel.style.top = `${clamp(y + event.clientY - gesture.startY, viewport.top + 8, viewport.top + viewport.height - height - 8)}px`;
  };

  const resize = (event: PointerEvent<HTMLElement>) => {
    const gesture = resizing.current;
    const panel = panelRef.current;
    if (!gesture?.handle || !panel) return;
    const { initial, handle } = gesture;
    const dx = event.clientX - gesture.startX;
    const dy = event.clientY - gesture.startY;
    const minWidth = Math.min(MIN_PIN_WIDTH, initial.width, Math.max(48, viewport.width - 16));
    const minHeight = Math.min(100, initial.height, Math.max(48, viewport.height - 16));
    const left = handle.includes("w") ? clamp(initial.x + dx, viewport.left + 8, initial.x + initial.width - minWidth) : initial.x;
    const right = handle.includes("e") ? clamp(initial.x + initial.width + dx, initial.x + minWidth, viewport.left + viewport.width - 8) : initial.x + initial.width;
    const top = handle.includes("n") ? clamp(initial.y + dy, viewport.top + 8, initial.y + initial.height - minHeight) : initial.y;
    const bottom = handle.includes("s") ? clamp(initial.y + initial.height + dy, initial.y + minHeight, viewport.top + viewport.height - 8) : initial.y + initial.height;
    panel.style.left = `${left}px`;
    panel.style.top = `${top}px`;
    panel.style.width = `${right - left}px`;
    panel.style.height = `${bottom - top}px`;
  };

  return <div
    ref={panelRef}
    className="pdf-pin-floating"
    role="region"
    aria-label={zh ? "钉住的 PDF 区域" : "Pinned PDF region"}
    aria-description={zh ? "方向键移动，Shift 加方向键调整大小" : "Use arrow keys to move; Shift plus arrow keys to resize"}
    aria-hidden={active}
    tabIndex={active ? -1 : 0}
    style={{ left: pin.geometry.x, top: pin.geometry.y, width: pin.geometry.width, height: pin.geometry.height, maxWidth: Math.max(48, viewport.width - 16), maxHeight: Math.max(48, viewport.height - 16), visibility: active ? "hidden" : "visible", pointerEvents: active ? "none" : "auto" }}
    onPointerDown={event => {
      if (event.button !== 0 || event.target instanceof Element && event.target.closest("button,[data-pin-resize]")) return;
      moving.current = { startX: event.clientX, startY: event.clientY, initial: panelGeometry(event.currentTarget, pin.geometry) };
      event.currentTarget.setPointerCapture?.(event.pointerId);
      event.preventDefault();
    }}
    onPointerMove={move}
    onPointerUp={() => { moving.current = undefined; commitGeometry(); }}
    onPointerCancel={() => { moving.current = undefined; commitGeometry(); }}
    onKeyDown={event => {
      if (event.target instanceof Element && event.target.closest("button")) return;
      const step = 12;
      const dx = event.key === "ArrowLeft" ? -step : event.key === "ArrowRight" ? step : 0;
      const dy = event.key === "ArrowUp" ? -step : event.key === "ArrowDown" ? step : 0;
      if (!dx && !dy) return;
      event.preventDefault();
      const geometry = panelRef.current ? panelGeometry(panelRef.current, pin.geometry) : pin.geometry;
      onUpdate({ ...pin, geometry: constrainPanel(event.shiftKey
        ? { ...geometry, width: geometry.width + dx, height: geometry.height + dy }
        : { ...geometry, x: geometry.x + dx, y: geometry.y + dy }, viewport) });
    }}
  >
    <div className="pdf-pin-floating-header">
      <span>{zh ? `钉住 · 第 ${pin.page} 页` : `Pinned · Page ${pin.page}`}</span>
      <div className="pdf-pin-floating-actions">
        <button type="button" aria-label={zh ? "最小化钉住区域" : "Minimize pinned region"} title={zh ? "收起到钉住内容" : "Minimize to pinned items"} onPointerDown={event => event.stopPropagation()} onClick={() => onMinimize(pin.id)}>−</button>
        <button type="button" aria-label={zh ? "关闭钉住区域" : "Close pinned region"} onPointerDown={event => event.stopPropagation()} onClick={() => onRemove(pin.id)}>×</button>
      </div>
    </div>
    <img src={pin.image} alt={zh ? `第 ${pin.page} 页的截取区域` : `Captured region from page ${pin.page}`} draggable={false} />
    {(["n", "ne", "e", "se", "s", "sw", "w", "nw"] as const).map(handle => <span key={handle} className={`pdf-pin-resize is-${handle}`} data-pin-resize={handle} aria-hidden="true"
      onPointerDown={event => { if (event.button !== 0 || !panelRef.current) return; event.stopPropagation(); resizing.current = { startX: event.clientX, startY: event.clientY, initial: panelGeometry(panelRef.current, pin.geometry), handle }; event.currentTarget.setPointerCapture?.(event.pointerId); event.preventDefault(); }}
      onPointerMove={resize}
      onPointerUp={event => { event.stopPropagation(); resizing.current = undefined; commitGeometry(); }}
      onPointerCancel={event => { event.stopPropagation(); resizing.current = undefined; commitGeometry(); }}
    />)}
  </div>;
}

/** The selector is viewport-local; only the saved crop is anchored to PDF page coordinates. */
export function PdfPinnedRegion({ stageRef, viewportRef, pageRefs, sourceKey, active, onActiveChange, language = "zh", pins, onAddPin, onUpdatePin, onRemovePin, onMinimizePin }: PdfPinnedRegionProps) {
  const zh = language === "zh";
  const rootRef = useRef<HTMLDivElement>(null);
  const gesture = useRef<Gesture>();
  const previousSource = useRef(sourceKey);
  const [viewport, setViewport] = useState<Bounds>({ left: 0, top: 0, width: 0, height: 0 });
  const [rect, setRect] = useState<Rect>();
  const [dragging, setDragging] = useState(false);
  const [selectedPage, setSelectedPage] = useState<number>();
  const [pin, setPin] = useState<PdfRegionPinRecord>();
  const [error, setError] = useState("");

  const measure = useCallback(() => {
    const stage = stageRef.current;
    const pane = viewportRef.current;
    if (!stage || !pane) return;
    const stageRect = stage.getBoundingClientRect();
    const paneRect = pane.getBoundingClientRect();
    const next = {
      left: Math.max(0, paneRect.left - stageRect.left),
      top: Math.max(0, paneRect.top - stageRect.top),
      width: Math.min(paneRect.width, stageRect.width),
      height: Math.min(paneRect.height, stageRect.height),
    };
    setViewport(previous => previous.left === next.left && previous.top === next.top && previous.width === next.width && previous.height === next.height ? previous : next);
  }, [stageRef, viewportRef]);

  useLayoutEffect(() => {
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    if (stageRef.current) observer?.observe(stageRef.current);
    if (viewportRef.current) observer?.observe(viewportRef.current);
    window.addEventListener("resize", measure);
    return () => { observer?.disconnect(); window.removeEventListener("resize", measure); };
  }, [measure, stageRef, viewportRef]);

  // On initial mount the parent stage ref can attach after this child's layout
  // effect. Measure again after commit so restored pins receive real bounds.
  useEffect(() => { measure(); }, [measure]);

  useEffect(() => {
    if (previousSource.current === sourceKey) return;
    previousSource.current = sourceKey;
    gesture.current = undefined;
    setDragging(false);
    setRect(undefined);
    setSelectedPage(undefined);
    setPin(undefined);
    setError("");
    onActiveChange(false);
  }, [sourceKey, onActiveChange]);

  useEffect(() => {
    if (!active) { gesture.current = undefined; setDragging(false); setRect(undefined); setSelectedPage(undefined); setError(""); return; }
    measure();
    rootRef.current?.focus({ preventScroll: true });
    const pane = viewportRef.current;
    const clearOnScroll = () => { gesture.current = undefined; setDragging(false); setRect(undefined); setSelectedPage(undefined); };
    pane?.addEventListener("scroll", clearOnScroll);
    return () => pane?.removeEventListener("scroll", clearOnScroll);
  }, [active, measure, viewportRef]);

  const finish = () => {
    gesture.current = undefined;
    setDragging(false);
    if (rect && (rect.width < MIN_REGION || rect.height < MIN_REGION)) {
      setRect(undefined);
      setSelectedPage(undefined);
    }
  };

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || !viewportRef.current || event.target instanceof Element && event.target.closest("button")) return;
    const point = localPoint(event, viewportRef.current);
    const target = event.target as HTMLElement;
    const handle = target.closest<HTMLElement>("[data-pin-handle]")?.dataset.pinHandle as Handle | undefined;
    const move = !!target.closest("[data-pin-selection]");
    if (rect && selectedPage !== undefined && (handle || move)) {
      gesture.current = { kind: handle || "move", page: selectedPage, startX: point.x, startY: point.y, initial: rect };
      setDragging(true);
      event.currentTarget.setPointerCapture?.(event.pointerId);
      event.preventDefault();
      return;
    }
    let page = -1;
    let visible: Bounds | undefined;
    for (let index = 0; index < (pageRefs.current?.length || 0); index++) {
      const bounds = pageBounds(pageRefs.current?.[index] || null, viewportRef.current);
      if (!bounds) continue;
      const clipped = drawingBounds(bounds, viewport);
      if (point.x >= clipped.left && point.x <= clipped.left + clipped.width && point.y >= clipped.top && point.y <= clipped.top + clipped.height) {
        page = index;
        visible = clipped;
        break;
      }
    }
    if (page < 0 || !visible) return;
    const startX = clamp(point.x, visible.left, visible.left + visible.width);
    const startY = clamp(point.y, visible.top, visible.top + visible.height);
    gesture.current = { kind: "draw", page, startX, startY, initial: { x: startX, y: startY, width: 0, height: 0 } };
    setDragging(true);
    setSelectedPage(page);
    setRect(gesture.current.initial);
    setError("");
    event.currentTarget.setPointerCapture?.(event.pointerId);
    event.preventDefault();
  };

  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const current = gesture.current;
    const pane = viewportRef.current;
    if (!current || !pane) return;
    const page = pageBounds(pageRefs.current?.[current.page] || null, pane);
    if (!page) return;
    const bounds = drawingBounds(page, viewport);
    const point = localPoint(event, pane);
    const x = clamp(point.x, bounds.left, bounds.left + bounds.width);
    const y = clamp(point.y, bounds.top, bounds.top + bounds.height);
    if (current.kind === "draw") {
      setRect(drawRect(current.startX, current.startY, x, y));
      return;
    }
    const initial = current.initial;
    const dx = x - current.startX;
    const dy = y - current.startY;
    if (current.kind === "move") {
      setRect({ ...initial, x: clamp(initial.x + dx, bounds.left, bounds.left + bounds.width - initial.width), y: clamp(initial.y + dy, bounds.top, bounds.top + bounds.height - initial.height) });
      return;
    }
    const left = current.kind.includes("w") ? clamp(initial.x + dx, bounds.left, initial.x + initial.width - MIN_REGION) : initial.x;
    const top = current.kind.includes("n") ? clamp(initial.y + dy, bounds.top, initial.y + initial.height - MIN_REGION) : initial.y;
    const right = current.kind.includes("e") ? clamp(initial.x + initial.width + dx, initial.x + MIN_REGION, bounds.left + bounds.width) : initial.x + initial.width;
    const bottom = current.kind.includes("s") ? clamp(initial.y + initial.height + dy, initial.y + MIN_REGION, bounds.top + bounds.height) : initial.y + initial.height;
    setRect({ x: left, y: top, width: right - left, height: bottom - top });
  };

  const pinSelection = () => {
    const pane = viewportRef.current;
    if (!pane || !rect || selectedPage === undefined || rect.width < MIN_REGION || rect.height < MIN_REGION) return;
    const page = pageRefs.current?.[selectedPage];
    const bounds = pageBounds(page || null, pane);
    if (!bounds) return;
    const bbox: [number, number, number, number] = [
      clamp((rect.x - bounds.left) / bounds.width, 0, 1),
      clamp((rect.y - bounds.top) / bounds.height, 0, 1),
      clamp((rect.x + rect.width - bounds.left) / bounds.width, 0, 1),
      clamp((rect.y + rect.height - bounds.top) / bounds.height, 0, 1),
    ];
    const image = cropPdfBlockPreview(page || null, bbox);
    if (!image) { setError(zh ? "此页还未绘制完成，请稍后重试。" : "This page is still rendering. Try again shortly."); return; }
    // Scale both dimensions together. Independent width/height limits turn a
    // wide or tall selection into a letterboxed frame with the wrong shape.
    const preferredScale = Math.max(1, MIN_PIN_WIDTH / rect.width, 120 / rect.height);
    const maxScale = Math.min(Math.min(480, viewport.width - 16) / rect.width, (viewport.height - 16) / rect.height);
    const scale = Math.min(preferredScale, maxScale);
    const width = rect.width * scale;
    const height = rect.height * scale;
    const cascade = Math.min(5, pins?.filter(item => !item.minimized).length || 0) * 24;
    const geometry = constrainPanel({ x: viewport.left + viewport.width - width - 8 - cascade, y: viewport.top + 8 + cascade, width, height }, viewport);
    const next: PdfRegionPinRecord = {
      id: globalThis.crypto?.randomUUID?.() || `pdf-region-${Date.now()}-${Math.random().toString(36).slice(2)}`,
      kind: "region",
      title: zh ? `截图 · 第 ${selectedPage + 1} 页` : `Crop · Page ${selectedPage + 1}`,
      geometry,
      minimized: false,
      image,
      page: selectedPage + 1,
      bbox,
    };
    if (pins !== undefined) {
      const added=onAddPin?.(next);
      if (added!==true) { setError(typeof added==='string'?added:zh ? "钉住内容已达 6 项，请先移除一项。" : "Six items are pinned. Remove one before adding another."); return; }
    } else setPin(next);
    onActiveChange(false);
  };

  const visiblePins = (pins === undefined ? (pin ? [pin] : []) : pins).filter(item => !item.minimized);
  if (!active && !visiblePins.length && !(pin && pins === undefined && pin.minimized)) return null;
  const selectionReady = !!rect && rect.width >= MIN_REGION && rect.height >= MIN_REGION && !dragging;
  const actionTop = rect && rect.y + rect.height + 48 > viewport.height ? Math.max(8, rect.y - 43) : (rect?.y || 0) + (rect?.height || 0) + 8;
  return <>
    {active && <div
      ref={rootRef}
      className={`pdf-pin-selector${rect ? " has-region" : ""}`}
      role="dialog"
      aria-label={zh ? "截取并钉住区域" : "Capture and pin region"}
      tabIndex={-1}
      style={{ left: viewport.left, top: viewport.top, width: viewport.width, height: viewport.height, background: rect ? "transparent" : SHADE }}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={finish}
      onPointerCancel={finish}
      onKeyDown={event => { if (event.key === "Escape") onActiveChange(false); else if (event.key === "Enter" && selectionReady) pinSelection(); }}
    >
      {rect ? <>
        <div className="pdf-pin-shade" style={{ left: 0, top: 0, width: "100%", height: rect.y }} />
        <div className="pdf-pin-shade" style={{ left: 0, top: rect.y, width: rect.x, height: rect.height }} />
        <div className="pdf-pin-shade" style={{ left: rect.x + rect.width, top: rect.y, right: 0, height: rect.height }} />
        <div className="pdf-pin-shade" style={{ left: 0, top: rect.y + rect.height, width: "100%", bottom: 0 }} />
        <div className="pdf-pin-selection" data-pin-selection style={{ left: rect.x, top: rect.y, width: rect.width, height: rect.height }}>
          {(["nw", "ne", "sw", "se"] as const).map(handle => <span key={handle} className={`pdf-pin-handle is-${handle}`} data-pin-handle={handle} aria-hidden="true" />)}
        </div>
      </> : <div className="pdf-pin-hint">{zh ? "在 PDF 页面上拖动，选取需要钉住的区域" : "Drag across a PDF page to capture a region"}</div>}
      {selectionReady && <div className="pdf-pin-actions" style={{ left: Math.min(rect!.x + rect!.width, Math.max(12, viewport.width - 218)), top: actionTop }}>
        <button type="button" onClick={pinSelection}>{zh ? "钉住选区" : "Pin region"}</button>
        <button type="button" onClick={() => { setRect(undefined); setSelectedPage(undefined); }}>{zh ? "重选" : "Reselect"}</button>
        <button type="button" onClick={() => onActiveChange(false)}>{zh ? "取消" : "Cancel"}</button>
      </div>}
      {error && <div role="status" className="pdf-pin-error">{error}</div>}
      {!selectionReady && <button type="button" className="pdf-pin-cancel" onClick={() => onActiveChange(false)} aria-label={zh ? "取消截取" : "Cancel capture"}>×</button>}
    </div>}
    {visiblePins.map(item => <PinnedWindow key={item.id} pin={item} viewport={viewport} active={active} zh={zh}
      onUpdate={updated => { if (pins !== undefined) onUpdatePin?.(updated); else setPin(updated); }}
      onRemove={id => { if (pins !== undefined) onRemovePin?.(id); else setPin(undefined); }}
      onMinimize={id => {
        if (pins !== undefined) onMinimizePin?.(id);
        else { setPin(current => current?.id === id ? { ...current, minimized: true } : current); onMinimizePin?.(id); }
      }} />)}
    {pins === undefined && pin?.minimized && !active && <button className="pdf-pin-restore" type="button" onClick={() => setPin(current => current ? { ...current, minimized: false } : current)}>
      {zh ? "恢复钉住区域" : "Restore pinned region"}
    </button>}
  </>;
}

export default PdfPinnedRegion;
