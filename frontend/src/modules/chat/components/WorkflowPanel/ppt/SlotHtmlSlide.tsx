import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { ArtifactSourceButton } from '../ArtifactSourceButton';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import {
  WorkflowSessionApi,
  type PptHtmlComputedStyle,
  type PptHtmlSelectionTarget,
  type RewriteSelectionPreview,
} from '@/modules/chat/utils/request';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from '@/modules/knowledge/utils/imageUrl';
import {
  ArtifactRewriteDialog,
  type ArtifactRewriteSelection,
} from '../ArtifactRewriteDialog';
import {
  applyHtmlPreviewCompatibilityFallbacks,
  extractHtmlFromArtifact,
  htmlForStaticPreview,
} from './exportHtmlToPptx';
import { htmlWithInlinedEcharts } from './echartsInline';
import { resolveSlideAssets } from './slideAssets';

/** Compatibility shape used by the current composite preview pager. */
export interface SlideNavigation {
  index: number;
  total: number;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onChange: (index: number) => void;
}

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
  if (obj.path && (obj.type === 'text' || obj.type === 'json')) {
    const pathForSign = String(obj.path ?? obj.url ?? '').trim();
    const apiUrlRaw = obj.url ? String(obj.url).trim() : '';
    const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
    const fetchUrl = apiUrl && !isExpiredSignedUrl(apiUrl)
      ? apiUrl
      : await resolveMarkdownImageUrlAsync(pathForSign);
    const response = await fetch(fetchUrl);
    if (!response.ok) throw new Error('failed to load html artifact');
    const text = await response.text();
    if (isSpaFallbackHtml(text)) throw new Error('invalid artifact content');
    return text;
  }
  return '';
}

interface FittedFrame {
  scale: number;
  left: number;
  top: number;
}

/** Fit the fixed 1600×900 slide into any composite material frame. */
export function fitSlideFrame(containerW: number, containerH: number): FittedFrame {
  if (!containerW || containerW < 1) return { scale: 0.5, left: 0, top: 0 };
  const availableH = containerH > 0 ? containerH : containerW * 9 / 16;
  const scale = Math.max(0.02, Math.min(containerW / 1600, availableH / 900, 1));
  return {
    scale,
    left: Math.max(0, (containerW - 1600 * scale) / 2),
    top: Math.max(0, (availableH - 900 * scale) / 2),
  };
}

/** Return the clicked element's stable 1-based occurrence for duplicate data-el ids. */
export function dataElOccurrenceIndex(target: HTMLElement): number {
  const el = target.dataset.el;
  if (!el) return 1;
  const matches = Array.from(
    target.ownerDocument.querySelectorAll<HTMLElement>('[data-el]'),
  ).filter((candidate) => candidate.dataset.el === el);
  const offset = matches.indexOf(target);
  return offset >= 0 ? offset + 1 : 1;
}

/**
 * A styled heading often contains spans only for color/layout. Clicking one of
 * those spans still means editing the whole heading. For a larger addressable
 * card/section, keep the exact nested node text so local edits stay bounded.
 */
export function pptClickedText(target: HTMLElement, clicked: HTMLElement): string {
  const targetOwnsText = /^(H[1-6]|P|LI|TD|TH|FIGCAPTION|LABEL|BUTTON)$/i.test(
    target.tagName,
  );
  const source = targetOwnsText ? target : clicked;
  return (source.innerText || source.textContent || target.innerText || target.textContent || '').trim();
}

function scaleFromViewport(): number {
  if (typeof window === 'undefined') return 0.8;
  return Math.max(0.25, Math.min(
    (window.innerWidth - 64) / 1600,
    (window.innerHeight - 104) / 900,
    1,
  ));
}

function canConsumeVerticalWheel(element: Element, deltaY: number): boolean {
  const scrollElement = element as HTMLElement;
  const maxScrollTop = scrollElement.scrollHeight - scrollElement.clientHeight;
  if (maxScrollTop <= 1) return false;
  const overflowY = element.ownerDocument.defaultView?.getComputedStyle(element).overflowY;
  if (!overflowY || !['auto', 'scroll', 'overlay'].includes(overflowY)) return false;
  return deltaY < 0
    ? scrollElement.scrollTop > 1
    : scrollElement.scrollTop < maxScrollTop - 1;
}

/** Forward wheel input from the slide iframe to the nearest usable panel scroller. */
export function forwardSlideFrameWheel(frame: HTMLIFrameElement, event: WheelEvent): boolean {
  if (
    event.defaultPrevented
    || event.ctrlKey
    || event.deltaY === 0
    || frame.classList.contains('slot-html-slide__frame--zoomed')
  ) return false;

  const frameDocument = frame.contentDocument;
  const visited = new Set<Element>();
  let current = event.target as Element | null;
  while (current?.ownerDocument === frameDocument) {
    visited.add(current);
    if (canConsumeVerticalWheel(current, event.deltaY)) return false;
    current = current.parentElement;
  }
  const frameScrollRoot = frameDocument?.scrollingElement;
  if (
    frameScrollRoot
    && !visited.has(frameScrollRoot)
    && canConsumeVerticalWheel(frameScrollRoot, event.deltaY)
  ) return false;

  let scrollOwner = frame.parentElement;
  while (scrollOwner && !canConsumeVerticalWheel(scrollOwner, event.deltaY)) {
    scrollOwner = scrollOwner.parentElement;
  }
  if (!scrollOwner) return false;

  event.preventDefault();
  scrollOwner.scrollBy({ top: event.deltaY, behavior: 'auto' });
  return true;
}

const EDITOR_STYLE = `
  [data-el],
  [data-selection-scope="group"],
  [data-lazymind-selection-group="true"],
  [data-lazymind-selection-target="visual"] { cursor: crosshair !important; }
  .lazymind-ppt-edit-hover-overlay,
  .lazymind-ppt-edit-group-hover-overlay,
  .lazymind-ppt-edit-selection-overlay,
  .lazymind-ppt-edit-multi-selection-overlay {
    position: fixed !important;
    z-index: 2147483647 !important;
    box-sizing: border-box !important;
    pointer-events: none !important;
  }
  .lazymind-ppt-edit-hover-overlay {
    border: 5px solid rgba(99, 102, 241, .9) !important;
  }
  .lazymind-ppt-edit-group-hover-overlay {
    border: 5px solid rgba(99, 102, 241, .9) !important;
  }
  .lazymind-ppt-edit-selection-overlay {
    border: 6px solid #f59e0b !important;
  }
  .lazymind-ppt-edit-multi-selection-overlay {
    border: 6px solid #f59e0b !important;
  }
  .lazymind-ppt-edit-multi-selection-count {
    position: absolute !important;
    top: -38px !important;
    left: -6px !important;
    padding: 5px 10px !important;
    border-radius: 6px !important;
    color: #fff !important;
    background: #d97706 !important;
    font: 600 20px/1.2 system-ui, sans-serif !important;
    white-space: nowrap !important;
  }
`;

interface OverlayRect {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

function createPptRectOverlay(
  doc: Document,
  rect: OverlayRect,
  className: string,
  offset: number,
): HTMLDivElement {
  const viewportWidth = doc.documentElement.clientWidth || doc.defaultView?.innerWidth || rect.right + offset;
  const viewportHeight = doc.documentElement.clientHeight || doc.defaultView?.innerHeight || rect.bottom + offset;
  const left = Math.max(0, rect.left - offset);
  const top = Math.max(0, rect.top - offset);
  const right = Math.min(viewportWidth, rect.right + offset);
  const bottom = Math.min(viewportHeight, rect.bottom + offset);
  const overlay = doc.createElement('div');
  overlay.className = className;
  overlay.setAttribute('aria-hidden', 'true');
  overlay.style.left = `${left}px`;
  overlay.style.top = `${top}px`;
  overlay.style.width = `${Math.max(0, right - left)}px`;
  overlay.style.height = `${Math.max(0, bottom - top)}px`;
  doc.documentElement.appendChild(overlay);
  return overlay;
}

function createPptElementOverlay(
  target: HTMLElement,
  className: string,
  offset: number,
): HTMLDivElement {
  return createPptRectOverlay(target.ownerDocument, target.getBoundingClientRect(), className, offset);
}

export function createPptHoverOverlay(target: HTMLElement): HTMLDivElement {
  return createPptElementOverlay(target, 'lazymind-ppt-edit-hover-overlay', 3);
}

export function createPptSelectionOverlay(target: HTMLElement): HTMLDivElement {
  return createPptElementOverlay(target, 'lazymind-ppt-edit-selection-overlay', 4);
}

export function createPptGroupHoverOverlay(targets: HTMLElement[]): HTMLDivElement | null {
  if (!targets.length) return null;
  const doc = targets[0].ownerDocument;
  if (targets.some((target) => target.ownerDocument !== doc)) return null;
  return createPptRectOverlay(
    doc,
    selectionBounds(targets),
    'lazymind-ppt-edit-group-hover-overlay',
    3,
  );
}

export function createPptMultiSelectionOverlay(targets: HTMLElement[]): HTMLDivElement | null {
  if (!targets.length) return null;
  const doc = targets[0].ownerDocument;
  if (targets.some((target) => target.ownerDocument !== doc)) return null;
  const rects = targets.map((target) => target.getBoundingClientRect());
  const overlay = createPptRectOverlay(doc, {
    left: Math.min(...rects.map((rect) => rect.left)),
    top: Math.min(...rects.map((rect) => rect.top)),
    right: Math.max(...rects.map((rect) => rect.right)),
    bottom: Math.max(...rects.map((rect) => rect.bottom)),
  }, 'lazymind-ppt-edit-multi-selection-overlay', 4);
  const count = doc.createElement('span');
  count.className = 'lazymind-ppt-edit-multi-selection-count';
  count.textContent = `已选 ${targets.length} 个元素`;
  overlay.appendChild(count);
  return overlay;
}

interface SelectedPptElement {
  node: HTMLElement;
  target: PptHtmlSelectionTarget;
}

export interface PptHoverCandidate {
  targets: HTMLElement[];
  scope: 'item' | 'group';
}

function selectedElementKey(target: PptHtmlSelectionTarget): string {
  return `${target.el}\u0000${target.index || 1}\u0000${(target.dom_path || []).join('.')}`;
}

function selectionElementId(target: HTMLElement): string {
  return target.dataset.el || target.dataset.lazymindSelectionEl || '';
}

/** Exact element-child path from body for a legacy visual without data-el. */
export function pptDomPath(target: HTMLElement): number[] | null {
  const path: number[] = [];
  let current: Element | null = target;
  while (current && current !== target.ownerDocument.body) {
    const parent: HTMLElement | null = current.parentElement;
    if (!parent) return null;
    const index = Array.from(parent.children).indexOf(current);
    if (index < 0) return null;
    path.unshift(index);
    current = parent;
  }
  return current === target.ownerDocument.body && path.length ? path : null;
}

function selectionBounds(targets: HTMLElement[]): OverlayRect {
  const rects = targets.map((target) => target.getBoundingClientRect());
  return {
    left: Math.min(...rects.map((rect) => rect.left)),
    top: Math.min(...rects.map((rect) => rect.top)),
    right: Math.max(...rects.map((rect) => rect.right)),
    bottom: Math.max(...rects.map((rect) => rect.bottom)),
  };
}

function topLevelAddressableDescendants(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>('[data-el]')).filter((target) => {
    let parent = target.parentElement;
    while (parent && parent !== container) {
      if (parent.dataset.el) return false;
      parent = parent.parentElement;
    }
    return parent === container;
  });
}

function repeatedSelectionFamily(targets: HTMLElement[]): string | null {
  const families = targets.map((target) => (
    target.dataset.el?.match(/^(.+?)[-_]\d+$/)?.[1] || ''
  ));
  return families[0] && families.every((family) => family === families[0])
    ? families[0]
    : null;
}

function isImplicitSelectionGroup(container: HTMLElement, targets: HTMLElement[]): boolean {
  if (targets.length < 2 || targets.length > 12) return false;
  const display = container.ownerDocument.defaultView?.getComputedStyle(container).display || '';
  if (!display.includes('grid') && !display.includes('flex')) return false;
  const rects = targets.map((target) => target.getBoundingClientRect());
  if (rects.some((rect) => rect.width <= 0 || rect.height <= 0)) return false;
  const verticalOverlap = Math.min(...rects.map((rect) => rect.bottom))
    - Math.max(...rects.map((rect) => rect.top));
  const shortest = Math.min(...rects.map((rect) => rect.height));
  if (verticalOverlap >= shortest * 0.45) return true;

  // Vertical compatibility is deliberately narrower: only a repeated stable
  // family such as bullet-1/2/3 can form a column. Mixed page content cannot.
  if (!repeatedSelectionFamily(targets)) return false;
  const horizontalOverlap = Math.min(...rects.map((rect) => rect.right))
    - Math.max(...rects.map((rect) => rect.left));
  const narrowest = Math.min(...rects.map((rect) => rect.width));
  return horizontalOverlap >= narrowest * 0.45;
}

function selectionGroupTargets(container: HTMLElement): HTMLElement[] {
  if (container.matches('html, body') || container.dataset.el) return [];
  const targets = topLevelAddressableDescendants(container);
  if (container.dataset.selectionScope === 'group') return targets.length > 1 ? targets : [];
  return isImplicitSelectionGroup(container, targets) ? targets : [];
}

function explicitDataGroupTargets(target: HTMLElement): HTMLElement[] {
  const group = target.dataset.group?.trim();
  if (!group) return [];
  return Array.from(target.ownerDocument.querySelectorAll<HTMLElement>('[data-group]'))
    .filter((candidate) => candidate.dataset.group?.trim() === group && selectionElementId(candidate));
}

function markLegacyVisualSelectionTargets(doc: Document): void {
  const marked = new Set<HTMLElement>();
  doc.querySelectorAll<HTMLElement>('svg, canvas, img, [role="img"]').forEach((media) => {
    if (media.closest('#bg, [data-el]')) return;
    const mediaRect = media.getBoundingClientRect();
    if (mediaRect.width < 80 || mediaRect.height < 60 || mediaRect.width * mediaRect.height < 15000) {
      return;
    }

    let candidate = media.matches('svg')
      ? (media.parentElement || media)
      : (media.closest('picture') as HTMLElement | null) || media;
    while (candidate.parentElement && candidate.parentElement !== doc.body) {
      const parent = candidate.parentElement;
      if (
        parent.matches('#bg, #ct, .wrapper, [data-el]')
        || parent.querySelector('[data-el]')
      ) break;
      const candidateRect = candidate.getBoundingClientRect();
      const parentRect = parent.getBoundingClientRect();
      const candidateArea = candidateRect.width * candidateRect.height;
      const parentArea = parentRect.width * parentRect.height;
      if (candidateArea <= 0 || parentArea > candidateArea * 1.8) break;
      candidate = parent;
    }
    if (candidate.matches('#bg, #ct, .wrapper, html, body') || marked.has(candidate)) return;
    const path = pptDomPath(candidate);
    if (!path) return;
    candidate.dataset.lazymindSelectionTarget = 'visual';
    candidate.dataset.lazymindSelectionEl = `__lazymind_auto_${candidate.tagName.toLowerCase()}_${path.join('_')}`;
    marked.add(candidate);
  });
}

/** Mark strict repeated groups and foreground visuals without changing stored HTML. */
export function markImplicitPptSelectionGroups(doc: Document): void {
  doc.querySelectorAll<HTMLElement>('body *:not([data-el])').forEach((container) => {
    if (container.dataset.selectionScope === 'group') return;
    if (selectionGroupTargets(container).length > 1) {
      container.dataset.lazymindSelectionGroup = 'true';
    }
  });
  markLegacyVisualSelectionTargets(doc);
}

function pointTouchesGroupEdge(
  container: HTMLElement,
  clientX: number | undefined,
  clientY: number | undefined,
): boolean {
  if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) return false;
  const rect = container.getBoundingClientRect();
  const edge = Math.min(20, Math.max(8, Math.min(rect.width, rect.height) * 0.08));
  return clientX! >= rect.left && clientX! <= rect.right
    && clientY! >= rect.top && clientY! <= rect.bottom
    && (
      clientX! - rect.left <= edge
      || rect.right - clientX! <= edge
      || clientY! - rect.top <= edge
      || rect.bottom - clientY! <= edge
    );
}

/** Resolve the floating selection scope under the pointer without committing it. */
export function resolvePptHoverCandidate(
  eventTarget: EventTarget | null,
  clientX?: number,
  clientY?: number,
): PptHoverCandidate | null {
  const element = eventTarget as HTMLElement | null;
  if (!element || typeof element.closest !== 'function') return null;
  const item = element.closest<HTMLElement>(
    '[data-el], [data-lazymind-selection-target="visual"]',
  );
  if (item) {
    const explicitGroup = explicitDataGroupTargets(item);
    if (explicitGroup.length > 1) return { targets: explicitGroup, scope: 'group' };
  }
  let groupContainer: HTMLElement | null = item?.parentElement || element;
  let groupTargets: HTMLElement[] = [];
  while (groupContainer && !groupContainer.matches('html, body')) {
    groupTargets = selectionGroupTargets(groupContainer);
    if (groupTargets.length > 1) break;
    groupContainer = groupContainer.parentElement;
  }

  if (groupContainer && groupTargets.length > 1) {
    const pointerIsOnGroupSurface = !item;
    const pointerIsOnGroupEdge = pointTouchesGroupEdge(groupContainer, clientX, clientY);
    if (pointerIsOnGroupSurface || pointerIsOnGroupEdge) {
      return { targets: groupTargets, scope: 'group' };
    }
  }
  if (item && selectionElementId(item)) return { targets: [item], scope: 'item' };
  return null;
}

export function SlotHtmlSlide({
  slot,
  compact = false,
  sessionId,
  slotId,
  readOnly = false,
  onRefresh,
  expanded: controlledExpanded,
  onExpandedChange,
  navigation,
  allowExpand = true,
  expandedAccessory,
}: {
  slot: SlotRevision;
  compact?: boolean;
  sessionId?: string;
  slotId?: string;
  readOnly?: boolean;
  onRefresh?: () => void;
  expanded?: boolean;
  onExpandedChange?: (expanded: boolean) => void;
  navigation?: SlideNavigation;
  allowExpand?: boolean;
  expandedAccessory?: ReactNode;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const frameCleanupRef = useRef(new Map<HTMLIFrameElement, () => void>());
  const selectionOverlayRefs = useRef<HTMLDivElement[]>([]);
  const selectedElementsRef = useRef<SelectedPptElement[]>([]);
  const editPreviewRef = useRef<RewriteSelectionPreview | null>(null);
  const [html, setHtml] = useState<string | null>(null);
  const [sourceHtml, setSourceHtml] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [fittedFrame, setFittedFrame] = useState<FittedFrame | null>(null);
  const [internalExpanded, setInternalExpanded] = useState(false);
  const [hovered, setHovered] = useState(false);
  const [expandedScale, setExpandedScale] = useState(scaleFromViewport);
  const [selection, setSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [editPreview, setEditPreview] = useState<RewriteSelectionPreview | null>(null);
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<string>();
  const [localRevision, setLocalRevision] = useState(slot.revision);
  const [localDraftVersion, setLocalDraftVersion] = useState(slot.draft_version);

  const page = slot.sort_order ?? ((slot.list_index ?? 0) + 1);
  const listIndex = slot.list_index ?? -1;
  const actionSlotId = slotId || slot.slot_id || slot.slot;
  const editable = Boolean(sessionId && actionSlotId && !readOnly && !compact && page > 0);
  const displayHtml = editPreview?.candidate_html || html;
  const pagedNavigation = navigation && 'index' in navigation ? navigation : undefined;
  const expanded = controlledExpanded ?? pagedNavigation?.expanded ?? internalExpanded;
  const [resolvedDisplayHtml, setResolvedDisplayHtml] = useState('');
  useEffect(() => {
    let cancelled = false;
    setResolvedDisplayHtml('');
    resolveSlideAssets(displayHtml || '').then((value) => {
      if (!cancelled) setResolvedDisplayHtml(value);
    }).catch(() => {
      if (!cancelled) setError('Failed to load slide images');
    });
    return () => { cancelled = true; };
  }, [displayHtml]);
  const srcDoc = useMemo(
    () => (resolvedDisplayHtml ? htmlForStaticPreview(resolvedDisplayHtml) : ''),
    [resolvedDisplayHtml],
  );

  const clearSelectedNode = useCallback(() => {
    selectionOverlayRefs.current.forEach((overlay) => overlay.remove());
    selectionOverlayRefs.current = [];
  }, []);
  const clearSelection = useCallback(() => {
    selectedElementsRef.current = [];
    setSelection(null);
    clearSelectedNode();
  }, [clearSelectedNode]);
  const setExpanded = useCallback((next: boolean) => {
    setInternalExpanded(next);
    pagedNavigation?.onExpandedChange(next);
    onExpandedChange?.(next);
  }, [onExpandedChange, pagedNavigation]);
  const closeExpanded = useCallback(() => {
    clearSelection();
    setExpanded(false);
  }, [clearSelection, setExpanded]);

  useEffect(() => {
    editPreviewRef.current = editPreview;
  }, [editPreview]);

  useEffect(() => {
    setLocalRevision(slot.revision);
    setLocalDraftVersion(slot.draft_version);
  }, [slot.draft_version, slot.revision]);

  useEffect(() => {
    if (!expanded) return undefined;
    const update = () => setExpandedScale(scaleFromViewport());
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeExpanded();
    };
    update();
    window.addEventListener('resize', update);
    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('resize', update);
      window.removeEventListener('keydown', onKeyDown);
    };
  }, [closeExpanded, expanded]);

  useEffect(() => () => {
    frameCleanupRef.current.forEach((cleanup) => cleanup());
    frameCleanupRef.current.clear();
    clearSelectedNode();
  }, [clearSelectedNode]);

  useEffect(() => {
    let cancelled = false;
    setError(null);
    setEditPreview(null);
    editPreviewRef.current = null;
    setApplyError(undefined);
    clearSelection();
    (async () => {
      const text = await loadArtifactText(slot.artifact_value);
      if (cancelled) return;
      const extracted = extractHtmlFromArtifact(text) || extractHtmlFromArtifact(slot.artifact_value);
      if (!extracted) {
        setError('Not a valid HTML slide');
        setHtml(null);
        return;
      }
      const withCharts = await htmlWithInlinedEcharts(extracted);
      if (!cancelled) {
        setHtml(withCharts);
        setSourceHtml(extracted);
      }
    })().catch(() => {
      if (!cancelled) {
        setError('Failed to load HTML slide');
        setHtml(null);
      }
    });
    return () => { cancelled = true; };
  }, [clearSelection, slot.artifact_value, slot.revision, slot.slot_id]);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) return undefined;
    const update = () => {
      const rect = viewport.getBoundingClientRect();
      setFittedFrame(fitSlideFrame(Math.max(0, rect.width), Math.max(0, rect.height)));
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(viewport);
    return () => observer.disconnect();
  }, [compact]);

  const selectElements = useCallback((
    frame: HTMLIFrameElement,
    candidateNodes: HTMLElement[],
    clicked: HTMLElement,
    additive = false,
  ) => {
    const candidates = candidateNodes.map((target): SelectedPptElement => {
      const stableEl = selectionElementId(target);
      const structuralPath = target.dataset.el ? null : pptDomPath(target);
      const computed = frame.contentWindow?.getComputedStyle(target);
      const computedStyle: PptHtmlComputedStyle | undefined = computed ? {
        font_size: computed.fontSize,
        width: computed.width,
        height: computed.height,
        line_height: computed.lineHeight,
        letter_spacing: computed.letterSpacing,
        text_align: computed.textAlign,
        font_weight: computed.fontWeight,
      } : undefined;
      const textSource = candidateNodes.length === 1 ? clicked : target;
      const selectedText = pptClickedText(target, textSource);
      return {
        node: target,
        target: {
          el: stableEl,
          index: dataElOccurrenceIndex(target),
          ...(structuralPath ? {
            dom_path: structuralPath,
            tag: target.tagName.toLowerCase(),
          } : {}),
          ...(target.dataset.group ? { group: target.dataset.group } : {}),
          ...(selectedText ? { selected_text: selectedText } : {}),
          ...(computedStyle ? { computed_style: computedStyle } : {}),
        },
      };
    });
    const existing = selectedElementsRef.current;
    let next: SelectedPptElement[];
    if (!additive) {
      next = candidates;
    } else {
      next = [...existing];
      candidates.forEach((candidate) => {
        const candidateKey = selectedElementKey(candidate.target);
        if (next.some((item) => selectedElementKey(item.target) === candidateKey)) {
          next = next.filter((item) => selectedElementKey(item.target) !== candidateKey);
          return;
        }
        // A parent and one of its children cannot both be edited deterministically.
        // The most recently clicked semantic target wins within that branch.
        next = next.filter((item) => (
          !item.node.contains(candidate.node) && !candidate.node.contains(item.node)
        ));
        next.push(candidate);
      });
    }

    clearSelectedNode();
    selectedElementsRef.current = next;
    if (!next.length) {
      setSelection(null);
      return;
    }

    if (next.length === 1) {
      selectionOverlayRefs.current = [createPptSelectionOverlay(next[0].node)];
    } else {
      const combinedOverlay = createPptMultiSelectionOverlay(next.map((item) => item.node));
      selectionOverlayRefs.current = combinedOverlay ? [combinedOverlay] : [];
    }

    const targetRect = selectionBounds(next.map((item) => item.node));
    const frameRect = frame.getBoundingClientRect();
    const scaleX = frame.offsetWidth ? frameRect.width / frame.offsetWidth : 1;
    const left = frameRect.left + (targetRect.left + (targetRect.right - targetRect.left) / 2) * scaleX;
    const topEdge = frameRect.top + targetRect.top * scaleX;
    const bottomEdge = frameRect.top + targetRect.bottom * scaleX;
    const below = bottomEdge + 92 < window.innerHeight;
    const primary = next[0].target;
    setSelection({
      type: 'ppt_html',
      page,
      el: primary.el,
      ...(primary.index ? { index: primary.index } : {}),
      ...(primary.dom_path ? { dom_path: primary.dom_path } : {}),
      ...(primary.tag ? { tag: primary.tag } : {}),
      ...(primary.group ? { group: primary.group } : {}),
      ...(primary.computed_style ? { computed_style: primary.computed_style } : {}),
      ...(next.length > 1 ? {
        targets: next.map((item) => item.target),
        scope: 'multi' as const,
      } : { scope: 'item' as const }),
      // The stable data-el can live on an outer visual block while the user
      // actually clicked a nested heading/span. Preserve that exact visible
      // text so the Workflow action changes the intended leaf instead of
      // trying to replace every label inside the outer container.
      selectedText: next.length === 1 ? (primary.selected_text || '') : '',
      anchor: {
        left,
        top: below ? bottomEdge : topEdge,
        placement: below ? 'below' : 'above',
      },
    });
  }, [clearSelectedNode, page]);

  const attachFrameInteractions = useCallback((frame: HTMLIFrameElement) => {
    frameCleanupRef.current.get(frame)?.();
    const doc = frame.contentDocument;
    if (!doc) return;
    applyHtmlPreviewCompatibilityFallbacks(doc);
    const style = doc.createElement('style');
    style.dataset.lazymindPptEditor = 'true';
    style.textContent = EDITOR_STYLE;
    doc.head?.appendChild(style);
    markImplicitPptSelectionGroups(doc);
    let hoverCandidateKey = '';
    let hoverOverlay: HTMLDivElement | null = null;
    const setHoverCandidate = (candidate: PptHoverCandidate | null) => {
      const key = candidate
        ? `${candidate.scope}:${candidate.targets.map((target) => (
          `${selectionElementId(target)}:${dataElOccurrenceIndex(target)}`
        )).join('|')}`
        : '';
      if (key === hoverCandidateKey) return;
      hoverOverlay?.remove();
      hoverOverlay = null;
      hoverCandidateKey = key;
      if (!candidate) return;
      hoverOverlay = candidate.targets.length === 1
        ? createPptHoverOverlay(candidate.targets[0])
        : createPptGroupHoverOverlay(candidate.targets);
    };
    const onMouseMove = (event: MouseEvent) => {
      const candidate = editable && !editPreviewRef.current
        ? resolvePptHoverCandidate(event.target, event.clientX, event.clientY)
        : null;
      const alreadySelected = candidate
        && candidate.targets.length === selectedElementsRef.current.length
        && candidate.targets.every((target) => (
          selectedElementsRef.current.some((item) => item.node === target)
        ));
      setHoverCandidate(alreadySelected ? null : candidate);
    };
    const onClick = (event: MouseEvent) => {
      const candidate = editable && !editPreviewRef.current
        ? resolvePptHoverCandidate(event.target, event.clientX, event.clientY)
        : null;
      setHoverCandidate(null);
      if (candidate) {
        event.preventDefault();
        event.stopPropagation();
        selectElements(
          frame,
          candidate.targets,
          event.target instanceof HTMLElement ? event.target : candidate.targets[0],
          event.shiftKey,
        );
        return;
      }
      clearSelection();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || editPreviewRef.current) return;
      clearSelection();
    };
    const onMouseEnter = () => setHovered(true);
    const onMouseLeave = () => {
      setHovered(false);
      setHoverCandidate(null);
    };
    const onWheel = (event: WheelEvent) => {
      forwardSlideFrameWheel(frame, event);
    };
    doc.addEventListener('mousemove', onMouseMove);
    doc.addEventListener('click', onClick);
    doc.addEventListener('mouseenter', onMouseEnter);
    doc.addEventListener('mouseleave', onMouseLeave);
    doc.addEventListener('wheel', onWheel, { passive: false });
    doc.addEventListener('keydown', onKeyDown);
    const cleanup = () => {
      setHoverCandidate(null);
      doc.removeEventListener('mousemove', onMouseMove);
      doc.removeEventListener('click', onClick);
      doc.removeEventListener('mouseenter', onMouseEnter);
      doc.removeEventListener('mouseleave', onMouseLeave);
      doc.removeEventListener('wheel', onWheel);
      doc.removeEventListener('keydown', onKeyDown);
      style.remove();
    };
    frameCleanupRef.current.set(frame, cleanup);
  }, [clearSelection, editable, selectElements]);

  const cancelPreview = useCallback(() => {
    setEditPreview(null);
    editPreviewRef.current = null;
    setApplyError(undefined);
    clearSelection();
  }, [clearSelection]);

  const persistPreview = useCallback(async (preview: RewriteSelectionPreview) => {
    const token = preview.commit?.token;
    if (!token || !sessionId || !actionSlotId) return;
    setApplying(true);
    setApplyError(undefined);
    try {
      const response = await WorkflowSessionApi().executeArtifactAction(
        sessionId,
        actionSlotId,
        listIndex,
        {
          action: 'rewrite_selection',
          base_revision: preview.base_revision,
          ...(preview.base_draft_version !== undefined
            ? { base_draft_version: preview.base_draft_version }
            : {}),
          input: { commit_token: token },
        },
        { silentError: true } as never,
      );
      if (response.data?.code !== 0 || response.data?.data?.status !== 'applied') {
        throw new Error('invalid apply response');
      }
      const result = response.data.data;
      if (typeof result.revision !== 'number' || typeof result.draft_version !== 'number') {
        throw new Error('invalid apply baseline');
      }
      setLocalRevision(result.revision);
      setLocalDraftVersion(result.draft_version);
      if (preview.candidate_html) {
        setHtml(preview.candidate_html);
        setSourceHtml(preview.candidate_html);
      }
      setEditPreview(null);
      editPreviewRef.current = null;
      clearSelection();
      onRefresh?.();
    } catch (requestError) {
      const message = (requestError as { response?: { data?: { message?: string } } })
        ?.response?.data?.message;
      setApplyError(message || '应用失败，请刷新后重试');
    } finally {
      setApplying(false);
    }
  }, [actionSlotId, clearSelection, listIndex, onRefresh, sessionId]);

  const retryPersistPreview = useCallback(() => {
    if (editPreview && !applying) void persistPreview(editPreview);
  }, [applying, editPreview, persistPreview]);

  if (error && !expanded) return <div className='slot-html-slide slot-html-slide--error'>{error}</div>;
  if ((!html || fittedFrame == null) && !expanded) {
    return (
      <div ref={hostRef} className={`slot-html-slide${compact ? ' slot-html-slide--compact' : ''}`}>
        <div ref={viewportRef} className='slot-html-slide__viewport slot-html-slide__viewport--placeholder'>
          <div className='slot-html-slide slot-html-slide--loading'>Loading slide…</div>
        </div>
      </div>
    );
  }

  const renderFrame = (zoomed: boolean) => (
    <iframe
      className={`slot-html-slide__frame${zoomed ? ' slot-html-slide__frame--zoomed' : ''}`}
      title={`${zoomed ? '放大预览-' : 'slide-'}${page}`}
      sandbox='allow-scripts allow-same-origin'
      srcDoc={srcDoc}
      onLoad={(event) => attachFrameInteractions(event.currentTarget)}
      aria-label={editable ? '点击幻灯片元素进行修改' : '点击放大幻灯片'}
      style={{
        position: 'absolute',
        left: zoomed ? 0 : (fittedFrame?.left ?? 0),
        top: zoomed ? 0 : (fittedFrame?.top ?? 0),
        width: 1600,
        height: 900,
        transform: `scale(${zoomed ? expandedScale : (fittedFrame?.scale ?? 0.5)})`,
        transformOrigin: 'top left',
      }}
    />
  );

  return (
    <div
      ref={hostRef}
      className={[
        'slot-html-slide',
        compact ? 'slot-html-slide--compact' : '',
        hovered ? 'slot-html-slide--hovered' : '',
        editable ? 'slot-html-slide--editable' : '',
      ].filter(Boolean).join(' ')}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div ref={viewportRef} className='slot-html-slide__viewport slot-html-slide__viewport--interactive'>
        {renderFrame(false)}
        <ArtifactSourceButton value={editPreview?.candidate_html || sourceHtml} overlay />
        {editable && !editPreview && (
          <div className='slot-html-slide__edit-hint'>点击元素进行 AI 修改</div>
        )}
        {allowExpand && (
          <button
            type='button'
            className='slot-html-slide__expand-button'
            onClick={() => setExpanded(true)}
            aria-label='放大幻灯片'
          >
            放大
          </button>
        )}
        {editPreview && (
          <div className='slot-html-slide__edit-confirm' role='status'>
            <span>
              {applying
                ? `正在保存 ${editPreview.target.count && editPreview.target.count > 1
                  ? `${editPreview.target.count} 个元素`
                  : editPreview.target.el || '所选元素'}…`
                : applyError
                  ? '自动保存失败'
                  : '正在准备保存…'}
            </span>
            {applyError && <span className='slot-html-slide__edit-error'>{applyError}</span>}
            {applyError && (
              <>
                <button type='button' onClick={cancelPreview}>取消候选</button>
                <button type='button' className='is-primary' onClick={retryPersistPreview}>重试保存</button>
              </>
            )}
          </div>
        )}
      </div>

      {editable && sessionId && actionSlotId && (
        <ArtifactRewriteDialog
          open={Boolean(selection)}
          sessionId={sessionId}
          slotId={actionSlotId}
          listIndex={listIndex}
          baseRevision={localRevision}
          baseDraftVersion={localDraftVersion}
          selection={selection}
          terminology='edit'
          interactionRootRef={hostRef}
          onClose={clearSelection}
          onApplied={() => undefined}
          portalZIndex={expanded ? 2200 : undefined}
          onPreviewReady={(preview) => {
            setApplyError(undefined);
            editPreviewRef.current = preview;
            setEditPreview(preview);
            setExpanded(false);
            void persistPreview(preview);
          }}
        />
      )}

      {expanded && typeof document !== 'undefined' && createPortal(
        <div
          className='slot-html-slide__zoom-overlay'
          role='dialog'
          aria-modal='true'
          aria-label='放大幻灯片预览'
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) closeExpanded();
          }}
        >
          <button type='button' className='slot-html-slide__zoom-close' aria-label='关闭放大预览' onClick={closeExpanded}>×</button>
          {pagedNavigation && pagedNavigation.total > 1 && (
            <>
              <button
                type='button'
                className='slot-html-slide__zoom-nav slot-html-slide__zoom-nav--previous'
                aria-label='上一页幻灯片'
                disabled={pagedNavigation.index <= 0 || applying || Boolean(selection) || Boolean(editPreview)}
                onClick={() => pagedNavigation.onChange(pagedNavigation.index - 1)}
              >‹</button>
              <button
                type='button'
                className='slot-html-slide__zoom-nav slot-html-slide__zoom-nav--next'
                aria-label='下一页幻灯片'
                disabled={pagedNavigation.index >= pagedNavigation.total - 1 || applying || Boolean(selection) || Boolean(editPreview)}
                onClick={() => pagedNavigation.onChange(pagedNavigation.index + 1)}
              >›</button>
            </>
          )}
          {pagedNavigation && pagedNavigation.total > 1 && (
            <div className='slot-html-slide__zoom-page' aria-live='polite'>
              {pagedNavigation.index + 1} / {pagedNavigation.total}
            </div>
          )}
          <div
            className='slot-html-slide__zoom-stage'
            style={{ width: 1600 * expandedScale, height: 900 * expandedScale }}
          >
            {error ? <div className='slot-html-slide--error'>{error}</div>
              : srcDoc ? renderFrame(true) : <div className='slot-html-slide--loading'>正在加载幻灯片…</div>}
          </div>
          {expandedAccessory && (
            <div className='slot-html-slide__zoom-accessory'>
              {expandedAccessory}
            </div>
          )}
        </div>,
        document.body,
      )}
    </div>
  );
}
