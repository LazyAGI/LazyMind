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
import {
  WorkflowSessionApi,
  type RewriteSelection,
  type RewriteSelectionPreview,
} from '@/modules/chat/utils/request';
import type { SelectionActionAnchor } from './artifactRewriteSelection';
import './ArtifactRewriteDialog.scss';

export type ArtifactRewriteSelection = RewriteSelection & {
  selectedText: string;
  anchor?: SelectionActionAnchor;
  paragraph?: HTMLElement;
  startOffset?: number;
};

interface ArtifactRewriteDialogProps {
  open: boolean;
  sessionId: string;
  slotId: string;
  listIndex: number;
  baseRevision: number;
  selection: ArtifactRewriteSelection | null;
  onClose: () => void;
  onApplied: (revision?: number) => void;
  onPreviewReady?: (preview: RewriteSelectionPreview) => void;
}

type DialogPhase = 'form' | 'previewing' | 'ready' | 'applying';

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
  const code = data?.error_code ?? data?.code ?? data?.data?.error_code ?? data?.data?.code;
  return typeof code === 'string' ? code : undefined;
}

function errorMessage(code: string | undefined, fallback: string): string {
  if (code === 'REVISION_CONFLICT') return 'chat.artifactRewrite.errors.revisionConflict';
  if (code === 'SELECTION_AMBIGUOUS') return 'chat.artifactRewrite.errors.ambiguous';
  if (code === 'SELECTION_STALE') return 'chat.artifactRewrite.errors.stale';
  if (code === 'SELECTION_UNSUPPORTED') return 'chat.artifactRewrite.errors.unsupported';
  if (code === 'WORKFLOW_ACTION_FAILED') return 'chat.artifactRewrite.errors.pluginFailed';
  return fallback;
}

function renderDiff(oldText: string, newText: string) {
  return diffWordsWithSpace(oldText, newText).map((part, index) => (
    <span
      className={part.added
        ? 'artifact-rewrite-dialog__diff-addition'
        : part.removed
          ? 'artifact-rewrite-dialog__diff-removal'
          : undefined}
      key={`${part.value}-${index}`}
    >
      {part.value}
    </span>
  ));
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
    && Boolean(data.artifact.value && typeof data.artifact.value === 'object');
}

export function ArtifactRewriteDialog({
  open,
  sessionId,
  slotId,
  listIndex,
  baseRevision,
  selection,
  onClose,
  onApplied,
  onPreviewReady,
}: ArtifactRewriteDialogProps) {
  const { t } = useTranslation();
  const [instruction, setInstruction] = useState('');
  const [preview, setPreview] = useState<RewriteSelectionPreview | null>(null);
  const [phase, setPhase] = useState<DialogPhase>('form');
  const [error, setError] = useState<string>();
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
    setInstruction(t('chat.artifactRewrite.defaultInstruction'));
    setPreview(null);
    setError(undefined);
    setPhase('form');
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, [open, selection, t]);

  const close = useCallback(() => {
    if (phase === 'previewing' || phase === 'applying') return;
    onClose();
    window.setTimeout(() => lastFocusRef.current?.focus(), 0);
  }, [onClose, phase]);

  const requestPreview = useCallback(async () => {
    const trimmedInstruction = instruction.trim();
    if (!selection || !trimmedInstruction || phase !== 'form') return;

    const requestId = ++requestIdRef.current;
    setPhase('previewing');
    setError(undefined);
    try {
      const response = await WorkflowSessionApi().previewRewriteSelection(
        sessionId,
        slotId,
        listIndex,
        {
          action: 'rewrite_selection',
          base_revision: baseRevision,
          input: {
            instruction: trimmedInstruction,
            selection: selection.type === 'ir'
              ? { type: 'ir', node_id: selection.node_id }
              : { type: 'markdown', selected_text: selection.selected_text },
          },
        },
        { silentError: true } as never,
      );
      const result = response?.data?.data;
      if (response?.data?.code !== 0 || !isReadyPreview(result)) {
        throw new Error('invalid preview response');
      }
      if (!mountedRef.current || requestId !== requestIdRef.current) return;
      if (onPreviewReady) {
        onPreviewReady(result);
        onClose();
        return;
      }
      setPreview(result);
      setPhase('ready');
    } catch (requestError) {
      if (!mountedRef.current || requestId !== requestIdRef.current) return;
      setError(t(errorMessage(errorCode(requestError), 'chat.artifactRewrite.errors.previewFailed')));
      setPhase('form');
    }
  }, [baseRevision, instruction, listIndex, onClose, onPreviewReady, phase, selection, sessionId, slotId, t]);

  const applyPreview = useCallback(async () => {
    if (!preview || phase !== 'ready') return;
    const requestId = ++requestIdRef.current;
    setPhase('applying');
    setError(undefined);
    try {
      const response = await WorkflowSessionApi().patchSlotItem(
        sessionId,
        slotId,
        listIndex,
        preview.artifact.value,
        preview.artifact.content_type,
        'checkpoint',
        preview.base_revision,
        { silentError: true } as never,
      );
      const result = response?.data?.data;
      if (response?.data?.code !== 0 || result?.type !== 'slot_item_patched') {
        throw new Error('invalid patch response');
      }
      if (!mountedRef.current || requestId !== requestIdRef.current) return;
      onApplied(typeof result.revision === 'number' ? result.revision : undefined);
      onClose();
      window.setTimeout(() => lastFocusRef.current?.focus(), 0);
    } catch (applyError) {
      if (!mountedRef.current || requestId !== requestIdRef.current) return;
      setError(t(errorMessage(errorCode(applyError), 'chat.artifactRewrite.errors.applyFailed')));
      setPhase('ready');
    }
  }, [listIndex, onApplied, onClose, phase, preview, sessionId, slotId, t]);

  const handleKeyDown = useCallback((event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      close();
    }
  }, [close]);

  if (!open || !selection) return null;
  const busy = phase === 'previewing' || phase === 'applying';
  const canPreview = phase === 'form' && instruction.trim().length > 0;
  const maxTop = Math.max(16, window.innerHeight - 320);
  const horizontalInset = Math.min(260, Math.max(16, (window.innerWidth - 32) / 2));
  const dialogStyle: CSSProperties = selection.anchor
    ? {
      top: Math.min(Math.max(16, selection.anchor.top + 10), maxTop),
      left: Math.min(
        Math.max(horizontalInset, selection.anchor.left),
        window.innerWidth - horizontalInset,
      ),
    }
    : { top: 24, left: '50%' };

  return ReactDOM.createPortal(
    <div
      className='artifact-rewrite-dialog'
      role='dialog'
      aria-labelledby='artifact-rewrite-dialog-title'
      style={dialogStyle}
      onKeyDown={handleKeyDown}
    >
      <header className='artifact-rewrite-dialog__header'>
        <h2 id='artifact-rewrite-dialog-title'>{t('chat.artifactRewrite.title')}</h2>
        <button type='button' onClick={close} disabled={busy} aria-label={t('chat.artifactRewrite.close')}>×</button>
      </header>

      <div className='artifact-rewrite-dialog__body'>
        {preview ? (
          <div className='artifact-rewrite-dialog__diff' aria-live='polite'>
            <div className='artifact-rewrite-dialog__diff-summary'>
              <span className='artifact-rewrite-dialog__diff-label artifact-rewrite-dialog__diff-label--before'>
                {t('chat.artifactRewrite.before')}
              </span>
              <span className='artifact-rewrite-dialog__diff-label artifact-rewrite-dialog__diff-label--after'>
                {t('chat.artifactRewrite.after')}
              </span>
            </div>
            <div className='artifact-rewrite-dialog__diff-content'>
              {renderDiff(preview.preview.old_text, preview.preview.new_text)}
            </div>
          </div>
        ) : (
          <label className='artifact-rewrite-dialog__instruction'>
            <span>{t('chat.artifactRewrite.instruction')}</span>
            <textarea
              ref={inputRef}
              value={instruction}
              onChange={(event) => setInstruction(event.target.value)}
              placeholder={t('chat.artifactRewrite.instructionPlaceholder')}
              disabled={busy}
              aria-describedby={error ? 'artifact-rewrite-dialog-error' : undefined}
            />
          </label>
        )}
        {error && <p id='artifact-rewrite-dialog-error' className='artifact-rewrite-dialog__error' role='alert'>{error}</p>}
      </div>

      <footer className='artifact-rewrite-dialog__footer'>
        <button type='button' onClick={close} disabled={busy}>
          {preview ? t('chat.artifactRewrite.reject') : t('common.cancel')}
        </button>
        {preview ? (
          <button type='button' className='artifact-rewrite-dialog__primary' onClick={() => void applyPreview()} disabled={busy}>
            {phase === 'applying' ? t('chat.artifactRewrite.applying') : t('chat.artifactRewrite.apply')}
          </button>
        ) : (
          <button type='button' className='artifact-rewrite-dialog__primary' onClick={() => void requestPreview()} disabled={!canPreview || busy}>
            {phase === 'previewing' ? t('chat.artifactRewrite.previewing') : t('chat.artifactRewrite.preview')}
          </button>
        )}
      </footer>
    </div>,
    document.body,
  );
}

interface ArtifactRewriteInlineDiffProps {
  target: HTMLElement;
  layer: HTMLElement;
  startOffset?: number;
  sessionId: string;
  slotId: string;
  listIndex: number;
  preview: RewriteSelectionPreview;
  onApplied: (revision?: number) => void;
  onReject: () => void;
}

function renderInlineDiff(oldText: string, newText: string) {
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
  target,
  layer,
  startOffset,
  sessionId,
  slotId,
  listIndex,
  preview,
  onApplied,
  onReject,
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
    setApplying(true);
    setError(undefined);
    try {
      const response = await WorkflowSessionApi().patchSlotItem(
        sessionId,
        slotId,
        listIndex,
        preview.artifact.value,
        preview.artifact.content_type,
        'checkpoint',
        preview.base_revision,
        { silentError: true } as never,
      );
      const result = response?.data?.data;
      if (response?.data?.code !== 0 || result?.type !== 'slot_item_patched') {
        throw new Error('invalid patch response');
      }
      onApplied(typeof result.revision === 'number' ? result.revision : undefined);
    } catch (applyError) {
      setError(t(errorMessage(errorCode(applyError), 'chat.artifactRewrite.errors.applyFailed')));
      setApplying(false);
    }
  }, [applying, listIndex, onApplied, preview, sessionId, slotId, t]);

  if (!layer) return null;
  return ReactDOM.createPortal(
    <div ref={setOverlay} className='artifact-rewrite-inline-diff__overlay' style={overlayStyle}>
      <div className='artifact-rewrite-inline-diff__content' aria-live='polite'>
        {before}
        {renderInlineDiff(preview.preview.old_text, preview.preview.new_text)}
        {after}
      </div>
      <div className='artifact-rewrite-inline-diff__actions'>
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
      </div>
    </div>,
    layer,
  );
}
