import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { WorkflowSessionApi, type RewriteSelectionPreview } from '@/modules/chat/utils/request';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from '@/modules/knowledge/utils/imageUrl';
import {
  ArtifactRewriteDialog,
  type ArtifactRewriteSelection,
} from '../ArtifactRewriteDialog';
import { extractHtmlFromArtifact, htmlForStaticPreview } from './exportHtmlToPptx';
import { htmlWithInlinedEcharts } from './echartsInline';

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

const EDITOR_STYLE = `
  [data-el], [data-group] { cursor: crosshair !important; }
  .lazymind-ppt-edit-hover {
    outline: 5px solid rgba(99, 102, 241, .9) !important;
    outline-offset: 3px !important;
  }
  .lazymind-ppt-edit-selected {
    outline: 6px solid #f59e0b !important;
    outline-offset: 4px !important;
  }
`;

export function SlotHtmlSlide({
  slot,
  compact = false,
  sessionId,
  slotId,
  readOnly = false,
  onRefresh,
}: {
  slot: SlotRevision;
  compact?: boolean;
  sessionId?: string;
  slotId?: string;
  readOnly?: boolean;
  onRefresh?: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const frameCleanupRef = useRef(new Map<HTMLIFrameElement, () => void>());
  const selectedNodeRef = useRef<HTMLElement | null>(null);
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fittedFrame, setFittedFrame] = useState<FittedFrame | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [hovered, setHovered] = useState(false);
  const [expandedScale, setExpandedScale] = useState(scaleFromViewport);
  const [selection, setSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [editPreview, setEditPreview] = useState<RewriteSelectionPreview | null>(null);
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<string>();

  const page = slot.sort_order ?? ((slot.list_index ?? 0) + 1);
  const listIndex = slot.list_index ?? -1;
  const actionSlotId = slotId || slot.slot_id || slot.slot;
  const editable = Boolean(sessionId && actionSlotId && !readOnly && !compact && page > 0);
  const displayHtml = editPreview?.candidate_html || html;
  const srcDoc = useMemo(
    () => (displayHtml ? htmlForStaticPreview(displayHtml) : ''),
    [displayHtml],
  );

  const clearSelectedNode = useCallback(() => {
    selectedNodeRef.current?.classList.remove('lazymind-ppt-edit-selected');
    selectedNodeRef.current = null;
  }, []);
  const closeExpanded = useCallback(() => setExpanded(false), []);

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
    setApplyError(undefined);
    clearSelectedNode();
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
      if (!cancelled) setHtml(withCharts);
    })().catch(() => {
      if (!cancelled) {
        setError('Failed to load HTML slide');
        setHtml(null);
      }
    });
    return () => { cancelled = true; };
  }, [clearSelectedNode, slot.artifact_value, slot.revision, slot.slot_id]);

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

  const selectElement = useCallback((
    frame: HTMLIFrameElement,
    target: HTMLElement,
    clicked: HTMLElement = target,
  ) => {
    clearSelectedNode();
    target.classList.add('lazymind-ppt-edit-selected');
    selectedNodeRef.current = target;
    const targetRect = target.getBoundingClientRect();
    const frameRect = frame.getBoundingClientRect();
    const scaleX = frame.offsetWidth ? frameRect.width / frame.offsetWidth : 1;
    const left = frameRect.left + (targetRect.left + targetRect.width / 2) * scaleX;
    const topEdge = frameRect.top + targetRect.top * scaleX;
    const bottomEdge = frameRect.top + targetRect.bottom * scaleX;
    const below = bottomEdge + 92 < window.innerHeight;
    const computed = frame.contentWindow?.getComputedStyle(target);
    setSelection({
      type: 'ppt_html',
      page,
      el: target.dataset.el || '',
      index: dataElOccurrenceIndex(target),
      ...(target.dataset.group ? { group: target.dataset.group } : {}),
      ...(computed ? {
        computed_style: {
          font_size: computed.fontSize,
          width: computed.width,
          height: computed.height,
          line_height: computed.lineHeight,
          letter_spacing: computed.letterSpacing,
          text_align: computed.textAlign,
          font_weight: computed.fontWeight,
        },
      } : {}),
      // The stable data-el can live on an outer visual block while the user
      // actually clicked a nested heading/span. Preserve that exact visible
      // text so the Workflow action changes the intended leaf instead of
      // trying to replace every label inside the outer container.
      selectedText: pptClickedText(target, clicked),
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
    const style = doc.createElement('style');
    style.dataset.lazymindPptEditor = 'true';
    style.textContent = EDITOR_STYLE;
    doc.head?.appendChild(style);
    let hoverTarget: HTMLElement | null = null;
    const addressable = (eventTarget: EventTarget | null) => {
      const element = eventTarget as HTMLElement | null;
      if (!element || typeof element.closest !== 'function') return null;
      const target = element.closest<HTMLElement>('[data-el]');
      return target?.dataset.el ? target : null;
    };
    const onMouseOver = (event: MouseEvent) => {
      const target = editable && !editPreview ? addressable(event.target) : null;
      if (target === hoverTarget) return;
      hoverTarget?.classList.remove('lazymind-ppt-edit-hover');
      hoverTarget = target;
      hoverTarget?.classList.add('lazymind-ppt-edit-hover');
    };
    const onMouseOut = () => {
      hoverTarget?.classList.remove('lazymind-ppt-edit-hover');
      hoverTarget = null;
    };
    const onClick = (event: MouseEvent) => {
      const target = editable && !editPreview ? addressable(event.target) : null;
      if (target) {
        event.preventDefault();
        event.stopPropagation();
        selectElement(
          frame,
          target,
          event.target instanceof HTMLElement ? event.target : target,
        );
      }
    };
    const onMouseEnter = () => setHovered(true);
    const onMouseLeave = () => setHovered(false);
    doc.addEventListener('mouseover', onMouseOver);
    doc.addEventListener('mouseout', onMouseOut);
    doc.addEventListener('click', onClick);
    doc.addEventListener('mouseenter', onMouseEnter);
    doc.addEventListener('mouseleave', onMouseLeave);
    const cleanup = () => {
      hoverTarget?.classList.remove('lazymind-ppt-edit-hover');
      doc.removeEventListener('mouseover', onMouseOver);
      doc.removeEventListener('mouseout', onMouseOut);
      doc.removeEventListener('click', onClick);
      doc.removeEventListener('mouseenter', onMouseEnter);
      doc.removeEventListener('mouseleave', onMouseLeave);
      style.remove();
    };
    frameCleanupRef.current.set(frame, cleanup);
  }, [editPreview, editable, selectElement]);

  const cancelPreview = useCallback(() => {
    setEditPreview(null);
    setApplyError(undefined);
    setSelection(null);
    clearSelectedNode();
  }, [clearSelectedNode]);

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
          base_revision: slot.revision,
          input: { commit_token: token },
        },
        { silentError: true } as never,
      );
      if (response.data?.code !== 0 || response.data?.data?.status !== 'applied') {
        throw new Error('invalid apply response');
      }
      if (preview.candidate_html) setHtml(preview.candidate_html);
      setEditPreview(null);
      setSelection(null);
      clearSelectedNode();
      onRefresh?.();
    } catch (requestError) {
      const message = (requestError as { response?: { data?: { message?: string } } })
        ?.response?.data?.message;
      setApplyError(message || '应用失败，请刷新后重试');
    } finally {
      setApplying(false);
    }
  }, [actionSlotId, clearSelectedNode, listIndex, onRefresh, sessionId, slot.revision]);

  const retryPersistPreview = useCallback(() => {
    if (editPreview && !applying) void persistPreview(editPreview);
  }, [applying, editPreview, persistPreview]);

  if (error) return <div className='slot-html-slide slot-html-slide--error'>{error}</div>;
  if (!html || fittedFrame == null) {
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
        left: zoomed ? 0 : fittedFrame.left,
        top: zoomed ? 0 : fittedFrame.top,
        width: 1600,
        height: 900,
        transform: `scale(${zoomed ? expandedScale : fittedFrame.scale})`,
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
        {editable && !editPreview && (
          <div className='slot-html-slide__edit-hint'>点击元素进行 AI 修改</div>
        )}
        <button
          type='button'
          className='slot-html-slide__expand-button'
          onClick={() => setExpanded(true)}
          aria-label='放大幻灯片'
        >
          放大
        </button>
        {editPreview && (
          <div className='slot-html-slide__edit-confirm' role='status'>
            <span>
              {applying
                ? `正在保存 ${editPreview.target.el || '所选元素'}…`
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
          baseRevision={slot.revision}
          selection={selection}
          terminology='edit'
          onClose={() => setSelection(null)}
          onApplied={() => undefined}
          onPreviewReady={(preview) => {
            setApplyError(undefined);
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
          <div
            className='slot-html-slide__zoom-stage'
            style={{ width: 1600 * expandedScale, height: 900 * expandedScale }}
          >
            {renderFrame(true)}
          </div>
        </div>,
        document.body,
      )}
    </div>
  );
}
