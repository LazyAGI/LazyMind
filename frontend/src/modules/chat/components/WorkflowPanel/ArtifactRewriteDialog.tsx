import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react';
import ReactDOM from 'react-dom';
import { diffWordsWithSpace } from 'diff';
import { useTranslation } from 'react-i18next';
import { ArrowUpOutlined, CloseOutlined, HighlightOutlined } from '@ant-design/icons';
import {
  WorkflowSessionApi,
  type RewriteSelection,
  type RewriteSelectionPreview,
} from '@/modules/chat/utils/request';
import { selectionActionAnchor, type SelectionActionAnchor } from './artifactRewriteSelection';
import { captureRewriteScrollAnchor } from './rewriteScrollAnchor';
import './ArtifactRewriteDialog.scss';
import './ArtifactRewriteSelectionHighlight.scss';

export type ArtifactRewriteSelection = RewriteSelection & {
  selectedText: string;
  sourceRange?: { selected_text: string; start: number; end: number };
  sourceRanges?: Array<{ selected_text: string; start: number; end: number }>;
  nodeSelections?: Array<{node_id: string; selected_text?: string}>;
  anchor?: SelectionActionAnchor;
  paragraph?: HTMLElement;
  paragraphs?: HTMLElement[];
  startOffset?: number;
};

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

function computeRewriteFormPosition(
  anchor: SelectionActionAnchor,
  size: { width: number; height: number },
): CSSProperties {
  const inset = 16;
  const gap = 8;
  const halfWidth = size.width / 2;
  const left = clamp(anchor.left, inset + halfWidth, window.innerWidth - inset - halfWidth);

  if (anchor.placement === 'above') {
    const top = clamp(anchor.top - gap, inset + size.height, window.innerHeight - inset);
    return { top, left, transform: 'translate(-50%, -100%)' };
  }

  const top = clamp(anchor.top + gap, inset, window.innerHeight - inset - size.height);
  return { top, left, transform: 'translate(-50%, 0)' };
}

function resolveRewriteFormAnchor(selection: ArtifactRewriteSelection | null): SelectionActionAnchor | null {
  if (selection?.anchor) return selection.anchor;
  const browserSelection = globalThis.getSelection();
  if (!browserSelection?.rangeCount || browserSelection.isCollapsed) return null;
  return selectionActionAnchor(browserSelection.getRangeAt(0));
}

interface ArtifactRewriteDialogProps {
  open: boolean;
  sessionId: string;
  slotId: string;
  listIndex: number;
  baseRevision: number;
  baseDraftVersion?: number;
  selection: ArtifactRewriteSelection | null;
  onClose: () => void;
  onApplied: (revision?: number, draftVersion?: number) => void;
  onPreviewReady?: (preview: RewriteSelectionPreview) => void;
  terminology?: 'polish' | 'edit';
  /** Optional layer override for selections opened inside a full-screen modal. */
  portalZIndex?: number;
  requestPreview?: (
    instruction: string,
    selection: ArtifactRewriteSelection,
  ) => Promise<RewriteSelectionPreview>;
}

type FormPhase = 'form' | 'previewing';

function errorCode(error: unknown): string | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const response = (error as {
    response?: {
      data?: {
        code?: unknown;
        error_code?: unknown;
        data?: { code?: unknown; error_code?: unknown };
      };
    };
  }).response;
  const data = response?.data;
  return [data?.data?.error_code, data?.data?.code, data?.error_code, data?.code]
    .find((code): code is string => typeof code === 'string');
}

function errorMessage(code: string | undefined, fallback: string): string {
  if (code === 'REVISION_CONFLICT') return 'errors.revisionConflict';
  if (code === 'SELECTION_AMBIGUOUS') return 'errors.ambiguous';
  if (code === 'SELECTION_STALE') return 'errors.stale';
  if (code === 'SELECTION_UNSUPPORTED') return 'errors.unsupported';
  if (code === 'WORKFLOW_ACTION_FAILED') return 'errors.workflowFailed';
  return fallback;
}

function isReadyPreview(value: unknown): value is RewriteSelectionPreview {
  if (!value || typeof value !== 'object') return false;
  const data = value as RewriteSelectionPreview;
  return data.status === 'ready'
    && data.action === 'rewrite_selection'
    && typeof data.base_revision === 'number'
    && typeof data.preview?.old_text === 'string'
    && typeof data.preview?.new_text === 'string'
    && typeof data.artifact?.content_type === 'string'
    && Boolean(data.artifact.value && ['object', 'string'].includes(typeof data.artifact.value));
}

export function ArtifactRewriteDialog({
  open,
  sessionId,
  slotId,
  listIndex,
  baseRevision,
  baseDraftVersion,
  selection,
  onClose,
  onPreviewReady,
  terminology = 'polish',
  portalZIndex,
  requestPreview: requestPreviewOverride,
}: ArtifactRewriteDialogProps) {
  const { t } = useTranslation();
  const translationPrefix = terminology === 'edit'
    ? 'chat.artifactEdit'
    : 'chat.artifactRewrite';
  const tr = useCallback(
    (key: string) => t(`${translationPrefix}.${key}`),
    [t, translationPrefix],
  );
  const [instruction, setInstruction] = useState('');
  const [phase, setPhase] = useState<FormPhase>('form');
  const [error, setError] = useState<string>();
  const [formStyle, setFormStyle] = useState<CSSProperties>();
  const [formPlacement, setFormPlacement] = useState<SelectionActionAnchor['placement']>('below');
  const formRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const lastFocusRef = useRef<HTMLElement | null>(null);
  const mountedRef = useRef(true);
  const requestIdRef = useRef(0);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      requestIdRef.current += 1;
    };
  }, []);

  useEffect(() => {
    if (!open) return;
    lastFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setInstruction('');
    setError(undefined);
    setPhase('form');
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, [open, selection, tr]);

  const updateFormPosition = useCallback(() => {
    const form = formRef.current;
    if (!form) return;
    const anchor = resolveRewriteFormAnchor(selection);
    const { width, height } = form.getBoundingClientRect();
    if (anchor) {
      setFormPlacement(anchor.placement);
      setFormStyle(computeRewriteFormPosition(anchor, { width, height }));
      return;
    }
    setFormPlacement('below');
    setFormStyle({
      top: clamp(window.innerHeight * 0.32, 16, window.innerHeight - height - 16),
      left: '50%',
      transform: 'translateX(-50%)',
    });
  }, [selection]);

  useLayoutEffect(() => {
    if (!open || !selection) return undefined;
    updateFormPosition();
    window.addEventListener('resize', updateFormPosition);
    window.addEventListener('scroll', updateFormPosition, true);
    return () => {
      window.removeEventListener('resize', updateFormPosition);
      window.removeEventListener('scroll', updateFormPosition, true);
    };
  }, [open, selection, updateFormPosition, instruction, error, phase]);

  const close = useCallback(() => {
    if (phase === 'previewing') return;
    onClose();
    window.setTimeout(() => lastFocusRef.current?.focus(), 0);
  }, [onClose, phase]);

  useEffect(() => {
    if (!open) return undefined;
    const handlePointerDown = (event: MouseEvent) => {
      const target = event.target;
      if (!(target instanceof Node) || formRef.current?.contains(target)) return;
      close();
    };
    document.addEventListener('mousedown', handlePointerDown);
    return () => document.removeEventListener('mousedown', handlePointerDown);
  }, [close, open]);

  const requestPreview = useCallback(async () => {
    const trimmedInstruction = instruction.trim();
    if (!trimmedInstruction || !selection || phase !== 'form') return;

    const requestId = ++requestIdRef.current;
    setPhase('previewing');
    setError(undefined);
    try {
      const result = requestPreviewOverride
        ? await requestPreviewOverride(trimmedInstruction, selection)
        : (await WorkflowSessionApi().previewRewriteSelection(
        sessionId,
        slotId,
        listIndex,
        {
          action: 'rewrite_selection',
          base_revision: baseRevision,
          ...(baseDraftVersion !== undefined ? { base_draft_version: baseDraftVersion } : {}),
          input: {
            instruction: trimmedInstruction,
            ...(selection.type === 'ppt_html'
              ? { selection: {
                  type: 'ppt_html',
                  page: selection.page,
                  el: selection.el,
                  ...(selection.index ? { index: selection.index } : {}),
                  ...(selection.group ? { group: selection.group } : {}),
                  ...(selection.selectedText
                    ? { selected_text: selection.selectedText }
                    : {}),
                  ...(selection.computed_style
                    ? { computed_style: selection.computed_style }
                    : {}),
                } }
              : selection.type === 'ir'
                ? { type: 'ir', selection_ranges: [{ node_id: selection.node_id, selected_text: selection.selectedText }] }
                : { type: 'markdown', selection_ranges: [selection.sourceRange ?? { selected_text: selection.selected_text }] }),
          },
        },
        { silentError: true } as never,
      )).data?.data;
      if (!isReadyPreview(result)) {
        throw new Error('invalid preview response');
      }
      if (!mountedRef.current || requestId !== requestIdRef.current) return;
      if (onPreviewReady) {
        onPreviewReady(result);
      }
      onClose();
    } catch (requestError) {
      if (!mountedRef.current || requestId !== requestIdRef.current) return;
      setError(tr(errorMessage(errorCode(requestError), 'errors.previewFailed')));
      setPhase('form');
    }
  }, [baseDraftVersion, baseRevision, instruction, listIndex, onClose, onPreviewReady, phase, requestPreviewOverride, selection, sessionId, slotId, tr]);

  const handleKeyDown = useCallback((event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      close();
      return;
    }
    if (event.key === 'Enter' && !event.shiftKey && event.target === inputRef.current) {
      event.preventDefault();
      void requestPreview();
    }
  }, [close, requestPreview]);

  if (!open || !selection) return null;
  const busy = phase === 'previewing';
  const canPreview = instruction.trim().length > 0;

  if (busy) {
    const hostSelector = '.workflow-slot__artifact-body, .md-editable-block';
    const progressHost = selection.paragraph?.closest<HTMLElement>(hostSelector)
      ?? lastFocusRef.current?.closest<HTMLElement>(hostSelector);
    return ReactDOM.createPortal(
      <div className={`artifact-rewrite-progress artifact-rewrite-progress--${progressHost ? 'docked' : 'floating'}`}
        role='status' aria-live='polite' aria-atomic='true'
        style={progressHost ? undefined : { ...formStyle, ...(portalZIndex === undefined ? {} : { zIndex: portalZIndex }) }}>
        <span className='artifact-rewrite-progress__spinner' aria-hidden='true' />
        <span>{tr('previewing')}<span className='artifact-rewrite-progress__hint'>{t('chat.writerLocal.loadingCount', { count: selection.type === 'ir' ? selection.nodeSelections?.length ?? 1 : selection.type === 'markdown' ? selection.sourceRanges?.length ?? selection.paragraphs?.length ?? 1 : 1 })}</span></span>
      </div>,
      progressHost ?? document.body,
    );
  }

  return ReactDOM.createPortal(
    <div
      ref={formRef}
      className={`artifact-rewrite-form artifact-rewrite-form--${formPlacement}`}
      style={portalZIndex === undefined ? formStyle : { ...formStyle, zIndex: portalZIndex }}
      onKeyDown={handleKeyDown}
    >
      <div className='artifact-rewrite-form__header'>
        <span className='artifact-rewrite-form__title'><HighlightOutlined aria-hidden />{tr('title')}</span>
        <button type='button' className='artifact-rewrite-form__close' onClick={close} aria-label={tr('close')} title={tr('close')}>
          <CloseOutlined aria-hidden />
        </button>
      </div>
      <div className='artifact-rewrite-form__input-shell'>
        <textarea
          ref={inputRef}
          rows={2}
          className='artifact-rewrite-form__input'
          value={instruction}
          onChange={(event) => setInstruction(event.target.value)}
          placeholder={tr('instructionPlaceholder')}
          aria-label={tr('instruction')}
          aria-describedby={error ? 'artifact-rewrite-form-error' : undefined}
        />
      </div>
      {error && (
        <p id='artifact-rewrite-form-error' className='artifact-rewrite-form__error' role='alert'>
          {error}
        </p>
      )}
      <div className='artifact-rewrite-form__footer'>
        <div className='artifact-rewrite-form__presets'>
          {(['concise', 'fluent', 'formal'] as const).map(preset => <button type='button' key={preset}
            aria-pressed={instruction.trim() === String(t(`chat.writerLocal.${preset}Instruction`))}
            onClick={() => { setInstruction(String(t(`chat.writerLocal.${preset}Instruction`))); inputRef.current?.focus(); }}>{t(`chat.writerLocal.${preset}`)}</button>)}
        </div>
        <button
          type='button'
          className='artifact-rewrite-form__submit'
          onClick={() => void requestPreview()}
          disabled={!canPreview}
          aria-label={tr('preview')}
          title={tr('preview')}
        >
          <ArrowUpOutlined aria-hidden />
        </button>
      </div>
    </div>,
    document.body,
  );
}

interface ArtifactRewriteInlineDiffProps {
  reviewActions?: React.ReactNode;
  showActions?: boolean;
  target: HTMLElement;
  layer: HTMLElement;
  startOffset?: number;
  sessionId: string;
  slotId: string;
  listIndex: number;
  preview: RewriteSelectionPreview;
  onApplied: (revision?: number, draftVersion?: number) => void;
  onReject: () => void;
  applyPreview?: () => Promise<number | undefined>;
}

export function renderInlineDiff(oldText: string, newText: string) {
  return diffWordsWithSpace(oldText, newText).map((part, index) => (
    <span
      className={part.added
        ? 'artifact-rewrite-inline-diff__added'
        : part.removed
          ? 'artifact-rewrite-inline-diff__removed'
          : undefined}
      key={`${part.value}-${index}`}
    >
      {part.value}
    </span>
  ));
}

/** Temporarily renders the proposed changes inside the selected editable block. */
export function ArtifactRewriteInlineDiff({
  reviewActions,
  showActions = true,
  target,
  layer,
  startOffset,
  sessionId,
  slotId,
  listIndex,
  preview,
  onApplied,
  onReject,
  applyPreview,
}: ArtifactRewriteInlineDiffProps) {
  const { t } = useTranslation();
  const [overlay, setOverlay] = useState<HTMLDivElement | null>(null);
  const [overlayStyle, setOverlayStyle] = useState<CSSProperties>();
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string>();
  const baseMarginBottomRef = useRef(0);

  useLayoutEffect(() => {
    const originalContentEditable = target.getAttribute('contenteditable');
    const originalAriaLabel = target.getAttribute('aria-label');
    const originalStyle = target.getAttribute('style');
    baseMarginBottomRef.current = Number.parseFloat(window.getComputedStyle(target).marginBottom) || 0;
    target.classList.add('artifact-rewrite-inline-diff');
    target.setAttribute('contenteditable', 'false');
    target.setAttribute('aria-label', t('chat.artifactRewrite.diffAria'));

    return () => {
      target.classList.remove('artifact-rewrite-inline-diff');
      if (originalContentEditable === null) target.removeAttribute('contenteditable');
      else target.setAttribute('contenteditable', originalContentEditable);
      if (originalAriaLabel === null) target.removeAttribute('aria-label');
      else target.setAttribute('aria-label', originalAriaLabel);
      if (originalStyle === null) target.removeAttribute('style');
      else target.setAttribute('style', originalStyle);
    };
  }, [target, t]);

  const targetText = target.textContent ?? '';
  const selectedTextAtOffset = typeof startOffset === 'number'
    && targetText.slice(startOffset, startOffset + preview.preview.old_text.length) === preview.preview.old_text;
  const start = selectedTextAtOffset ? startOffset : targetText.indexOf(preview.preview.old_text);
  const before = start >= 0 ? targetText.slice(0, start) : '';
  const after = start >= 0
    ? targetText.slice(start + preview.preview.old_text.length)
    : '';

  useLayoutEffect(() => {
    if (!overlay) return;
    const container = layer.parentElement;
    const surface = target.closest('.writer-markdown-editor__surface, .writer-ir__document--editable');
    if (!container || !surface) return;

    let frameId: number | undefined;
    const updatePosition = () => {
      const targetRect = target.getBoundingClientRect();
      const containerRect = container.getBoundingClientRect();
      const computed = window.getComputedStyle(target);
      setOverlayStyle({
        top: targetRect.top - containerRect.top,
        left: targetRect.left - containerRect.left,
        width: targetRect.width,
        fontFamily: computed.fontFamily,
        fontSize: computed.fontSize,
        fontWeight: computed.fontWeight,
        letterSpacing: computed.letterSpacing,
        lineHeight: computed.lineHeight,
        textAlign: computed.textAlign as CSSProperties['textAlign'],
      });
      const extraHeight = Math.max(0, overlay.getBoundingClientRect().height - targetRect.height);
      target.style.marginBottom = `${baseMarginBottomRef.current + extraHeight}px`;
    };
    const schedulePosition = () => {
      if (frameId !== undefined) window.cancelAnimationFrame(frameId);
      frameId = window.requestAnimationFrame(updatePosition);
    };
    const resizeObserver = new ResizeObserver(schedulePosition);
    resizeObserver.observe(target);
    resizeObserver.observe(overlay);
    resizeObserver.observe(container);
    const mutationObserver = new MutationObserver(schedulePosition);
    mutationObserver.observe(surface, { childList: true, characterData: true, subtree: true });
    surface.addEventListener('scroll', schedulePosition, { passive: true });
    window.addEventListener('resize', schedulePosition);
    schedulePosition();

    return () => {
      if (frameId !== undefined) window.cancelAnimationFrame(frameId);
      resizeObserver.disconnect();
      mutationObserver.disconnect();
      surface.removeEventListener('scroll', schedulePosition);
      window.removeEventListener('resize', schedulePosition);
    };
  }, [layer, overlay, target]);

  const apply = useCallback(async () => {
    if (applying) return;
    const restoreScroll = captureRewriteScrollAnchor([target]);
    setApplying(true);
    setError(undefined);
    try {
      if (applyPreview) {
        const revision = await applyPreview();
        onApplied(revision);
        restoreScroll();
        return;
      }
      if (preview.commit?.token) {
        const response = await WorkflowSessionApi().executeArtifactAction(sessionId, slotId, listIndex, {
          action: 'rewrite_selection', base_revision: preview.base_revision,
          ...(preview.base_draft_version !== undefined ? { base_draft_version: preview.base_draft_version } : {}),
          input: { commit_token: preview.commit.token },
        });
        if (response.data?.code !== 0 || response.data.data?.status !== 'applied') {
          throw new Error('invalid commit response');
        }
        onApplied(response.data.data.revision, response.data.data.draft_version);
        restoreScroll();
        return;
      }
      const response = await WorkflowSessionApi().patchSlotItem(
        sessionId,
        slotId,
        listIndex,
        preview.artifact.value,
        preview.artifact.content_type,
        ['draft_document', 'flat_draft_document'].includes(slotId) ? 'draft' : 'checkpoint',
        preview.base_revision,
        preview.base_draft_version,
        { silentError: true } as never,
      );
      const result = response?.data?.data;
      if (
        response?.data?.code !== 0
        || result?.type !== 'slot_item_patched'
        || typeof result.revision !== 'number'
        || typeof result.draft_version !== 'number'
      ) {
        throw new Error('invalid patch response');
      }
      onApplied(result.revision, result.draft_version);
      restoreScroll();
    } catch (applyError) {
      setError(t(errorMessage(errorCode(applyError), 'chat.artifactRewrite.errors.applyFailed')));
      setApplying(false);
    }
  }, [applyPreview, applying, listIndex, onApplied, preview, sessionId, slotId, t, target]);

  if (!layer) return null;
  return ReactDOM.createPortal(
    <div ref={setOverlay} className='artifact-rewrite-inline-diff__overlay' style={overlayStyle}>
      <div className='artifact-rewrite-inline-diff__content' aria-live='polite'>
        {before}
        {renderInlineDiff(preview.preview.old_text, preview.preview.new_text)}
        {after}
      </div>
      {reviewActions}
      {showActions && <div className='artifact-rewrite-inline-diff__actions'>
        {error && <p className='artifact-rewrite-inline-diff__error' role='alert'>{error}</p>}
        <button type='button' onClick={onReject} disabled={applying}>
          {t('chat.artifactRewrite.reject')}
        </button>
        <button
          type='button'
          className='artifact-rewrite-inline-diff__apply'
          onClick={() => void apply()}
          disabled={applying}
        >
          {applying ? t('chat.artifactRewrite.applying') : t('chat.artifactRewrite.apply')}
        </button>
      </div>}
    </div>,
    layer,
  );
}
