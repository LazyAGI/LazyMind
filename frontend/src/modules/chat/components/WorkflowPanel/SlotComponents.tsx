import { useState, useCallback, useRef, useEffect, useMemo, createContext, useContext } from "react";
import ReactDOM from "react-dom";
import type { SlotRevision, SlotVersionEntry } from "@/modules/chat/store/workflowPanel";
import { useWorkflowStore, draftStore } from "@/modules/chat/store/workflowPanel";
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from "@/modules/knowledge/utils/imageUrl";
import { buildDiffLinesWithInline } from "@/modules/memory/shared";
import { DiffLineContent } from "@/modules/memory/components/DiffLineContent";
import { uploadFileInChunks } from "@/modules/chat/utils/chunkUpload";
import { WorkflowSessionApi, type RewriteSelectionPreview } from "@/modules/chat/utils/request";
import { FilePreviewDrawer } from "./FilePreviewDrawer";
import {
  WriterArtifactContent,
  WRITER_ARTIFACT_SLOT_IDS,
  unwrapArtifactPayload,
} from './writerArtifactViews';
import { WriterIRControl, type WriterIRSaveMode, type WriterIRSaveResult } from './WriterIRControl';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import {
  ArtifactRewriteDialog,
  type ArtifactRewriteSelection,
} from './ArtifactRewriteDialog';
import { ArtifactRewriteSelectionAction } from './ArtifactRewriteSelectionAction';
import { selectedMarkdownParagraph, type MarkdownSelection } from './artifactRewriteSelection';
import {
  isWriterDocument,
  normalizeWriterDocumentForSync,
  restoreLegacyWriterImageReference,
  updateWriterBlockContent,
  type WriterBlock,
  type WriterDocument,
} from './writerIR';
import { PluginPanelTabActiveContext, SlotEditingContext } from './slotEditingContext';
import MarkdownViewer from '@/modules/chat/components/MarkdownViewer';
import i18n from '@/i18n';
import { useTranslation } from 'react-i18next';
import { localizeErrorCode } from '@/components/request';
import type { TaskArtifactStream } from '@/modules/chat/store/taskCenter';

export { SlotEditingContext } from './slotEditingContext';
export type { SlotEditingContextValue } from './slotEditingContext';

function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options);
}

export const SlotDownloadContext = createContext(true);

/**
 * Normalize the content_type returned by the Python backend.
 * Python stores short forms: 'text', 'json', 'image', 'file', 'file_list'.
 */
function normalizeContentType(ct: string): 'image' | 'file' | 'text' {
  if (ct === 'image' || ct.startsWith('image/')) return 'image';
  if (ct === 'file' || ct === 'file_list' || ct.startsWith('application/')) return 'file';
  return 'text';
}

/** True when the URL can be used directly as an <img src> in the browser. */
function isBrowserReadyImageUrl(url: string): boolean {
  const trimmed = (url || '').trim();
  if (!trimmed) return false;
  if (trimmed.startsWith('data:image/')) return true;
  if (/^https?:\/\//i.test(trimmed)) return true;
  return trimmed.includes('/api/core/static-files/') || trimmed.includes('/static-files/');
}

function preloadImageUrl(src: string): Promise<boolean> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(true);
    img.onerror = () => resolve(false);
    img.src = src;
  });
}

const SLOT_IMAGE_PRELOAD_RETRIES = 4;
const SLOT_IMAGE_PRELOAD_RETRY_MS = 800;
const MEDIA_LIBRARY_LOAD_RETRIES = 4;
const MEDIA_LIBRARY_LOAD_RETRY_MS = 800;

/**
 * Resolve a slot image URL and preload it before display.
 * Avoids flashing a broken <img> when the API returns a signed URL before the file exists.
 */
function useSlotImageUrl(raw: Record<string, unknown> | undefined) {
  const pathForSign = String(raw?.path ?? raw?.url ?? '').trim();
  const apiUrlRaw = raw?.url ? String(raw.url).trim() : '';
  const [displayUrl, setDisplayUrl] = useState('');
  const [pending, setPending] = useState(Boolean(pathForSign));

  useEffect(() => {
    if (!pathForSign) {
      setDisplayUrl('');
      setPending(false);
      return;
    }

    let cancelled = false;

    async function resolveCandidate(): Promise<string> {
      const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
      if (apiUrl && isBrowserReadyImageUrl(apiUrl) && !isExpiredSignedUrl(apiUrl)) {
        return apiUrl;
      }
      const signed = await resolveMarkdownImageUrlAsync(pathForSign);
      return isBrowserReadyImageUrl(signed) ? signed : '';
    }

    async function load() {
      setPending(true);
      setDisplayUrl('');
      let candidate = await resolveCandidate();
      if (!candidate || cancelled) {
        if (!cancelled) setPending(false);
        return;
      }

      for (let attempt = 0; attempt < SLOT_IMAGE_PRELOAD_RETRIES && !cancelled; attempt++) {
        if (await preloadImageUrl(candidate)) {
          if (!cancelled) {
            setDisplayUrl(candidate);
            setPending(false);
          }
          return;
        }
        if (attempt + 1 >= SLOT_IMAGE_PRELOAD_RETRIES) break;
        await new Promise((r) => setTimeout(r, SLOT_IMAGE_PRELOAD_RETRY_MS));
        candidate = await resolveMarkdownImageUrlAsync(pathForSign);
        if (!isBrowserReadyImageUrl(candidate)) break;
      }

      if (!cancelled) {
        setDisplayUrl('');
        setPending(false);
      }
    }

    load();
    return () => {
      cancelled = true;
    };
  }, [pathForSign, apiUrlRaw]);

  return { displayUrl, pending, hasSource: Boolean(pathForSign) };
}

function useArtifactFileUrl(
  raw: Record<string, unknown> | undefined,
  refreshToken: string | number = 0,
) {
  const pathForSign = String(raw?.path ?? raw?.url ?? '').trim();
  const apiUrlRaw = raw?.url ? String(raw.url).trim() : '';
  const stablePath = raw?.path
    ? String(raw.path).trim()
    : apiUrlRaw.split(/[?#]/, 1)[0];
  const sourceKey = `${stablePath}\n${refreshToken}`;
  const [url, setUrl] = useState('');
  const [resolving, setResolving] = useState(Boolean(pathForSign));
  const [resolvedSourceKey, setResolvedSourceKey] = useState(
    pathForSign ? '' : sourceKey,
  );

  useEffect(() => {
    if (!pathForSign) {
      setUrl('');
      setResolving(false);
      setResolvedSourceKey(sourceKey);
      return;
    }

    let cancelled = false;
    setResolving(true);

    async function resolveCandidate(): Promise<string> {
      const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
      if (apiUrl && !isExpiredSignedUrl(apiUrl)) {
        return apiUrl;
      }
      return resolveMarkdownImageUrlAsync(pathForSign);
    }

    resolveCandidate()
      .then((resolved) => {
        if (!cancelled) {
          setUrl(resolved);
          setResolvedSourceKey(sourceKey);
          setResolving(false);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setUrl('');
          setResolvedSourceKey(sourceKey);
          setResolving(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [apiUrlRaw, pathForSign, refreshToken, sourceKey]);

  const sourceResolved = resolvedSourceKey === sourceKey;
  return {
    url: sourceResolved ? url : '',
    resolving: resolving || !sourceResolved,
    hasSource: Boolean(pathForSign),
    sourceKey,
  };
}

function useWriterMediaLibrary(sessionId?: string): unknown {
  const sessionByConversation = useWorkflowStore((state) => state.sessionByConversation);
  const mediaAssetSlot = useMemo(() => Object.values(sessionByConversation)
    .find((session) => session?.session_id === sessionId)
    ?.slots
    ?.find((candidate) => candidate.slot_id === 'resolved_media_assets' && candidate.selected), [
      sessionByConversation,
      sessionId,
    ]);
  const [fetchedMediaAssetSlot, setFetchedMediaAssetSlot] = useState<SlotRevision>();
  const selectedMediaAssetSlot = mediaAssetSlot ?? fetchedMediaAssetSlot;

  useEffect(() => {
    if (mediaAssetSlot || !sessionId) {
      setFetchedMediaAssetSlot(undefined);
      return;
    }
    let cancelled = false;
    WorkflowSessionApi().getSlots(sessionId)
      .then((response) => {
        const slot = (response?.data?.data?.slots ?? []).find(
          (candidate: SlotRevision) => candidate.slot_id === 'resolved_media_assets' && candidate.selected,
        );
        if (!cancelled) setFetchedMediaAssetSlot(slot);
      })
      .catch(() => {
        if (!cancelled) setFetchedMediaAssetSlot(undefined);
      });
    return () => { cancelled = true; };
  }, [mediaAssetSlot, sessionId]);
  const {
    url: mediaLibraryUrl,
    resolving: mediaLibraryResolving,
    hasSource: hasMediaLibrary,
  } = useArtifactFileUrl(
    selectedMediaAssetSlot?.artifact_value,
    selectedMediaAssetSlot?.revision ?? 0,
  );
  const [mediaLibrary, setMediaLibrary] = useState<unknown>(null);

  useEffect(() => {
    if (!hasMediaLibrary || mediaLibraryResolving || !mediaLibraryUrl) {
      setMediaLibrary(null);
      return;
    }
    const controller = new AbortController();

    async function loadMediaLibrary() {
      for (let attempt = 0; attempt < MEDIA_LIBRARY_LOAD_RETRIES; attempt += 1) {
        try {
          const response = await fetch(mediaLibraryUrl, { signal: controller.signal });
          if (!response.ok) throw new Error('media library load failed');
          const json = await response.json();
          if (!controller.signal.aborted) {
            setMediaLibrary(unwrapArtifactPayload(json));
          }
          return;
        } catch (fetchError: unknown) {
          if (controller.signal.aborted || (fetchError instanceof DOMException && fetchError.name === 'AbortError')) {
            return;
          }
          if (attempt + 1 < MEDIA_LIBRARY_LOAD_RETRIES) {
            await new Promise((resolve) => window.setTimeout(resolve, MEDIA_LIBRARY_LOAD_RETRY_MS));
          }
        }
      }
      if (!controller.signal.aborted) setMediaLibrary(null);
    }

    void loadMediaLibrary();
    return () => controller.abort();
  }, [hasMediaLibrary, mediaLibraryResolving, mediaLibraryUrl]);

  return mediaLibrary;
}

function isSpaFallbackHtml(content: string): boolean {
  const normalized = content.trim().toLowerCase();
  return normalized.startsWith('<!doctype html')
    && (normalized.includes('/@vite/client') || normalized.includes('id="root"'));
}

export function isWriterIrSource(source: unknown): boolean {
  const normalized = String(source ?? '')
    .split(/[?#]/, 1)[0]
    .toLowerCase();
  return normalized.endsWith('.lmd') || normalized.endsWith('_ir.json');
}

/** Shown when the slot has no artifact yet (backend returned no artifact_value). */
function SlotPending({ type, cardMode }: { type: 'image' | 'file' | 'text'; cardMode?: boolean }) {
  if (type === 'image') {
    return (
      <div className={`workflow-slot workflow-slot--image workflow-slot--pending${cardMode ? ' workflow-slot--image-card' : ''}`}>
        <span className='workflow-slot__placeholder-icon' aria-hidden='true'>🖼</span>
        <span className='workflow-slot__placeholder'>{tr('chat.slots.inProgress')}</span>
      </div>
    );
  }
  if (type === 'file') {
    return (
      <div className='workflow-slot workflow-slot--file workflow-slot--pending'>
        <span className='workflow-slot__placeholder'>{tr('chat.slots.pendingGeneration')}</span>
      </div>
    );
  }
  return (
    <div className='workflow-slot workflow-slot--text workflow-slot--pending'>
      <p className='workflow-slot__text workflow-slot__text--pending'>{tr('chat.slots.pendingCalculation')}</p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// TextDiffView — 复用 memory 模块的 buildDiffLines 和样式渲染 diff 块
// ---------------------------------------------------------------------------

interface TextDiffViewProps {
  currentText: string;
  otherText: string;
  otherLabel: string;
  /** When true, otherText is the newer version (green) and currentText is the older one (red). */
  reversed?: boolean;
}

function TextDiffView({ currentText, otherText, otherLabel, reversed }: TextDiffViewProps) {
  const diffLines = useMemo(
    () => reversed
      ? buildDiffLinesWithInline(currentText, otherText)
      : buildDiffLinesWithInline(otherText, currentText),
    [currentText, otherText, reversed],
  );

  return (
    <div className='workflow-slot__version-diff'>
      <div className='workflow-slot__version-diff-header'>
        {reversed ? (
          <>
            <span className='workflow-slot__version-diff-label workflow-slot__version-diff-label--remove'>
              {tr('chat.slots.currentVersion')}
            </span>
            <span className='workflow-slot__version-diff-label workflow-slot__version-diff-label--add'>
              {otherLabel}
            </span>
          </>
        ) : (
          <>
            <span className='workflow-slot__version-diff-label workflow-slot__version-diff-label--remove'>
              {otherLabel}
            </span>
            <span className='workflow-slot__version-diff-label workflow-slot__version-diff-label--add'>
              {tr('chat.slots.currentVersion')}
            </span>
          </>
        )}
      </div>
      <div className='workflow-slot__version-diff-body'>
        {diffLines.map((line, index) => (
          <div
            key={`${index}-${line.type}-${line.text.slice(0, 20)}`}
            className={`memory-diff-line is-${line.type}`}
          >
            <span className='memory-diff-prefix'>
              {line.type === 'add' ? '+' : line.type === 'remove' ? '-' : ' '}
            </span>
            <DiffLineContent line={line} />
          </div>
        ))}
        {diffLines.length === 0 && (
          <div className='workflow-slot__version-diff-empty'>{tr('chat.slots.identicalContent')}</div>
        )}
      </div>
    </div>
  );
}

const snapshotDiffTextCache = new Map<string, Promise<string>>();

function snapshotDiffCacheKey(snapshot: unknown): string {
  if (snapshot == null) return '';
  if (typeof snapshot === 'string') return `s:${snapshot}`;
  if (typeof snapshot !== 'object') return `v:${String(snapshot)}`;
  const record = snapshot as Record<string, unknown>;
  if (record.url || record.path) {
    return `f:${String(record.url ?? '')}\n${String(record.path ?? '')}\n${String(record.size ?? '')}\n${String(record.filename ?? record.name ?? '')}`;
  }
  try {
    return `j:${JSON.stringify(snapshot)}`;
  } catch {
    return `o:${Object.keys(record).join(',')}`;
  }
}

function formatPayloadForDiff(payload: unknown): string {
  const unwrapped = unwrapArtifactPayload(payload);
  if (isWriterDocument(unwrapped)) {
    return writerDocumentToMarkdown(unwrapped);
  }
  if (isWriterDocument(payload)) {
    return writerDocumentToMarkdown(payload);
  }
  if (typeof unwrapped === 'string') return unwrapped;
  if (unwrapped != null) return JSON.stringify(unwrapped, null, 2);
  if (typeof payload === 'string') return payload;
  return payload == null ? '' : JSON.stringify(payload, null, 2);
}

async function resolveSnapshotDiffText(snapshot: unknown): Promise<string> {
  if (snapshot == null) return '';
  if (typeof snapshot === 'string') {
    const trimmed = snapshot.trim();
    if (
      trimmed.startsWith('{')
      || trimmed.startsWith('[')
      || (!trimmed.includes('/static-files/')
        && !trimmed.includes('/api/core/')
        && !trimmed.startsWith('http')
        && !trimmed.startsWith('/var/'))
    ) {
      return snapshot;
    }
    const fetchUrl = trimmed.includes('/static-files/') || trimmed.startsWith('http')
      ? resolveCoreAssetUrl(trimmed)
      : await resolveMarkdownImageUrlAsync(trimmed);
    if (!fetchUrl) return snapshot;
    const response = await fetch(fetchUrl);
    if (!response.ok) throw new Error(localizeErrorCode('2000509'));
    const contentType = response.headers.get('content-type') || '';
    if (
      contentType.includes('json')
      || isWriterIrSource(trimmed)
      || trimmed.toLowerCase().includes('.json')
    ) {
      return formatPayloadForDiff(await response.json());
    }
    const text = await response.text();
    if (isSpaFallbackHtml(text)) throw new Error(localizeErrorCode('2000509'));
    return text;
  }

  if (typeof snapshot !== 'object') return String(snapshot);
  const record = snapshot as Record<string, unknown>;
  if (record.text !== undefined) return String(record.text);
  if (record.data !== undefined) {
    return typeof record.data === 'string'
      ? record.data
      : JSON.stringify(record.data, null, 2);
  }
  if (isWriterDocument(record) || isWriterDocument(unwrapArtifactPayload(record))) {
    return formatPayloadForDiff(record);
  }

  const pathForSign = String(record.url ?? record.path ?? '').trim();
  if (!pathForSign) return JSON.stringify(record, null, 2);

  const apiUrl = record.url ? resolveCoreAssetUrl(String(record.url)) : '';
  const fetchUrl = apiUrl && !isExpiredSignedUrl(apiUrl)
    ? apiUrl
    : await resolveMarkdownImageUrlAsync(pathForSign);
  if (!fetchUrl) throw new Error(localizeErrorCode('2000509'));

  const response = await fetch(fetchUrl);
  if (!response.ok) throw new Error(localizeErrorCode('2000509'));
  const contentType = response.headers.get('content-type') || '';
  const looksJson = contentType.includes('json')
    || isWriterIrSource(pathForSign)
    || isWriterIrSource(record.filename)
    || pathForSign.toLowerCase().includes('.json')
    || String(record.type ?? '').toLowerCase() === 'json'
    || String(record.filename ?? '').toLowerCase().endsWith('.json');
  if (looksJson) {
    return formatPayloadForDiff(await response.json());
  }
  const text = await response.text();
  if (isSpaFallbackHtml(text)) throw new Error(localizeErrorCode('2000509'));
  return text;
}

function loadSnapshotDiffText(snapshot: unknown): Promise<string> {
  const key = snapshotDiffCacheKey(snapshot);
  if (!key) return Promise.resolve('');
  const cached = snapshotDiffTextCache.get(key);
  if (cached) return cached;
  const pending = resolveSnapshotDiffText(snapshot).catch((error) => {
    snapshotDiffTextCache.delete(key);
    throw error;
  });
  snapshotDiffTextCache.set(key, pending);
  return pending;
}

function useResolvedSnapshotText(snapshot: unknown) {
  const [text, setText] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    loadSnapshotDiffText(snapshot)
      .then((resolved) => {
        if (!cancelled) {
          setText(resolved);
          setLoading(false);
        }
      })
      .catch((loadError: unknown) => {
        if (!cancelled) {
          setText('');
          setLoading(false);
          setError(
            loadError instanceof Error ? loadError.message : localizeErrorCode('2000509'),
          );
        }
      });
    return () => {
      cancelled = true;
    };
  }, [snapshot]);

  return { text, loading, error };
}

function SnapshotTextPreview({ snapshot }: { snapshot: unknown }) {
  const { text, loading, error } = useResolvedSnapshotText(snapshot);
  if (loading) {
    return <div className='workflow-slot__version-compare-hint'>{tr('common.loading')}</div>;
  }
  if (error) {
    return <div className='workflow-slot__version-compare-hint'>{error || tr('chat.slots.contentLoadFailed')}</div>;
  }
  return (
    <pre className='workflow-slot__version-current-text'>
      {text || tr('chat.slots.noContent')}
    </pre>
  );
}

interface SnapshotTextDiffViewProps {
  currentSnapshot: unknown;
  otherSnapshot?: unknown;
  otherText?: string;
  otherLabel: string;
  reversed?: boolean;
}

function SnapshotTextDiffView({
  currentSnapshot,
  otherSnapshot,
  otherText,
  otherLabel,
  reversed,
}: SnapshotTextDiffViewProps) {
  const [currentText, setCurrentText] = useState('');
  const [resolvedOtherText, setResolvedOtherText] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    Promise.all([
      loadSnapshotDiffText(currentSnapshot),
      otherText !== undefined
        ? Promise.resolve(otherText)
        : loadSnapshotDiffText(otherSnapshot),
    ])
      .then(([current, other]) => {
        if (!cancelled) {
          setCurrentText(current);
          setResolvedOtherText(other);
          setLoading(false);
        }
      })
      .catch((loadError: unknown) => {
        if (!cancelled) {
          setCurrentText('');
          setResolvedOtherText('');
          setLoading(false);
          setError(
            loadError instanceof Error ? loadError.message : localizeErrorCode('2000509'),
          );
        }
      });
    return () => {
      cancelled = true;
    };
  }, [currentSnapshot, otherSnapshot, otherText]);

  if (loading) {
    return <div className='workflow-slot__version-compare-hint'>{tr('common.loading')}</div>;
  }
  if (error) {
    return <div className='workflow-slot__version-compare-hint'>{error || tr('chat.slots.contentLoadFailed')}</div>;
  }
  return (
    <TextDiffView
      currentText={currentText}
      otherText={resolvedOtherText}
      otherLabel={otherLabel}
      reversed={reversed}
    />
  );
}

// ---------------------------------------------------------------------------
// Global version popover state — only one popover open at a time.
// ---------------------------------------------------------------------------

type PopoverKey = string; // `${sessionId}:${slotId}:${listIndex}`
let _openPopoverKey: PopoverKey | null = null;
const _popoverListeners = new Set<() => void>();

function _notifyPopoverListeners() {
  _popoverListeners.forEach((fn) => fn());
}

function useGlobalPopoverOpen(key: PopoverKey): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const listener = () => {
      setOpen(_openPopoverKey === key);
    };
    _popoverListeners.add(listener);
    return () => { _popoverListeners.delete(listener); };
  }, [key]);

  const setGlobalOpen = useCallback((next: boolean) => {
    if (next) {
      _openPopoverKey = key;
    } else if (_openPopoverKey === key) {
      _openPopoverKey = null;
    }
    _notifyPopoverListeners();
  }, [key]);

  return [open, setGlobalOpen];
}

// ---------------------------------------------------------------------------
// SlotVersionPopover — 版本历史浮层 (Portal, 居中全屏遮罩)
// ---------------------------------------------------------------------------

/** Renders a single file revision (icon + name + preview/download) inside the version popover. */
function FileRevisionPreview({
  info,
  label,
}: {
  info: { url: string; name: string; size?: number };
  label: string;
}) {
  const [previewOpen, setPreviewOpen] = useState(false);
  const allowDownload = useContext(SlotDownloadContext);
  return (
    <>
      <div className='workflow-slot__version-file-card'>
        <div className='workflow-slot__version-file-card-header'>
          <span className='workflow-slot__file-icon' aria-hidden='true'>
            {getFileIcon(info.name || '')}
          </span>
          <div className='workflow-slot__version-file-card-info'>
            <span className='workflow-slot__version-file-card-name' title={info.name}>
              {info.name || '—'}
            </span>
            <span className='workflow-slot__version-file-card-meta'>
              {label}
              {typeof info.size === 'number' && info.size > 0 ? ` · ${formatFileSize(info.size)}` : ''}
            </span>
          </div>
        </div>
        {info.url && (
          <div className='workflow-slot__version-file-card-actions'>
            <button
              className='workflow-slot__file-action-btn'
              onClick={() => setPreviewOpen(true)}
              type='button'
            >
              {tr('chat.slots.preview')}
            </button>
            {allowDownload && (
              <a
                className='workflow-slot__file-action-btn'
                href={info.url}
                download={info.name || undefined}
                onClick={(e) => e.stopPropagation()}
              >
                {tr('chat.slots.download')}
              </a>
            )}
          </div>
        )}
      </div>
      <FilePreviewDrawer
        open={previewOpen}
        filename={info.name || ''}
        url={info.url}
        onClose={() => setPreviewOpen(false)}
      />
    </>
  );
}

interface SlotVersionPopoverProps {
  sessionId: string;
  slotId: string;
  /** List index used for backend API calls. Use -1 for single (non-list) slots. */
  listIndex: number;
  /**
   * List index used for draftStore operations (localStorage key).
   * Defaults to listIndex when not provided.
   * Single slots should pass 0 here (the front-end canonical key).
   */
  draftListIndex?: number;
  revisionCount: number;
  /** The revision number of the currently selected version — shown on the badge. */
  currentRevision?: number;
  currentValue?: any;
  currentChangeSource?: 'ai' | 'human' | 'provider_sync';
  contentType?: string;
  onRollbackDone?: () => void;
  draftText?: string;
  /** Called when the user clicks "Discard draft" in draft mode. */
  onDiscardDraft?: () => void;
}

// Sentinel value representing the draft entry in the version list.
const DRAFT_REVISION = -1;

export function SlotVersionPopover({
  sessionId,
  slotId,
  listIndex,
  draftListIndex,
  revisionCount,
  currentRevision,
  currentValue,
  contentType,
  onRollbackDone,
  draftText,
  onDiscardDraft,
}: SlotVersionPopoverProps) {
  // effectiveDraftIndex: index used for draftStore operations (localStorage key).
  const effectiveDraftIndex = draftListIndex ?? listIndex;
  const popoverKey: PopoverKey = `${sessionId}:${slotId}:${listIndex}`;
  const [open, setOpen] = useGlobalPopoverOpen(popoverKey);
  const [versions, setVersions] = useState<SlotVersionEntry[]>([]);
  const [loading, setLoading] = useState(false);
  // previewIndex: index into versions[] of the currently previewed version
  const [previewIndex, setPreviewIndex] = useState<number>(0);
  const [rolling, setRolling] = useState(false);
  // selectedRevision: the version the user clicked in the left list (text mode)
  // DRAFT_REVISION means the draft entry is selected.
  const [selectedRevision, setSelectedRevision] = useState<number | null>(null);
  const [uploading, setUploading] = useState(false);
  const [flushing, setFlushing] = useState(false);
  const versionUploadRef = useRef<HTMLInputElement>(null);
  const { getSlotVersions, rollbackSlotItem, patchSlotItemValue } = useWorkflowStore();
  const isWriterDraft = slotId === 'draft_document';
  const versionLabel = (revision: number) => isWriterDraft
    ? tr('chat.writerIR.localVersion', { version: revision })
    : `v${revision}`;
  const historyTitle = isWriterDraft
    ? tr('chat.writerIR.localHistory')
    : tr('chat.slots.versionHistory');
  const changeSourceLabel = (version: SlotVersionEntry) => {
    if (version.provider_synced) return tr('chat.slots.feishuSynced');
    return version.change_source === 'human' ? tr('chat.slots.manual') : tr('chat.slots.ai');
  };

  const handleOpen = useCallback(async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (open) {
      setOpen(false);
      return;
    }
    // Always load version history; in draft mode also default-select the draft entry.
    setLoading(true);
    try {
      const vs = await getSlotVersions(sessionId, slotId, listIndex);
      const sorted = [...vs].sort((a, b) => b.revision - a.revision);
      setVersions(sorted);
      const currentIdx = sorted.findIndex((v) => v.selected);
      setPreviewIndex(currentIdx >= 0 ? currentIdx : 0);
      // Default selection: draft entry when draft exists, otherwise current version.
      setSelectedRevision(draftText !== undefined ? DRAFT_REVISION : null);
      setOpen(true);
    } finally {
      setLoading(false);
    }
  }, [open, sessionId, slotId, listIndex, getSlotVersions, draftText, setOpen]);

  const handleClose = useCallback(() => setOpen(false), [setOpen]);

  const handleOverlayClick = useCallback((e: React.MouseEvent) => {
    if (e.target === e.currentTarget) handleClose();
  }, [handleClose]);

  const handleRollback = useCallback(async (revision: number) => {
    setRolling(true);
    try {
      await rollbackSlotItem(sessionId, slotId, listIndex, revision);
      setOpen(false);
      onRollbackDone?.();
    } finally {
      setRolling(false);
    }
  }, [sessionId, slotId, listIndex, rollbackSlotItem, setOpen, onRollbackDone]);

  const handleFlushDraft = useCallback(async () => {
    if (!draftText) return;
    setFlushing(true);
    try {
      await draftStore.flushDraft(sessionId, slotId, effectiveDraftIndex, listIndex);
      onDiscardDraft?.();
      setOpen(false);
      onRollbackDone?.();
    } finally {
      setFlushing(false);
    }
  }, [draftText, sessionId, slotId, effectiveDraftIndex, listIndex, onDiscardDraft, setOpen, onRollbackDone]);

  const handleVersionUploadClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    versionUploadRef.current?.click();
  }, []);

  const handleVersionFileChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setUploading(true);
    try {
      const storedPath = await uploadFileInChunks(file);
      await patchSlotItemValue(sessionId, slotId, listIndex, { path: storedPath }, isImage ? 'image' : undefined);
      setOpen(false);
      onRollbackDone?.();
    } catch {
      // upload failure — no-op
    } finally {
      setUploading(false);
    }
  }, [sessionId, slotId, listIndex, patchSlotItemValue, setOpen, onRollbackDone]);

  const isImage = contentType === 'image';
  const isFile = contentType === 'file';

  // Extract plain text/URL from a content_snapshot or artifact_value.
  // For image slots, url/path values are passed through resolveCoreAssetUrl so that
  // relative /static-files/... paths are correctly expanded to absolute browser URLs.
  const extractText = (snapshot: any): string => {
    if (!snapshot) return '';
    if (typeof snapshot === 'string') return snapshot;
    if (snapshot?.url) return isImage ? resolveCoreAssetUrl(snapshot.url) : snapshot.url;
    if (snapshot?.path) return isImage ? resolveCoreAssetUrl(snapshot.path) : snapshot.path;
    if (snapshot?.text !== undefined) return String(snapshot.text);
    if (snapshot?.data !== undefined) {
      return typeof snapshot.data === 'string' ? snapshot.data : JSON.stringify(snapshot.data, null, 2);
    }
    return JSON.stringify(snapshot, null, 2);
  };

  // Extract displayable file info {url, name, size} from a content_snapshot.
  const extractFileInfo = (snapshot: any): { url: string; name: string; size?: number } => {
    const empty = { url: '', name: '' };
    if (!snapshot) return empty;
    if (typeof snapshot === 'string') return { url: resolveCoreAssetUrl(snapshot), name: snapshot.split('/').pop() ?? snapshot };
    const rawPath: string = snapshot.url ?? snapshot.path ?? '';
    return {
      url: rawPath ? resolveCoreAssetUrl(rawPath) : '',
      name: snapshot.filename ?? snapshot.name ?? (rawPath ? rawPath.split('/').pop() : ''),
      size: typeof snapshot.size === 'number' ? snapshot.size : undefined,
    };
  };

  const previewedVersion = versions[previewIndex] ?? null;
  // The currently-selected (active) version
  const currentVersion = versions.find((v) => v.selected) ?? versions[0] ?? null;
  const activeCurrentValue = currentVersion?.content_snapshot ?? currentValue;
  // Whether the previewed version is already the current one
  const isPreviewingCurrent = previewedVersion?.selected ?? false;

  // Format date as MM/DD HH:mm
  const formatDate = (isoStr: string) => {
    const d = new Date(isoStr);
    const mm = String(d.getMonth() + 1).padStart(2, '0');
    const dd = String(d.getDate()).padStart(2, '0');
    const hh = String(d.getHours()).padStart(2, '0');
    const min = String(d.getMinutes()).padStart(2, '0');
    return `${mm}/${dd} ${hh}:${min}`;
  };

  // effectiveSelectedRevision: the revision number clicked in left list, or DRAFT_REVISION for the draft entry.
  // null means default to current version.
  const effectiveSelectedVersion =
    selectedRevision === DRAFT_REVISION
      ? null
      : (versions.find((v) => v.revision === (selectedRevision ?? currentVersion?.revision)) ?? currentVersion);
  // When draft is selected (DRAFT_REVISION), the right pane shows draft vs current diff.
  const isDraftSelected = selectedRevision === DRAFT_REVISION;

  const popoverContent = open ? ReactDOM.createPortal(
    <div
      className='workflow-slot__version-overlay'
      onClick={handleOverlayClick}
      role='presentation'
    >
      <div
        className={`workflow-slot__version-popover${isImage ? ' workflow-slot__version-popover--image' : ''}${isFile ? ' workflow-slot__version-popover--file' : ''}`}
        role='dialog'
        aria-label={historyTitle}
        aria-modal='true'
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className='workflow-slot__version-popover-header'>
          <span className='workflow-slot__version-popover-title'>
            {historyTitle}
          </span>
          <button
            className='workflow-slot__version-popover-close'
            onClick={handleClose}
            aria-label={tr('chat.slots.closeVersionHistory')}
          >×</button>
        </div>

        {isImage ? (
          /* ── Image mode: top-down layout ── */
          <>
            {currentVersion && (
              <div className='workflow-slot__version-meta-row'>
                <span className='workflow-slot__version-meta-label'>{tr('chat.slots.currentVersionLabel')}</span>
                <span className='workflow-slot__version-meta-badge'>V{currentVersion.revision}</span>
                <span className='workflow-slot__version-meta-time'>
                  {tr('chat.slots.createdAt', { time: formatDate(currentVersion.created_at) })}
                </span>
              </div>
            )}

            <div className='workflow-slot__version-preview-area'>
              {versions.length > 1 && (
                <button
                  className='workflow-slot__version-nav workflow-slot__version-nav--prev'
                  onClick={() => setPreviewIndex((i) => Math.max(0, i - 1))}
                  disabled={previewIndex === 0}
                  aria-label={tr('chat.slots.previousVersion')}
                >‹</button>
              )}
              <div className='workflow-slot__version-preview-img-wrap'>
                {previewedVersion && extractText(previewedVersion.content_snapshot) ? (
                  <img
                    key={previewedVersion.revision}
                    className='workflow-slot__version-preview-img'
                    src={extractText(previewedVersion.content_snapshot)}
                    alt=''
                  />
                ) : previewedVersion ? (
                  <span className='workflow-slot__version-preview-empty'>{tr('chat.slots.noImage')}</span>
                ) : null}
              </div>
              {versions.length > 1 && (
                <button
                  className='workflow-slot__version-nav workflow-slot__version-nav--next'
                  onClick={() => setPreviewIndex((i) => Math.min(versions.length - 1, i + 1))}
                  disabled={previewIndex === versions.length - 1}
                  aria-label={tr('chat.slots.nextVersion')}
                >›</button>
              )}
            </div>

            <div className='workflow-slot__version-strip'>
              {versions.map((v, idx) => (
                <button
                  key={v.revision}
                  className={[
                    'workflow-slot__version-thumb',
                    idx === previewIndex ? 'workflow-slot__version-thumb--active' : '',
                    v.selected ? 'workflow-slot__version-thumb--current' : '',
                  ].join(' ')}
                  onClick={() => setPreviewIndex(idx)}
                  aria-label={tr('chat.slots.versionAria', { version: `V${v.revision}` })}
                >
                  <div className='workflow-slot__version-thumb-img-wrap'>
                    {extractText(v.content_snapshot) ? (
                      <img
                        className='workflow-slot__version-thumb-img'
                        src={extractText(v.content_snapshot)}
                        alt=''
                      />
                    ) : (
                      <span className='workflow-slot__version-thumb-empty'>—</span>
                    )}
                    <span className='workflow-slot__version-thumb-badge'>V{v.revision}</span>
                  </div>
                  {v.selected && (
                    <span className='workflow-slot__version-thumb-current-tag'>{tr('chat.slots.currentVersion')}</span>
                  )}
                </button>
              ))}
              {/* Upload new version card */}
              <button
                className='workflow-slot__version-thumb workflow-slot__version-thumb--upload'
                onClick={handleVersionUploadClick}
                disabled={uploading}
                aria-label={tr('chat.slots.uploadAndSelect')}
                type='button'
              >
                <span className='workflow-slot__version-thumb-upload-icon'>+</span>
                <span className='workflow-slot__version-thumb-upload-label'>
                  {uploading ? tr('chat.slots.uploading') : tr('chat.slots.uploadAndSelect')}
                </span>
              </button>
              <input
                ref={versionUploadRef}
                type='file'
                accept='image/*'
                style={{ display: 'none' }}
                onChange={handleVersionFileChange}
                aria-hidden='true'
              />
            </div>

            <div className='workflow-slot__version-footer'>
              <div className='workflow-slot__version-footer-actions'>
                <button className='workflow-slot__version-footer-cancel' onClick={handleClose}>{tr('common.cancel')}</button>
                <button
                  className='workflow-slot__version-footer-apply'
                  disabled={rolling || isPreviewingCurrent || !previewedVersion}
                  onClick={() => previewedVersion && handleRollback(previewedVersion.revision)}
                >
                  {rolling ? tr('chat.slots.rollingBack') : tr('chat.slots.setCurrentVersion')}
                </button>
              </div>
              {previewedVersion && !isPreviewingCurrent && (
                <p className='workflow-slot__version-footer-hint'>
                  {tr('chat.slots.setCurrentVersionHint')}
                </p>
              )}
            </div>
          </>
        ) : isFile ? (
          /* ── File mode: left version list + right file preview ── */
          <div className='workflow-slot__version-popover-body'>
            <ul className='workflow-slot__version-list' role='listbox' aria-label={tr('chat.slots.versionList')}>
              {versions.map((v) => {
                const info = extractFileInfo(v.content_snapshot);
                return (
                  <li
                    key={v.revision}
                    role='option'
                    aria-selected={!isDraftSelected && effectiveSelectedVersion?.revision === v.revision}
                    className={[
                      'workflow-slot__version-item',
                      v.selected ? 'workflow-slot__version-item--current' : '',
                      !isDraftSelected && effectiveSelectedVersion?.revision === v.revision ? 'workflow-slot__version-item--focused' : '',
                    ].join(' ')}
                    onClick={() => setSelectedRevision(v.revision)}
                  >
                    <span className='workflow-slot__version-label'>
                      <span className={`workflow-slot__version-source-badge workflow-slot__version-source-badge--${v.change_source}`}>
                        {changeSourceLabel(v)}
                      </span>
                      {versionLabel(v.revision)}
                      {v.selected && <span className='workflow-slot__version-current-tag'>{tr('chat.slots.current')}</span>}
                      {isWriterDraft && v.provider_synced && (
                        <span className='workflow-slot__version-current-tag'>{tr('chat.writerIR.syncedClean')}</span>
                      )}
                    </span>
                    <span className='workflow-slot__version-file-name' title={info.name}>
                      {info.name || '—'}
                    </span>
                    <span className='workflow-slot__version-time'>
                      {new Date(v.created_at).toLocaleString(i18n.language)}
                    </span>
                  </li>
                );
              })}
            </ul>

            {effectiveSelectedVersion && !effectiveSelectedVersion.selected ? (
              <div className='workflow-slot__version-compare workflow-slot__version-compare--file'>
                <FileRevisionPreview
                  info={extractFileInfo(effectiveSelectedVersion.content_snapshot)}
                  label={tr('chat.slots.versionSourceLabel', {
                    version: versionLabel(effectiveSelectedVersion.revision),
                    source: changeSourceLabel(effectiveSelectedVersion),
                  })}
                />
                <button
                  className='workflow-slot__version-apply-btn'
                  disabled={rolling}
                  onClick={() => handleRollback(effectiveSelectedVersion.revision)}
                  aria-label={tr('chat.slots.applyVersionAria', { version: versionLabel(effectiveSelectedVersion.revision) })}
                >
                  {rolling ? tr('chat.slots.rollingBack') : tr('chat.slots.applyVersion', { version: versionLabel(effectiveSelectedVersion.revision) })}
                </button>
              </div>
            ) : (
              <div className='workflow-slot__version-compare workflow-slot__version-compare--file'>
                {effectiveSelectedVersion ? (
                  <FileRevisionPreview
                    info={extractFileInfo(activeCurrentValue)}
                    label={tr('chat.slots.currentVersion')}
                  />
                ) : (
                  <div className='workflow-slot__version-compare-hint'>{tr('chat.slots.selectVersionPreview')}</div>
                )}
              </div>
            )}
          </div>
        ) : (
          /* ── Text mode: left list + right diff (unified, with optional draft entry) ── */
          <div className='workflow-slot__version-popover-body'>
            <ul className='workflow-slot__version-list' role='listbox' aria-label={tr('chat.slots.versionList')}>
              {/* Draft entry — only shown when there is a pending local draft */}
              {draftText !== undefined && (
                <li
                  role='option'
                  aria-selected={isDraftSelected}
                  className={[
                    'workflow-slot__version-item',
                    'workflow-slot__version-item--draft',
                    isDraftSelected ? 'workflow-slot__version-item--focused' : '',
                  ].join(' ')}
                  onClick={() => setSelectedRevision(DRAFT_REVISION)}
                >
                  <span className='workflow-slot__version-label'>
                    <span className='workflow-slot__version-source-badge workflow-slot__version-source-badge--human'>
                      {tr('chat.slots.draft')}
                    </span>
                    {tr('chat.slots.draft')}
                  </span>
                  <span className='workflow-slot__version-time'>{tr('chat.slots.notSubmitted')}</span>
                </li>
              )}
              {versions.map((v) => (
                <li
                  key={v.revision}
                  role='option'
                  aria-selected={!isDraftSelected && effectiveSelectedVersion?.revision === v.revision}
                  className={[
                    'workflow-slot__version-item',
                    v.selected ? 'workflow-slot__version-item--current' : '',
                    !isDraftSelected && effectiveSelectedVersion?.revision === v.revision ? 'workflow-slot__version-item--focused' : '',
                  ].join(' ')}
                  onClick={() => setSelectedRevision(v.revision)}
                >
                  <span className='workflow-slot__version-label'>
                    <span className={`workflow-slot__version-source-badge workflow-slot__version-source-badge--${v.change_source}`}>
                      {changeSourceLabel(v)}
                    </span>
                    {versionLabel(v.revision)}
                    {v.selected && <span className='workflow-slot__version-current-tag'>{tr('chat.slots.current')}</span>}
                    {isWriterDraft && v.provider_synced && (
                      <span className='workflow-slot__version-current-tag'>{tr('chat.writerIR.syncedClean')}</span>
                    )}
                  </span>
                  <span className='workflow-slot__version-time'>
                    {new Date(v.created_at).toLocaleString(i18n.language)}
                  </span>
                </li>
              ))}
            </ul>

            {isDraftSelected && draftText !== undefined ? (
              /* Draft selected: show draft vs current diff with discard + flush actions */
              <div className='workflow-slot__version-compare'>
                <SnapshotTextDiffView
                  currentSnapshot={activeCurrentValue}
                  otherText={draftText}
                  otherLabel={tr('chat.slots.draft')}
                  reversed={true}
                />
                <div className='workflow-slot__version-draft-actions'>
                  <button
                    className='workflow-slot__version-discard-btn'
                    onClick={() => { onDiscardDraft?.(); handleClose(); }}
                    aria-label={tr('chat.slots.discardDraft')}
                  >
                    {tr('chat.slots.discardDraft')}
                  </button>
                  <button
                    className='workflow-slot__version-flush-btn'
                    disabled={flushing}
                    onClick={handleFlushDraft}
                    aria-label={tr('chat.slots.confirmChanges')}
                  >
                    {flushing ? tr('chat.slots.submitting') : tr('chat.slots.confirmChanges')}
                  </button>
                </div>
              </div>
            ) : effectiveSelectedVersion && !effectiveSelectedVersion.selected ? (
              <div className='workflow-slot__version-compare'>
                <SnapshotTextDiffView
                  currentSnapshot={activeCurrentValue}
                  otherSnapshot={effectiveSelectedVersion.content_snapshot}
                  otherLabel={tr('chat.slots.versionSourceLabel', {
                    version: versionLabel(effectiveSelectedVersion.revision),
                    source: changeSourceLabel(effectiveSelectedVersion),
                  })}
                  reversed={currentVersion !== null && effectiveSelectedVersion.revision > currentVersion.revision}
                />
                <button
                  className='workflow-slot__version-apply-btn'
                  disabled={rolling}
                  onClick={() => handleRollback(effectiveSelectedVersion.revision)}
                  aria-label={tr('chat.slots.applyVersionAria', { version: versionLabel(effectiveSelectedVersion.revision) })}
                >
                  {rolling ? tr('chat.slots.rollingBack') : tr('chat.slots.applyVersion', { version: versionLabel(effectiveSelectedVersion.revision) })}
                </button>
              </div>
            ) : (
              <div className='workflow-slot__version-compare workflow-slot__version-compare--same'>
                {effectiveSelectedVersion ? (
                  <SnapshotTextPreview snapshot={activeCurrentValue} />
                ) : (
                  <div className='workflow-slot__version-compare-hint'>{tr('chat.slots.selectVersionCompare')}</div>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </div>,
    document.body,
  ) : null;

  return (
    <div className='workflow-slot__version-wrap'>
      <button
        className={`workflow-slot__version-btn${draftText !== undefined ? ' workflow-slot__version-btn--draft' : ''}`}
        onClick={handleOpen}
        title={draftText !== undefined ? tr('chat.slots.draftCompareHint') : isWriterDraft ? historyTitle : tr('chat.slots.versionHistoryCount', { count: revisionCount })}
        aria-label={draftText !== undefined ? tr('chat.slots.draft') : isWriterDraft ? historyTitle : tr('chat.slots.versionHistoryCount', { count: revisionCount })}
        disabled={loading}
      >
        <span className='workflow-slot__version-count'>
          {draftText !== undefined ? 'draft' : versionLabel(currentRevision ?? (revisionCount > 1 ? revisionCount : 1))}
        </span>
      </button>
      {popoverContent}
    </div>
  );
}

// --------------------------------------------------------------------------
// SlotImage with delete, version badge, reference button, drag handle
// --------------------------------------------------------------------------

interface SlotImageProps {
  slot: SlotRevision;
  cardMode?: boolean;
  sessionId?: string;
  slotId?: string;
  /** Number of revisions for this item — shown as version badge. */
  revisionCount?: number;
  isDraggable?: boolean;
  /** Called after delete or rollback so the parent can refresh. */
  onRefresh?: () => void;
  /** Called when the user clicks the reference (cite) button. */
  onReference?: (slot: SlotRevision) => void;
  readOnly?: boolean;
  hideMutationActions?: boolean;
}

export function SlotImage({
  slot,
  cardMode = false,
  sessionId,
  slotId,
  revisionCount,
  isDraggable,
  onRefresh,
  onReference,
  readOnly,
  hideMutationActions,
}: SlotImageProps) {
  const raw = slot.artifact_value;
  const { displayUrl: url, pending, hasSource } = useSlotImageUrl(raw);
  const alt: string = slot.caption ?? raw?.alt ?? '';
  const { deleteSlotItem, patchSlotCaption, patchSlotItemValue } = useWorkflowStore();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [captionEditing, setCaptionEditing] = useState(false);
  const [captionDraft, setCaptionDraft] = useState('');
  const [uploading, setUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Reset editing state when a different slot item is mapped to this component instance
  // (e.g. after delete+reorder, the same React node may receive a new slot via props).
  const prevListIndexRef = useRef(slot.list_index);
  useEffect(() => {
    if (prevListIndexRef.current !== slot.list_index) {
      prevListIndexRef.current = slot.list_index;
      setCaptionEditing(false);
      setCaptionDraft('');
      setConfirmDelete(false);
    }
  }, [slot.list_index]);

  const handleUploadClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    fileInputRef.current?.click();
  }, []);

  const handleFileChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file || !sessionId || !slotId || slot.list_index === undefined) return;
    // Reset input so the same file can be re-selected later
    e.target.value = '';
    setUploading(true);
    try {
      const storedPath = await uploadFileInChunks(file);
      await patchSlotItemValue(sessionId, slotId, slot.list_index, { path: storedPath }, 'image');
      onRefresh?.();
    } catch {
      // upload failure — no-op, user can retry
    } finally {
      setUploading(false);
    }
  }, [sessionId, slotId, slot.list_index, patchSlotItemValue, onRefresh]);

  const handleDeleteClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setConfirmDelete(true);
  }, []);

  const handleDeleteConfirm = useCallback(async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!sessionId || !slotId || slot.list_index === undefined) return;
    await deleteSlotItem(sessionId, slotId, slot.list_index);
    setConfirmDelete(false);
    onRefresh?.();
  }, [sessionId, slotId, slot.list_index, deleteSlotItem, onRefresh]);

  const handleDeleteCancel = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setConfirmDelete(false);
  }, []);

  const handleReference = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    onReference?.(slot);
  }, [slot, onReference]);

  const handleCaptionEdit = useCallback(() => {
    setCaptionDraft(slot.caption ?? '');
    setCaptionEditing(true);
  }, [slot.caption]);

  const handleCaptionSave = useCallback(async () => {
    if (!sessionId || !slotId || slot.list_index === undefined) return;
    setCaptionEditing(false);
    await patchSlotCaption(sessionId, slotId, slot.list_index, captionDraft);
    onRefresh?.();
  }, [sessionId, slotId, slot.list_index, captionDraft, patchSlotCaption, onRefresh]);

  const handleCaptionKeyDown = useCallback((e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') handleCaptionSave();
    if (e.key === 'Escape') setCaptionEditing(false);
  }, [handleCaptionSave]);

  if (!hasSource || pending || !url) {
    return <SlotPending type='image' cardMode={cardMode} />;
  }

  const hasActions = Boolean(sessionId && slotId && slot.list_index !== undefined) && !readOnly;
  const showMutationActions = hasActions && !hideMutationActions;

  // Overlays rendered directly on top of the image (no separate action bar)
  const overlays = hasActions ? (
    <>
      {/* Delete + Upload buttons — top-right, shown on hover via CSS */}
      {showMutationActions && (confirmDelete ? (
        <span className='workflow-slot__delete-confirm workflow-slot__delete-confirm--overlay'>
          <span className='workflow-slot__delete-confirm-text'>{tr('chat.slots.confirmDeleteQuestion')}</span>
          <button
            className='workflow-slot__delete-confirm-yes'
            onClick={handleDeleteConfirm}
            aria-label={tr('chat.slots.confirmDelete')}
          >{tr('common.delete')}</button>
          <button
            className='workflow-slot__delete-confirm-no'
            onClick={handleDeleteCancel}
            aria-label={tr('chat.slots.cancelDelete')}
          >{tr('common.cancel')}</button>
        </span>
      ) : (
        <span className='workflow-slot__top-right-actions'>
          <button
            className='workflow-slot__upload-overlay-btn'
            onClick={handleUploadClick}
            disabled={uploading}
            title={tr('chat.slots.uploadAndSelect')}
            aria-label={tr('chat.slots.uploadAndSelect')}
          >
            {uploading ? '…' : '+'}
          </button>
          <button
            className='workflow-slot__delete-btn workflow-slot__delete-btn--overlay'
            onClick={handleDeleteClick}
            title={tr('common.delete')}
            aria-label={tr('chat.slots.deleteImage')}
          >×</button>
        </span>
      ))}

      {/* Version badge — bottom-left, always visible, overlaid on image */}
      {revisionCount !== undefined && revisionCount > 0 && (
        <div className='workflow-slot__version-overlay-badge'>
          <SlotVersionPopover
            sessionId={sessionId!}
            slotId={slotId!}
            listIndex={slot.list_index!}
            revisionCount={revisionCount}
            currentRevision={slot.revision}
            currentValue={slot.artifact_value}
            currentChangeSource={slot.change_source}
            contentType='image'
            onRollbackDone={onRefresh}
          />
        </div>
      )}

      {/* Reference button — bottom-right, shown on hover */}
      {onReference && (
        <button
          className='workflow-slot__ref-btn workflow-slot__ref-btn--overlay'
          onClick={handleReference}
          title={tr('chat.slots.referenceImage')}
          aria-label={tr('chat.slots.referenceImage')}
        >📎</button>
      )}

      {/* Drag handle — bottom-left edge, shown on hover */}
      {isDraggable && (
        <span className='workflow-slot__drag-handle workflow-slot__drag-handle--overlay' title={tr('chat.slots.dragToSort')} aria-hidden='true'>⠿</span>
      )}
    </>
  ) : null;

  if (cardMode) {
    return (
      <div className='workflow-slot workflow-slot--image-card-wrap'>
        <div className='workflow-slot workflow-slot--image-card'>
          <img src={url} alt={alt} className='workflow-slot__image-card-img' loading='lazy' />
          {alt && <div className='workflow-slot__image-card-caption'>{alt}</div>}
          {overlays}
        </div>
        {/* Hidden file input */}
        {showMutationActions && (
          <input
            ref={fileInputRef}
            type='file'
            accept='image/*'
            style={{ display: 'none' }}
            onChange={handleFileChange}
            aria-hidden='true'
          />
        )}
        {hasActions && (
          <div className='workflow-slot__caption'>
            {captionEditing ? (
              <input
                className='workflow-slot__caption-input'
                value={captionDraft}
                onChange={(e) => setCaptionDraft(e.target.value)}
                onBlur={handleCaptionSave}
                onKeyDown={handleCaptionKeyDown}
                autoFocus
                aria-label={tr('chat.slots.editDescription')}
                placeholder={tr('chat.slots.addDescription')}
              />
            ) : (
              <span
                className='workflow-slot__caption-text'
                onClick={handleCaptionEdit}
                title={tr('chat.slots.clickToEditDescription')}
                role='button'
                tabIndex={0}
                onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
              >
                {slot.caption || <span className='workflow-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
              </span>
            )}
          </div>
        )}
      </div>
    );
  }
  return (
    <div className='workflow-slot workflow-slot--image'>
      <img src={url} alt={alt} className='workflow-slot__image' loading='lazy' />
      {overlays}
      {hasActions && (
        <div className='workflow-slot__caption'>
          {captionEditing ? (
            <input
              className='workflow-slot__caption-input'
              value={captionDraft}
              onChange={(e) => setCaptionDraft(e.target.value)}
              onBlur={handleCaptionSave}
              onKeyDown={handleCaptionKeyDown}
              autoFocus
              aria-label={tr('chat.slots.editDescription')}
              placeholder={tr('chat.slots.addDescription')}
            />
          ) : (
            <span
              className='workflow-slot__caption-text'
              onClick={handleCaptionEdit}
              title={tr('chat.slots.clickToEditDescription')}
              role='button'
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
            >
              {slot.caption || <span className='workflow-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

// --------------------------------------------------------------------------
// SlotText with inline editing, draft store, and version badge
// --------------------------------------------------------------------------

interface SlotTextProps {
  slot: SlotRevision;
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  onRefresh?: () => void;
  readOnly?: boolean;
}

export function SlotText({ slot, sessionId, slotId, revisionCount, onRefresh, readOnly }: SlotTextProps) {
  const raw = slot.artifact_value;
  const { patchSlotCaption } = useWorkflowStore();
  const { setEditing: notifyEditing } = useContext(SlotEditingContext);
  const editingKey = `${sessionId}:${slotId}:${slot.list_index}`;
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [offloadedText, setOffloadedText] = useState<string | null>(null);
  const [offloadLoading, setOffloadLoading] = useState(false);
  // hasPendingDraft: reactive flag to show/hide the "draft" badge.
  const [hasPendingDraft, setHasPendingDraft] = useState(() => {
    if (!sessionId || !slotId) return false;
    const saved = draftStore.getLocalDraft(sessionId, slotId, slot.list_index ?? 0);
    return saved?.text !== undefined;
  });
  // Caption inline editing state.
  const [captionEditing, setCaptionEditing] = useState(false);
  const [captionDraft, setCaptionDraft] = useState('');
  // Flag to skip onBlur save when user presses Escape.
  const cancelledRef = useRef(false);

  useEffect(
    () => () => notifyEditing(editingKey, false),
    [editingKey, notifyEditing],
  );

  // Detect large-content offload: {"type":"text"|"json","path":"...","size":N}
  const isOffloaded = raw && typeof raw === 'object' && raw.path && (raw.type === 'text' || raw.type === 'json');

  // Fetch offloaded file content on mount (or when path changes).
  useEffect(() => {
    if (!isOffloaded) return;
    let cancelled = false;
    setOffloadLoading(true);

    const pathForSign = String(raw?.path ?? raw?.url ?? '').trim();
    const apiUrlRaw = raw?.url ? String(raw.url).trim() : '';

    async function loadOffloadedText(): Promise<string> {
      const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
      const fetchUrl = apiUrl && !isExpiredSignedUrl(apiUrl)
        ? apiUrl
        : await resolveMarkdownImageUrlAsync(pathForSign);
      const response = await fetch(fetchUrl);
      if (!response.ok) {
        throw new Error(localizeErrorCode('2000509'));
      }
      const text = await response.text();
      if (isSpaFallbackHtml(text)) {
        throw new Error('invalid artifact content');
      }
      return text;
    }

    loadOffloadedText()
      .then((t) => {
        if (!cancelled) setOffloadedText(t);
      })
      .catch(() => {
        if (!cancelled) setOffloadedText(localizeErrorCode('2000509'));
      })
      .finally(() => {
        if (!cancelled) setOffloadLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [isOffloaded, raw?.path, raw?.url]);

  const canEdit = Boolean(sessionId && slotId) && !readOnly;
  // For single slots, list_index is undefined from the backend; use 0 as the canonical index
  // for localStorage keys (front-end only convention).
  const effectiveListIndex = slot.list_index ?? 0;
  // For API calls, single slots must use -1 so the backend queries list_index IS NULL.
  const apiListIndex = slot.list_index ?? -1;

  let text = '';
  if (isOffloaded) {
    text = offloadedText ?? '';
  } else if (raw?.text !== undefined) {
    text = String(raw.text);
  } else if (raw?.data !== undefined) {
    text = typeof raw.data === 'string' ? raw.data : JSON.stringify(raw.data, null, 2);
  } else if (raw !== undefined && raw !== null) {
    text = JSON.stringify(raw);
  }

  const showPending =
    (isOffloaded && offloadLoading) ||
    (!isOffloaded && (raw === undefined || raw === null));

  // On mount: restore localStorage draft only if it differs from the current artifact text.
  // Also restart the 60s flush timer so the draft doesn't stay in localStorage forever.
  useEffect(() => {
    if (!canEdit || !sessionId || !slotId || showPending) return;
    const saved = draftStore.getLocalDraft(sessionId, slotId, effectiveListIndex);
    if (saved?.text !== undefined && String(saved.text) !== text) {
      setDraft(String(saved.text));
      setHasPendingDraft(true);
      // Re-register with draftStore to restart the 60s flush timer lost on page reload.
      draftStore.setDraft(sessionId, slotId, effectiveListIndex, saved, apiListIndex);
    } else if (saved?.text !== undefined) {
      draftStore.cancelDraft(sessionId, slotId, effectiveListIndex);
      setHasPendingDraft(false);
    }
  // Run only on mount (stable deps).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleEdit = () => {
    const saved = (sessionId && slotId)
      ? draftStore.getLocalDraft(sessionId, slotId, effectiveListIndex)
      : null;
    const savedText = saved?.text !== undefined ? String(saved.text) : undefined;
    setDraft(savedText !== undefined && savedText !== text ? savedText : text);
    setEditing(true);
    notifyEditing(editingKey, true);
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value;
    setDraft(val);
    if (sessionId && slotId) {
      const draftPayload: Record<string, unknown> = { text: val };
      if (isOffloaded) {
        draftPayload._isOffloaded = true;
        draftPayload._originalFilename = (raw as any)?.path
          ? (raw as any).path.split('/').pop() ?? 'artifact.txt'
          : 'artifact.txt';
      }
      draftStore.setDraft(sessionId, slotId, effectiveListIndex, draftPayload, apiListIndex);
    }
  };

  const handleSave = () => {
    if (cancelledRef.current) {
      cancelledRef.current = false;
      return;
    }
    if (sessionId && slotId) {
      if (draft !== text) {
        const draftPayload: Record<string, unknown> = { text: draft };
        if (isOffloaded) {
          draftPayload._isOffloaded = true;
          draftPayload._originalFilename = (raw as any)?.path
            ? (raw as any).path.split('/').pop() ?? 'artifact.txt'
            : 'artifact.txt';
        }
        draftStore.setDraft(sessionId, slotId, effectiveListIndex, draftPayload, apiListIndex);
        setHasPendingDraft(true);
      } else {
        draftStore.cancelDraft(sessionId, slotId, effectiveListIndex);
        setHasPendingDraft(false);
      }
    }
    setEditing(false);
    notifyEditing(editingKey, false);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      handleSave();
    }
    if (e.key === 'Escape') {
      handleCancel();
    }
  };

  const handleCancel = () => {
    cancelledRef.current = true;
    if (sessionId && slotId) {
      draftStore.cancelDraft(sessionId, slotId, effectiveListIndex);
      setHasPendingDraft(false);
    }
    setEditing(false);
    notifyEditing(editingKey, false);
  };

  // Caption helpers.
  const handleCaptionEdit = () => {
    setCaptionDraft(slot.caption ?? '');
    setCaptionEditing(true);
  };

  const handleCaptionSave = async () => {
    if (!sessionId || !slotId) return;
    setCaptionEditing(false);
    await patchSlotCaption(sessionId, slotId, effectiveListIndex, captionDraft);
    onRefresh?.();
  };

  const handleCaptionKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') handleCaptionSave();
    if (e.key === 'Escape') setCaptionEditing(false);
  };

  // Determine display text: prefer draft if user is not editing (shows unsaved draft).
  const displayText = (() => {
    if (editing) return draft;
    if (sessionId && slotId) {
      const saved = draftStore.getLocalDraft(sessionId, slotId, effectiveListIndex);
      if (saved?.text !== undefined) return String(saved.text);
    }
    return text;
  })();

  // Compute the pending draft text for the version badge: non-null only when there
  // is a local draft that differs from the committed artifact text.
  const pendingDraftText = (() => {
    if (!hasPendingDraft || !canEdit || !sessionId || !slotId) return undefined;
    const saved = draftStore.getLocalDraft(sessionId, slotId, effectiveListIndex);
    if (saved?.text !== undefined && String(saved.text) !== text) return String(saved.text);
    return undefined;
  })();

  if (showPending) {
    return <SlotPending type='text' />;
  }

  return (
    <div className='workflow-slot workflow-slot--text'>
      {editing ? (
        <textarea
          className='workflow-slot__text-editor'
          value={draft}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          onBlur={handleSave}
          autoFocus
          rows={6}
          aria-label={tr('chat.slots.editText')}
        />
      ) : (
        <>
          <p
            className={`workflow-slot__text${canEdit ? ' workflow-slot__text--editable' : ''}`}
            onClick={canEdit ? handleEdit : undefined}
            title={canEdit ? tr('chat.slots.clickToEdit') : undefined}
            role={canEdit ? 'button' : undefined}
            tabIndex={canEdit ? 0 : undefined}
            onKeyDown={canEdit ? (e) => e.key === 'Enter' && handleEdit() : undefined}
          >{displayText}</p>
          <div className='workflow-slot__text-meta'>
            {revisionCount !== undefined && revisionCount > 0 && sessionId && slotId && (
              <SlotVersionPopover
                sessionId={sessionId}
                slotId={slotId}
                listIndex={apiListIndex}
                draftListIndex={effectiveListIndex}
                revisionCount={revisionCount}
                currentRevision={slot.revision}
                currentValue={slot.artifact_value}
                currentChangeSource={slot.change_source}
                contentType='text'
                onRollbackDone={onRefresh}
                draftText={pendingDraftText}
                onDiscardDraft={pendingDraftText !== undefined ? () => {
                  if (sessionId && slotId) {
                    draftStore.cancelDraft(sessionId, slotId, effectiveListIndex);
                    setHasPendingDraft(false);
                  }
                } : undefined}
              />
            )}
          </div>
          {/* Caption inline edit */}
          {canEdit && (
            <div className='workflow-slot__caption'>
              {captionEditing ? (
                <input
                  className='workflow-slot__caption-input'
                  value={captionDraft}
                  onChange={(e) => setCaptionDraft(e.target.value)}
                  onBlur={handleCaptionSave}
                  onKeyDown={handleCaptionKeyDown}
                  autoFocus
                  aria-label={tr('chat.slots.editDescription')}
                  placeholder={tr('chat.slots.addDescription')}
                />
              ) : (
                <span
                  className='workflow-slot__caption-text'
                  onClick={handleCaptionEdit}
                  title={tr('chat.slots.clickToEditDescription')}
                  role='button'
                  tabIndex={0}
                  onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
                >
                  {slot.caption || <span className='workflow-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
                </span>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}

// --------------------------------------------------------------------------
// getFileIcon — maps filename extension to an emoji icon
// --------------------------------------------------------------------------

function getFileIcon(filename: string): string {
  const ext = filename.split('.').pop()?.toLowerCase() ?? '';
  if (ext === 'pdf') return '📕';
  if (ext === 'doc' || ext === 'docx') return '📝';
  if (ext === 'xls' || ext === 'xlsx') return '📊';
  if (ext === 'ppt' || ext === 'pptx') return '📑';
  if (ext === 'txt' || ext === 'md') return '📄';
  if (ext === 'json' || ext === 'lmd' || ext === 'csv') return '📋';
  if (ext === 'zip' || ext === 'tar' || ext === 'gz' || ext === 'rar') return '🗜️';
  if (ext === 'jpg' || ext === 'jpeg' || ext === 'png' || ext === 'gif' || ext === 'webp') return '🖼️';
  return '📎';
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

interface SlotFileProps {
  slot: SlotRevision;
  sessionId?: string;
  slotId?: string;
  /** Number of revisions for this item — shown as version badge. */
  revisionCount?: number;
  onRefresh?: () => void;
  readOnly?: boolean;
}

function isJsonArtifactFile(slot: SlotRevision): boolean {
  const raw = slot.artifact_value;
  const name = String(raw?.filename ?? raw?.name ?? '').toLowerCase();
  const path = String(raw?.url ?? raw?.path ?? '').toLowerCase();
  return name.endsWith('.json') || path.endsWith('.json');
}

function isWriterIrArtifactFile(slot: SlotRevision): boolean {
  const raw = slot.artifact_value;
  if (raw?.document_format === 'writer_ir' || raw?.document_format === 'lmd') return true;
  return [
    raw?.filename,
    raw?.name,
    raw?.path,
    raw?.url,
  ].some(isWriterIrSource);
}

function isMarkdownArtifactFile(slot: SlotRevision): boolean {
  const raw = slot.artifact_value;
  if (raw?.document_format === 'markdown') return true;
  const name = String(raw?.filename ?? raw?.name ?? '').toLowerCase();
  const path = String(raw?.url ?? raw?.path ?? '').toLowerCase();
  return name.endsWith('.md')
    || name.endsWith('.markdown')
    || path.endsWith('.md')
    || path.endsWith('.markdown');
}

function isOffloadedArtifactReference(raw: Record<string, unknown>): boolean {
  const hasPath = Boolean(String(raw.path ?? raw.url ?? '').trim());
  return hasPath && (raw.type === 'text' || raw.type === 'json');
}

function getInlineStructuredArtifactPayload(slot: SlotRevision): unknown | null {
  const raw = slot.artifact_value;
  if (!raw || typeof raw !== 'object') return null;
  const record = raw as Record<string, unknown>;

  if (isOffloadedArtifactReference(record)) {
    return null;
  }

  if (isWriterDocument(record)) {
    return record;
  }

  if (record.data !== undefined) {
    const payload = unwrapArtifactPayload(raw);
    if (payload !== null && payload !== undefined && typeof payload === 'object') {
      return payload;
    }
    if (typeof payload === 'string') {
      try {
        return JSON.parse(payload);
      } catch {
        return null;
      }
    }
  }

  if (slot.content_type === 'json' && record.text === undefined) {
    return unwrapArtifactPayload(raw);
  }

  return null;
}

function replaceStructuredArtifactPayload(
  source: unknown,
  document: WriterDocument,
): unknown {
  if (isWriterDocument(source)) return document;
  if (
    source
    && typeof source === 'object'
    && !Array.isArray(source)
    && Object.prototype.hasOwnProperty.call(source, 'data')
  ) {
    return { ...(source as Record<string, unknown>), data: document };
  }
  return document;
}

/** True when the document has a cloud provider target (Feishu uri or document_id). */
function hasProviderTarget(document?: WriterDocument | null): boolean {
  const binding = document?.provider_binding;
  if (!binding) return false;

  return (
    (typeof binding.uri === 'string' && binding.uri.trim() !== '')
    || (
      typeof binding.document_id === 'string'
      && binding.document_id.trim() !== ''
    )
  );
}

function ensureWriterIrFilename(name: string): string {
  const trimmed = name.trim() || 'writer-document.lmd';
  if (trimmed.toLowerCase().endsWith('.lmd')) return trimmed;
  if (trimmed.toLowerCase().endsWith('_ir.json')) {
    return `${trimmed.slice(0, -5)}.lmd`;
  }
  return `${trimmed.replace(/\.json$/i, '')}.lmd`;
}

async function syncWriterDocumentSlot(
  sessionId: string,
  slotId: string,
  listIndex: number,
  sourceRevision: string | number | undefined,
  sourceDocument: WriterDocument,
  revisedDocument: WriterDocument,
  mode: WriterIRSaveMode = 'checkpoint',
): Promise<WriterIRSaveResult> {
  if (typeof sourceRevision !== 'number' || sourceRevision <= 0) {
    throw new Error(tr('chat.writerIR.saveFailed'));
  }
  const response = await WorkflowSessionApi().syncWriterDocument(
    sessionId,
    slotId,
    listIndex,
    {
      base_revision: sourceRevision,
      source_document: normalizeWriterDocumentForSync(sourceDocument),
      revised_document: normalizeWriterDocumentForSync(revisedDocument),
      mode,
    },
    { silentError: true } as never,
  );
  const result = response?.data?.data;
  if (
    response?.data?.code !== 0
    || !result
    || (result.status !== 'synced' && result.status !== 'no_change')
    || typeof result.revision !== 'number'
    || result.revision <= 0
    || result.feishu_synced !== true
    || (result.status === 'synced' && result.artifact_saved !== true)
    || (result.status === 'no_change' && result.artifact_saved !== false)
    || result.patch_result?.success !== true
    || !isWriterDocument(result.document)
  ) {
    throw new Error(tr('chat.writerIR.saveFailed'));
  }
  return {
    document: result.document,
    sourceRevision: result.revision,
  };
}

function writerBlockToMarkdown(block: WriterBlock, depth = 0): string {
  if (block.type === 'document') {
    return (block.children ?? []).map((child) => writerBlockToMarkdown(child, depth)).filter(Boolean).join('\n\n');
  }

  const content = block.content?.trim() ?? '';
  const children = (block.children ?? [])
    .map((child) => writerBlockToMarkdown(child, depth + 1))
    .filter(Boolean)
    .join('\n\n');
  let current = content;

  if (block.type === 'heading') {
    const level = Math.min(6, Math.max(1, Number(block.numbering?.level ?? 2)));
    current = content ? `${'#'.repeat(level)} ${content}` : '';
  } else if (block.type === 'list_item') {
    current = content ? `${'  '.repeat(depth)}${block.numbering?.ordered ? '1.' : '-'} ${content}` : '';
  } else if (block.type === 'quote') {
    current = content ? content.split('\n').map((line) => `> ${line}`).join('\n') : '';
  } else if (block.type === 'code') {
    current = content ? `\`\`\`\n${content}\n\`\`\`` : '';
  } else if (block.type === 'divider') {
    current = '---';
  }

  return [current, children].filter(Boolean).join('\n\n');
}

function writerDocumentToMarkdown(document: WriterDocument): string {
  const title = document.title.trim() ? `# ${document.title.trim()}` : '';
  const body = document.blocks.map((block) => writerBlockToMarkdown(block)).filter(Boolean).join('\n\n');
  return `${[title, body].filter(Boolean).join('\n\n')}\n`;
}

function writerMarkdownFilename(name: string): string {
  const trimmed = name.trim() || 'document';
  if (trimmed.toLowerCase().endsWith('_ir.lmd')) return `${trimmed.slice(0, -7)}.md`;
  if (trimmed.toLowerCase().endsWith('_ir.json')) return `${trimmed.slice(0, -8)}.md`;
  return `${trimmed.replace(/\.(?:lmd|json)$/i, '')}.md`;
}

function shouldRenderInlineStructuredContent(
  slot: SlotRevision,
  expectedType?: 'image' | 'file' | 'text',
  slotId?: string,
): boolean {
  const payload = getInlineStructuredArtifactPayload(slot);
  if (payload === null) return false;
  if (isWriterDocument(payload)) {
    return expectedType !== 'image';
  }
  if (expectedType !== 'text') return false;
  if (slot.content_type === 'json') return true;
  const resolvedSlotId = slotId ?? slot.slot;
  return WRITER_ARTIFACT_SLOT_IDS.has(resolvedSlotId);
}

function shouldRenderJsonFileAsContent(
  slot: SlotRevision,
  expectedType?: 'image' | 'file' | 'text',
): boolean {
  const raw = slot.artifact_value;
  if (!raw || typeof raw !== 'object') return false;
  if (isWriterIrArtifactFile(slot)) return true;
  const declaredJson = slot.content_type === 'json' || raw.type === 'json';
  if (expectedType !== 'text' && !declaredJson) return false;
  if (isJsonArtifactFile(slot)) return true;
  const hasPath = Boolean(String(raw.path ?? raw.url ?? '').trim());
  return hasPath && declaredJson;
}

function shouldRenderMarkdownFileAsContent(
  slot: SlotRevision,
  expectedType?: 'image' | 'file' | 'text',
): boolean {
  return expectedType === 'file' && isMarkdownArtifactFile(slot);
}

interface SlotJsonFileProps {
  slot: SlotRevision;
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  onRefresh?: () => void;
  readOnly?: boolean;
}

function WriterWriteBackSummary({
  slot,
  revision,
}: {
  slot: SlotRevision;
  revision: number;
}) {
  if (slot.slot_id !== 'draft_document' || !Number.isFinite(revision) || revision <= 0) {
    return null;
  }

  const state = slot.write_back_state ?? 'blocked';
  const stateKey = {
    blocked: 'chat.writerIR.writeBackBlocked',
    initial_delivery: 'chat.writerIR.initialDelivery',
    synced_clean: 'chat.writerIR.syncedClean',
    synced_dirty: 'chat.writerIR.syncedDirty',
  }[state] ?? 'chat.writerIR.writeBackBlocked';

  return (
    <div className={`workflow-slot__writer-writeback-summary workflow-slot__writer-writeback-summary--${state}`} role='status' aria-live='polite'>
      <span>{tr('chat.writerIR.localVersion', { version: revision })}</span>
      {typeof slot.last_synced_revision === 'number' && (
        <span>{tr('chat.writerIR.syncedToVersion', { version: slot.last_synced_revision })}</span>
      )}
      <span>{tr(stateKey)}</span>
      {slot.write_back_url && (
        <a href={slot.write_back_url} target='_blank' rel='noreferrer'>
          {tr('chat.writerIR.openFeishuDocument')}
        </a>
      )}
    </div>
  );
}

function isWriterWriteBackDisabled(
  slot: SlotRevision,
  canWriteBack: boolean,
  revision: number,
  locallyEditing = false,
): boolean {
  if (!canWriteBack) return true;
  if (slot.write_back_state !== 'synced_clean') return false;

  // A local save can advance the displayed revision before the next session
  // projection refresh updates write_back_state to synced_dirty. Keep write-back
  // available for that known newer local revision, and let the shared footer
  // flush an in-progress editor before requesting the server write-back.
  return !locallyEditing && !(
    typeof slot.last_synced_revision === 'number'
    && revision > slot.last_synced_revision
  );
}

function useRegisterWriterWriteBack({
  enabled,
  initialDelivery,
  synced,
  actionKey,
  sessionId,
  revision,
  getLatestRevision,
  writeBackUrl: serverWriteBackUrl,
  disabled,
  onSuccess,
  onConflict,
}: {
  enabled: boolean;
  initialDelivery?: boolean;
  synced?: boolean;
  actionKey?: string;
  sessionId?: string;
  revision: number;
  getLatestRevision?: () => number;
  writeBackUrl?: string;
  disabled?: boolean;
  onSuccess?: (revision: number, document: WriterDocument) => void;
  onConflict?: () => void;
}) {
  const tabActive = useContext(PluginPanelTabActiveContext);
  const { registerFooterAction } = useContext(SlotEditingContext);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error' | 'conflict'>('idle');
  const writeBackUrl = serverWriteBackUrl;

  const writeBack = useCallback(async () => {
    if (!sessionId) return;
    setStatus('loading');
    try {
      const currentRevision = getLatestRevision?.() ?? revision;
      const response = await WorkflowSessionApi().writeBackWriterDocument(
        sessionId,
        currentRevision,
        undefined,
        undefined,
        { silentError: true } as never,
      );
      const result = response?.data?.data;
      if (
        response?.data?.code !== 0
        || result?.status !== 'synced'
        || result.feishu_synced !== true
        || result.artifact_saved !== true
        || typeof result.revision !== 'number'
        || result.patch_result?.success !== true
        || !isWriterDocument(result.document)
      ) {
        throw new Error(tr('chat.writerIR.writeBackFailed'));
      }
      setStatus('success');
      onSuccess?.(result.revision, result.document);
    } catch (error) {
      if ((error as { response?: { status?: number } })?.response?.status === 409) {
        setStatus('conflict');
        onConflict?.();
      } else {
        setStatus('error');
      }
    }
  }, [getLatestRevision, onConflict, onSuccess, revision, sessionId]);

  useEffect(() => {
    if (!enabled || !tabActive || !actionKey || !sessionId) return undefined;
    return registerFooterAction(actionKey, {
      label: status === 'loading'
        ? tr(initialDelivery ? 'chat.writerIR.writingToFeishu' : 'chat.writerIR.writingBack')
        : tr(initialDelivery ? 'chat.writerIR.writeToFeishu' : 'chat.writerIR.writeBack'),
      order: 30,
      tone: 'primary',
      icon: 'write-back',
      disabled: disabled || status === 'loading',
      flushBeforeAction: true,
      onClick: () => {
        void writeBack();
      },
      statusText: status === 'success' || (synced && status === 'idle')
        ? tr('chat.writerIR.writeBackSuccess')
        : status === 'error'
          ? tr('chat.writerIR.writeBackFailed')
          : status === 'conflict'
            ? tr('chat.writerIR.revisionConflict')
          : undefined,
      statusTone: status === 'success' || (synced && status === 'idle')
        ? 'success'
        : status === 'error' || status === 'conflict' ? 'error' : undefined,
      statusLink: writeBackUrl
        ? { href: writeBackUrl, label: tr('chat.writerIR.openFeishuDocument') }
        : undefined,
    });
  }, [
    actionKey,
    disabled,
    enabled,
    initialDelivery,
    registerFooterAction,
    sessionId,
    status,
    synced,
    tabActive,
    writeBack,
    writeBackUrl,
  ]);
}

function useRegisterArtifactDownload({
  enabled,
  actionKey,
  label,
  url,
  filename,
}: {
  enabled: boolean;
  actionKey?: string;
  label: string;
  url?: string | null;
  filename?: string;
}) {
  const tabActive = useContext(PluginPanelTabActiveContext);
  const { registerFooterAction } = useContext(SlotEditingContext);

  useEffect(() => {
    if (!enabled || !tabActive || !actionKey || !url) return undefined;
    return registerFooterAction(actionKey, {
      label,
      order: 10,
      tone: 'secondary',
      icon: 'download',
      onClick: () => {
        const anchor = document.createElement('a');
        anchor.href = url;
        if (filename) anchor.download = filename;
        anchor.click();
      },
    });
  }, [actionKey, enabled, filename, label, registerFooterAction, tabActive, url]);
}

function SlotJsonFile({
  slot,
  sessionId,
  slotId,
  revisionCount,
  onRefresh,
  readOnly,
}: SlotJsonFileProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const raw = slot.artifact_value;
  const name = String(raw?.filename ?? raw?.name ?? slotId ?? slot.slot);
  const [reloadToken, setReloadToken] = useState(0);
  const { url, resolving, hasSource, sourceKey } = useArtifactFileUrl(
    raw,
    `${slot.revision}:${reloadToken}`,
  );
  const pluginStore = useWorkflowStore();
  const { patchSlotItemValue } = pluginStore;
  const mediaLibrary = useWriterMediaLibrary(sessionId);
  const { setEditing: notifyEditing } = useContext(SlotEditingContext);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [payload, setPayload] = useState<unknown>(null);
  const [sourceJson, setSourceJson] = useState<unknown>(null);
  const [loadedSourceKey, setLoadedSourceKey] = useState('');
  const [loadedRevision, setLoadedRevision] = useState<number>();
  const [localRevisionCount, setLocalRevisionCount] = useState<number | undefined>(revisionCount);
  const [writerEditing, setWriterEditing] = useState(false);
  const [rewriteSelection, setRewriteSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [rewritePreview, setRewritePreview] = useState<{
    selection: ArtifactRewriteSelection;
    preview: RewriteSelectionPreview;
  } | null>(null);
  const hasPayloadRef = useRef(false);
  const latestRevisionRef = useRef(slot.revision);
  hasPayloadRef.current = payload !== null;

  const applySavedRevision = useCallback((revision?: number) => {
    if (typeof revision !== 'number' || revision <= 0) return;
    latestRevisionRef.current = Math.max(latestRevisionRef.current, revision);
    setLoadedRevision((prev) => (prev === undefined || revision > prev ? revision : prev));
    setLocalRevisionCount((prev) => Math.max(prev ?? 0, revisionCount ?? 0, revision));
  }, [revisionCount]);

  useEffect(() => {
    if (typeof slot.revision === 'number' && slot.revision > 0) {
      latestRevisionRef.current = Math.max(latestRevisionRef.current, slot.revision);
      setLoadedRevision((prev) => (prev === undefined || slot.revision >= prev ? slot.revision : prev));
    }
  }, [slot.revision]);

  useEffect(() => {
    if (typeof revisionCount === 'number' && revisionCount > 0) {
      setLocalRevisionCount((prev) => (prev === undefined || revisionCount >= prev ? revisionCount : prev));
    }
  }, [revisionCount]);

  useEffect(() => {
    if (!hasSource) return;
    setError(null);
  }, [hasSource, sourceKey]);

  useEffect(() => {
    if (!hasSource) {
      setLoading(false);
      setError(localizeErrorCode('2000509'));
      setPayload(null);
      setSourceJson(null);
      setLoadedSourceKey('');
      setLoadedRevision(undefined);
      return;
    }
    if (resolving) return;
    if (!url) {
      setLoading(false);
      setError(localizeErrorCode('2000509'));
      return;
    }

    const controller = new AbortController();
    // Soft refresh: keep the editor mounted when content is already on screen.
    if (!hasPayloadRef.current) setLoading(true);
    setError(null);

    fetch(url, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) {
          throw new Error(localizeErrorCode('2000509'));
        }
        return response.json();
      })
      .then((json) => {
        setSourceJson(json);
        setPayload(unwrapArtifactPayload(json));
        setLoadedSourceKey(sourceKey);
        // Prefer a newer locally-saved revision over a stale session snapshot.
        setLoadedRevision((prev) => (
          typeof prev === 'number' && prev > slot.revision ? prev : slot.revision
        ));
        setLoading(false);
      })
      .catch((fetchError: unknown) => {
        if (fetchError instanceof DOMException && fetchError.name === 'AbortError') return;
        setError(localizeErrorCode('2000509'));
        setLoading(false);
      });

    return () => controller.abort();
  }, [hasSource, reloadToken, resolving, slot.revision, sourceKey, url]);

  const apiListIndex = slot.list_index ?? -1;
  const resolvedSlotId = slotId ?? slot.slot;
  const writerDocument = useMemo(() => isWriterDocument(payload)
    ? restoreLegacyWriterImageReference(payload, mediaLibrary)
    : null, [mediaLibrary, payload]);

  const usesWriterSync = resolvedSlotId !== 'draft_document'
    && apiListIndex === -1
    && hasProviderTarget(writerDocument);
  const displayRevision = loadedRevision ?? slot.revision;
  const displayRevisionCount = localRevisionCount ?? revisionCount;
  const getLatestRevision = useCallback(() => latestRevisionRef.current, []);
  const hasFeishuWriteBackTarget = resolvedSlotId === 'draft_document'
    && Boolean(sessionId)
    && apiListIndex === -1
    && !readOnly
    && slot.write_back_ready === true
    && typeof displayRevision === 'number'
    && displayRevision > 0;
  const initialDelivery = slot.write_back_state === 'initial_delivery';
  const canWriteBack = hasFeishuWriteBackTarget
    && (initialDelivery || slot.write_back_state === 'synced_clean' || slot.write_back_state === 'synced_dirty');
  const writeBackDisabled = isWriterWriteBackDisabled(
    slot,
    canWriteBack,
    displayRevision,
    writerEditing,
  );
  const canEditWriterIR = Boolean(sessionId && slotId)
    && !readOnly
    && writerDocument?.ui_editable === true
    && (loadedSourceKey === sourceKey || writerEditing);
  const editingKey = `${sessionId}:${slotId}:${apiListIndex}:writer-ir`;
  const showVersionBadge =
    displayRevisionCount !== undefined && displayRevisionCount > 0 && Boolean(sessionId && slotId);
  const canRewriteIR = Boolean(sessionId && slotId)
    && !readOnly
    && writerDocument !== null
    && typeof displayRevision === 'number'
    && displayRevision > 0
    && rewriteSelection === null
    && rewritePreview === null;

  const handleSaveWriterDocument = useCallback(async (
    sourceDocument: WriterDocument,
    document: WriterDocument,
    sourceRevision?: string | number,
    mode: WriterIRSaveMode = 'checkpoint',
  ): Promise<WriterIRSaveResult | void> => {
    if (!sessionId || !slotId || readOnly) {
      throw new Error(tr('chat.writerIR.saveFailed'));
    }

    if (usesWriterSync) {
      try {
        const result = await syncWriterDocumentSlot(
          sessionId,
          slot.slot_id,
          apiListIndex,
          sourceRevision,
          sourceDocument,
          document,
          mode,
        );
        const serialized = replaceStructuredArtifactPayload(sourceJson, result.document);
        setSourceJson(serialized);
        // Prefer the client snapshot reference so WriterIRControl can treat the
        // prop update as identity-equal to its in-flight draft and skip a redraw.
        setPayload(document);
        applySavedRevision(
          typeof result.sourceRevision === 'number' ? result.sourceRevision : undefined,
        );
        // Keep the editor mounted: local payload/revision are already authoritative.
        // Session polling will catch up without a hard refresh.
        return {
          ...result,
          document,
        };
      } catch (syncError) {
        onRefresh?.();
        throw syncError;
      }
    }

    const serialized = replaceStructuredArtifactPayload(sourceJson, document);
    const filename = ensureWriterIrFilename(name);
    const file = new File(
      [JSON.stringify(serialized, null, 2)],
      filename,
      { type: 'application/json' },
    );
    const storedPath = await uploadFileInChunks(file);
    const nextValue: Record<string, unknown> = {
      ...(raw && typeof raw === 'object' ? raw : {}),
      path: storedPath,
      filename,
      size: file.size,
    };
    delete nextValue.url;

    const persistMode = resolvedSlotId === 'draft_document' ? 'draft' : mode;
    const revision = await patchSlotItemValue(
      sessionId, slotId, apiListIndex, nextValue, 'file', persistMode,
    );
    setSourceJson(serialized);
    setPayload(document);
    applySavedRevision(revision);
    return {
      document,
      sourceRevision: typeof revision === 'number' ? revision : sourceRevision,
    };
  }, [
    apiListIndex,
    applySavedRevision,
    name,
    onRefresh,
    patchSlotItemValue,
    raw,
    readOnly,
    resolvedSlotId,
    sessionId,
    slot.slot_id,
    slotId,
    sourceJson,
    usesWriterSync,
  ]);

  const handleWriterEditingChange = useCallback((editing: boolean) => {
    setWriterEditing(editing);
    notifyEditing(editingKey, editing);
  }, [editingKey, notifyEditing]);

  const openIRRewrite = useCallback((selection: {
    nodeId: string;
    selectedText: string;
    anchor?: ArtifactRewriteSelection['anchor'];
  }) => {
    if (!canRewriteIR) return;
    setRewriteSelection({
      type: 'ir',
      node_id: selection.nodeId,
      selectedText: selection.selectedText,
      anchor: selection.anchor,
    });
  }, [canRewriteIR]);

  const handleIRRewriteApplied = useCallback((revision?: number) => {
    if (writerDocument && rewritePreview?.selection.type === 'ir') {
      setPayload(updateWriterBlockContent(
        writerDocument,
        rewritePreview.selection.node_id,
        rewritePreview.preview.preview.new_text,
      ));
    }
    applySavedRevision(revision);
    setRewriteSelection(null);
    setRewritePreview(null);
    setReloadToken((value) => value + 1);
    onRefresh?.();
  }, [applySavedRevision, onRefresh, rewritePreview, writerDocument]);

  const handleIRRewritePreview = useCallback((preview: RewriteSelectionPreview) => {
    if (rewriteSelection?.type !== 'ir') return;
    setRewritePreview({ selection: rewriteSelection, preview });
  }, [rewriteSelection]);

  const rejectIRRewrite = useCallback(() => {
    setRewritePreview(null);
  }, []);

  const handleWriteBackSuccess = useCallback((revision: number) => {
    setPayload(document);
    applySavedRevision(revision);
    onRefresh?.();
  }, [applySavedRevision, onRefresh]);

  const [writerMarkdownDownload, setWriterMarkdownDownload] = useState<{
    url: string;
    filename: string;
  } | null>(null);

  useEffect(() => {
    if (!writerDocument) {
      setWriterMarkdownDownload(null);
      return;
    }
    const blob = new Blob([writerDocumentToMarkdown(writerDocument)], {
      type: 'text/markdown;charset=utf-8',
    });
    const url = URL.createObjectURL(blob);
    setWriterMarkdownDownload({
      url,
      filename: writerMarkdownFilename(name),
    });
    return () => URL.revokeObjectURL(url);
  }, [name, writerDocument]);

  useRegisterWriterWriteBack({
    enabled: canWriteBack,
    initialDelivery,
    actionKey: sessionId && slotId ? `${editingKey}:writeback` : undefined,
    sessionId,
    revision: displayRevision,
    getLatestRevision,
    writeBackUrl: slot.write_back_url,
    disabled: writeBackDisabled,
    synced: slot.write_back_state === 'synced_clean',
    onSuccess: handleWriteBackSuccess,
    onConflict: onRefresh,
  });

  useRegisterArtifactDownload({
    enabled: allowDownload && Boolean(writerMarkdownDownload || url),
    actionKey: sessionId && slotId ? `${editingKey}:download` : undefined,
    label: writerMarkdownDownload
      ? tr('chat.writer.downloadMarkdown')
      : tr('chat.slots.download'),
    url: writerMarkdownDownload?.url ?? (allowDownload ? url : undefined),
    filename: writerMarkdownDownload?.filename ?? name,
  });

  if (!hasSource) {
    return (
      <div className='workflow-slot workflow-slot--text workflow-slot--pending'>
        <span className='workflow-slot__placeholder'>{tr('chat.slots.pendingGeneration')}</span>
      </div>
    );
  }

  if ((loading || resolving) && payload === null) {
    return (
      <div className='workflow-slot workflow-slot--artifact workflow-slot--pending'>
        <span className='workflow-slot__placeholder'>{tr('common.loading')}</span>
      </div>
    );
  }

  if (payload === null) {
    return (
      <div className='workflow-slot workflow-slot--artifact workflow-slot--error'>
        <span className='workflow-slot__placeholder'>{error ?? tr('chat.slots.contentLoadFailed')}</span>
        <button
          className='workflow-slot__file-action-btn'
          type='button'
          onClick={() => setReloadToken((value) => value + 1)}
        >
          {tr('common.retry')}
        </button>
      </div>
    );
  }

  return (
    <div className='workflow-slot workflow-slot--artifact'>
      <div className='workflow-slot__artifact-body'>
        {loadedSourceKey !== sourceKey && !error && payload === null && (
          <div className='writer-ir__notice writer-ir__notice--warning' role='status'>
            {tr('chat.writerIR.refreshing')}
          </div>
        )}
        {error && loadedSourceKey !== sourceKey && (
          <div className='writer-ir__notice writer-ir__notice--error' role='alert'>
            <span>{tr('chat.writerIR.refreshFailed')}</span>
            <button
              className='workflow-slot__file-action-btn'
              type='button'
              onClick={() => setReloadToken((value) => value + 1)}
            >
              {tr('common.retry')}
            </button>
          </div>
        )}
        {writerDocument ? (
          <WriterIRControl
            document={writerDocument}
            sourceRevision={displayRevision}
            readOnly={!canEditWriterIR}
            editingKey={editingKey}
            feishuVersionOnly={resolvedSlotId === 'draft_document'}
            onSave={canEditWriterIR ? handleSaveWriterDocument : undefined}
            onEditingChange={handleWriterEditingChange}
            onRewriteSelection={canRewriteIR ? openIRRewrite : undefined}
            rewriteDialogOpen={rewriteSelection !== null}
            rewritePreview={rewritePreview?.selection.type === 'ir' ? {
              nodeId: rewritePreview.selection.node_id,
              sessionId: sessionId ?? '',
              slotId: slotId ?? '',
              listIndex: apiListIndex,
              preview: rewritePreview.preview,
            } : null}
            onRewritePreviewApplied={handleIRRewriteApplied}
            onRewritePreviewRejected={rejectIRRewrite}
          />
        ) : (
          <WriterArtifactContent slotId={resolvedSlotId} data={payload} hideDownload={!allowDownload} />
        )}
      </div>
      <WriterWriteBackSummary slot={slot} revision={displayRevision} />
      <div className='workflow-slot__artifact-footer'>
        <div className='workflow-slot__artifact-footer-left'>
          {showVersionBadge && !writerEditing && (
            <SlotVersionPopover
              sessionId={sessionId!}
              slotId={slotId!}
              listIndex={apiListIndex}
              revisionCount={displayRevisionCount!}
              currentRevision={displayRevision}
              currentValue={slot.artifact_value}
              currentChangeSource={slot.change_source}
              contentType='json'
              onRollbackDone={onRefresh}
            />
          )}
        </div>
      </div>
      <ArtifactRewriteDialog
        open={rewriteSelection !== null}
        sessionId={sessionId ?? ''}
        slotId={slotId ?? ''}
        listIndex={apiListIndex}
        baseRevision={displayRevision}
        selection={rewriteSelection}
        onClose={() => setRewriteSelection(null)}
        onApplied={handleIRRewriteApplied}
        onPreviewReady={handleIRRewritePreview}
      />
    </div>
  );
}

interface SlotInlineStructuredProps {
  slot: SlotRevision;
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  onRefresh?: () => void;
  readOnly?: boolean;
}

function SlotInlineStructured({
  slot,
  sessionId,
  slotId,
  revisionCount,
  onRefresh,
  readOnly,
}: SlotInlineStructuredProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const payload = getInlineStructuredArtifactPayload(slot);
  const { patchSlotItemValue } = useWorkflowStore();
  const mediaLibrary = useWriterMediaLibrary(sessionId);
  const { setEditing: notifyEditing } = useContext(SlotEditingContext);
  const [writerEditing, setWriterEditing] = useState(false);
  const [rewriteSelection, setRewriteSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [rewritePreview, setRewritePreview] = useState<{
    selection: ArtifactRewriteSelection;
    preview: RewriteSelectionPreview;
  } | null>(null);
  const [localRevision, setLocalRevision] = useState(slot.revision);
  const [localRevisionCount, setLocalRevisionCount] = useState<number | undefined>(revisionCount);
  const apiListIndex = slot.list_index ?? -1;
  const resolvedSlotId = slotId ?? slot.slot;
  const writerDocument = useMemo(() => isWriterDocument(payload)
    ? restoreLegacyWriterImageReference(payload, mediaLibrary)
    : null, [mediaLibrary, payload]);

  const usesWriterSync = resolvedSlotId !== 'draft_document'
    && apiListIndex === -1
    && hasProviderTarget(writerDocument);
  const displayRevision = localRevision ?? slot.revision;
  const displayRevisionCount = localRevisionCount ?? revisionCount;
  const hasFeishuWriteBackTarget = resolvedSlotId === 'draft_document'
    && Boolean(sessionId)
    && apiListIndex === -1
    && !readOnly
    && slot.write_back_ready === true
    && typeof displayRevision === 'number'
    && displayRevision > 0;
  const initialDelivery = slot.write_back_state === 'initial_delivery';
  const canWriteBack = hasFeishuWriteBackTarget
    && (initialDelivery || slot.write_back_state === 'synced_clean' || slot.write_back_state === 'synced_dirty');
  const writeBackDisabled = isWriterWriteBackDisabled(
    slot,
    canWriteBack,
    displayRevision,
    writerEditing,
  );
  const canEditWriterIR = Boolean(sessionId && slotId)
    && !readOnly
    && writerDocument?.ui_editable === true;
  const editingKey = `${sessionId}:${slotId}:${apiListIndex}:writer-ir`;
  const showVersionBadge =
    displayRevisionCount !== undefined && displayRevisionCount > 0 && Boolean(sessionId && slotId);
  const canRewriteIR = Boolean(sessionId && slotId)
    && !readOnly
    && writerDocument !== null
    && typeof displayRevision === 'number'
    && displayRevision > 0
    && rewriteSelection === null
    && rewritePreview === null;
  const [writerMarkdownDownload, setWriterMarkdownDownload] = useState<{
    url: string;
    filename: string;
  } | null>(null);

  const applySavedRevision = useCallback((revision?: number) => {
    if (typeof revision !== 'number' || revision <= 0) return;
    setLocalRevision((prev) => (prev === undefined || revision > prev ? revision : prev));
    setLocalRevisionCount((prev) => Math.max(prev ?? 0, revisionCount ?? 0, revision));
  }, [revisionCount]);

  useEffect(() => {
    if (typeof slot.revision === 'number' && slot.revision > 0) {
      setLocalRevision((prev) => (prev === undefined || slot.revision >= prev ? slot.revision : prev));
    }
  }, [slot.revision]);

  useEffect(() => {
    if (typeof revisionCount === 'number' && revisionCount > 0) {
      setLocalRevisionCount((prev) => (prev === undefined || revisionCount >= prev ? revisionCount : prev));
    }
  }, [revisionCount]);

  useEffect(() => {
    if (!writerDocument) {
      setWriterMarkdownDownload(null);
      return;
    }
    const blob = new Blob([writerDocumentToMarkdown(writerDocument)], {
      type: 'text/markdown;charset=utf-8',
    });
    const url = URL.createObjectURL(blob);
    setWriterMarkdownDownload({
      url,
      filename: writerMarkdownFilename(slot.caption || resolvedSlotId),
    });
    return () => URL.revokeObjectURL(url);
  }, [resolvedSlotId, slot.caption, writerDocument]);

  const handleSaveWriterDocument = useCallback(async (
    sourceDocument: WriterDocument,
    document: WriterDocument,
    sourceRevision?: string | number,
    mode: WriterIRSaveMode = 'checkpoint',
  ): Promise<WriterIRSaveResult | void> => {
    if (!sessionId || !slotId || readOnly) {
      throw new Error(tr('chat.writerIR.saveFailed'));
    }
    if (usesWriterSync) {
      try {
        const result = await syncWriterDocumentSlot(
          sessionId,
          slot.slot_id,
          apiListIndex,
          sourceRevision,
          sourceDocument,
          document,
          mode,
        );
        applySavedRevision(
          typeof result.sourceRevision === 'number' ? result.sourceRevision : undefined,
        );
        // Avoid hard session refresh; WriterIRControl already applied the result.
        return result;
      } catch (syncError) {
        onRefresh?.();
        throw syncError;
      }
    }
    const serialized = replaceStructuredArtifactPayload(slot.artifact_value, document);
    const persistMode = resolvedSlotId === 'draft_document' ? 'draft' : mode;
    const revision = await patchSlotItemValue(
      sessionId, slotId, apiListIndex, serialized, 'json', persistMode,
    );
    applySavedRevision(revision);
    return {
      document,
      sourceRevision: typeof revision === 'number' ? revision : sourceRevision,
    };
  }, [
    apiListIndex,
    applySavedRevision,
    onRefresh,
    patchSlotItemValue,
    readOnly,
    resolvedSlotId,
    sessionId,
    slot,
    slotId,
    usesWriterSync,
  ]);

  const handleWriterEditingChange = useCallback((editing: boolean) => {
    setWriterEditing(editing);
    notifyEditing(editingKey, editing);
  }, [editingKey, notifyEditing]);

  const openIRRewrite = useCallback((selection: {
    nodeId: string;
    selectedText: string;
    anchor?: ArtifactRewriteSelection['anchor'];
  }) => {
    if (!canRewriteIR) return;
    setRewriteSelection({
      type: 'ir',
      node_id: selection.nodeId,
      selectedText: selection.selectedText,
      anchor: selection.anchor,
    });
  }, [canRewriteIR]);

  const handleIRRewriteApplied = useCallback((revision?: number) => {
    applySavedRevision(revision);
    setRewriteSelection(null);
    setRewritePreview(null);
    onRefresh?.();
  }, [applySavedRevision, onRefresh]);

  const handleIRRewritePreview = useCallback((preview: RewriteSelectionPreview) => {
    if (rewriteSelection?.type !== 'ir') return;
    setRewritePreview({ selection: rewriteSelection, preview });
  }, [rewriteSelection]);

  const rejectIRRewrite = useCallback(() => {
    setRewritePreview(null);
  }, []);

  const handleWriteBackSuccess = useCallback((revision: number) => {
    applySavedRevision(revision);
    onRefresh?.();
  }, [applySavedRevision, onRefresh]);

  useRegisterWriterWriteBack({
    enabled: canWriteBack,
    initialDelivery,
    actionKey: sessionId && slotId ? `${editingKey}:writeback` : undefined,
    sessionId,
    revision: displayRevision,
    writeBackUrl: slot.write_back_url,
    disabled: writeBackDisabled,
    synced: slot.write_back_state === 'synced_clean',
    onSuccess: handleWriteBackSuccess,
    onConflict: onRefresh,
  });

  useRegisterArtifactDownload({
    enabled: allowDownload && Boolean(writerMarkdownDownload),
    actionKey: sessionId && slotId ? `${editingKey}:download` : undefined,
    label: tr('chat.writer.downloadMarkdown'),
    url: writerMarkdownDownload?.url,
    filename: writerMarkdownDownload?.filename,
  });

  if (payload === null) {
    return (
      <div className='workflow-slot workflow-slot--artifact workflow-slot--error'>
        <span className='workflow-slot__placeholder'>{tr('chat.slots.contentLoadFailed')}</span>
      </div>
    );
  }

  return (
    <div className='workflow-slot workflow-slot--artifact'>
      <div className='workflow-slot__artifact-body'>
        {writerDocument ? (
          <WriterIRControl
            document={writerDocument}
            sourceRevision={displayRevision}
            readOnly={!canEditWriterIR}
            editingKey={editingKey}
            feishuVersionOnly={resolvedSlotId === 'draft_document'}
            onSave={canEditWriterIR ? handleSaveWriterDocument : undefined}
            onEditingChange={handleWriterEditingChange}
            onRewriteSelection={canRewriteIR ? openIRRewrite : undefined}
            rewriteDialogOpen={rewriteSelection !== null}
            rewritePreview={rewritePreview?.selection.type === 'ir' ? {
              nodeId: rewritePreview.selection.node_id,
              sessionId: sessionId ?? '',
              slotId: slotId ?? '',
              listIndex: apiListIndex,
              preview: rewritePreview.preview,
            } : null}
            onRewritePreviewApplied={handleIRRewriteApplied}
            onRewritePreviewRejected={rejectIRRewrite}
          />
        ) : (
          <WriterArtifactContent slotId={resolvedSlotId} data={payload} hideDownload={!allowDownload} />
        )}
      </div>
      <WriterWriteBackSummary slot={slot} revision={displayRevision} />
      <div className='workflow-slot__artifact-footer'>
        <div className='workflow-slot__artifact-footer-left'>
          {showVersionBadge && !writerEditing && (
            <SlotVersionPopover
              sessionId={sessionId!}
              slotId={slotId!}
              listIndex={apiListIndex}
              revisionCount={displayRevisionCount!}
              currentRevision={displayRevision}
              currentValue={slot.artifact_value}
              currentChangeSource={slot.change_source}
              contentType='json'
              onRollbackDone={onRefresh}
            />
          )}
        </div>
      </div>
      <ArtifactRewriteDialog
        open={rewriteSelection !== null}
        sessionId={sessionId ?? ''}
        slotId={slotId ?? ''}
        listIndex={apiListIndex}
        baseRevision={displayRevision}
        selection={rewriteSelection}
        onClose={() => setRewriteSelection(null)}
        onApplied={handleIRRewriteApplied}
        onPreviewReady={handleIRRewritePreview}
      />
    </div>
  );
}

interface SlotMarkdownFileProps {
  slot: SlotRevision;
  originalFileSlot?: SlotRevision;
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  onRefresh?: () => void;
  readOnly?: boolean;
}

/** Displays the temporary Markdown emitted by a Writer Task SSE stream. */
export function SlotMarkdownStream({ stream }: { stream: TaskArtifactStream }) {
  const content = stream.final_content ?? stream.content;
  const isAborted = stream.state === 'aborted';
  const showError = isAborted && !content;
  const bodyRef = useRef<HTMLDivElement>(null);
  const autoFollowRef = useRef(true);
  const status = isAborted
    ? stream.message || tr('chat.slots.contentLoadFailed')
    : stream.final_content_error || (stream.state === 'streaming'
      ? tr('chat.slots.inProgress')
      : tr('common.loading'));

  useEffect(() => {
    autoFollowRef.current = true;
  }, [stream.stream_id]);

  useEffect(() => {
    if (!content || !autoFollowRef.current) return undefined;
    const frame = window.requestAnimationFrame(() => {
      const body = bodyRef.current;
      if (!body || !autoFollowRef.current) return;
      body.scrollTop = body.scrollHeight;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [content, stream.stream_id]);

  const handleScroll = useCallback((event: React.UIEvent<HTMLDivElement>) => {
    const body = event.currentTarget;
    const distanceToBottom = body.scrollHeight - body.scrollTop - body.clientHeight;
    autoFollowRef.current = distanceToBottom <= 24;
  }, []);

  return (
    <div className={`workflow-slot workflow-slot--artifact workflow-slot--artifact-stream${showError ? ' workflow-slot--error' : ''}`}>
      <div className='workflow-slot__artifact-stream-status' role='status' aria-live='polite'>
        {!isAborted && stream.state === 'streaming' && (
          <span className='workflow-slot__artifact-stream-cursor' aria-hidden='true' />
        )}
        <span>{status}</span>
      </div>
      {content ? (
        <div
          ref={bodyRef}
          className='workflow-slot__artifact-body'
          onScroll={handleScroll}
        >
          <div className='writer-artifact__markdown'>
            <MarkdownViewer>{content}</MarkdownViewer>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function SlotMarkdownFile({
  slot,
  originalFileSlot,
  sessionId,
  slotId,
  revisionCount,
  onRefresh,
  readOnly,
}: SlotMarkdownFileProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const raw = slot.artifact_value;
  const name: string = raw?.filename ?? raw?.name ?? slotId ?? slot.slot;
  const [reloadToken, setReloadToken] = useState(0);
  const { url, resolving, hasSource } = useArtifactFileUrl(raw, `${slot.revision}:${reloadToken}`);
  const originalRaw = originalFileSlot?.artifact_value;
  const originalName: string = originalRaw?.filename ?? originalRaw?.name ?? 'final_document.lmd';
  const { url: originalUrl } = useArtifactFileUrl(originalRaw);
  const { patchSlotItemValue } = useWorkflowStore();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [content, setContent] = useState('');
  const [currentValue, setCurrentValue] = useState(raw);
  const [localRevision, setLocalRevision] = useState(slot.revision);
  const [localRevisionCount, setLocalRevisionCount] = useState<number | undefined>(revisionCount);
  const [rewriteSelection, setRewriteSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [rewritePreview, setRewritePreview] = useState<{
    selection: ArtifactRewriteSelection;
    preview: RewriteSelectionPreview;
  } | null>(null);
  const [renderedSelection, setRenderedSelection] = useState<MarkdownSelection | null>(null);
  const markdownPreviewRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!hasSource) {
      setLoading(false);
      setError(localizeErrorCode('2000509'));
      return;
    }
    if (resolving || !url) {
      return;
    }

    const controller = new AbortController();
    setLoading(true);
    setError(null);

    fetch(url, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) {
          throw new Error(localizeErrorCode('2000509'));
        }
        return response.text();
      })
      .then((text) => {
        if (isSpaFallbackHtml(text)) {
          throw new Error('invalid artifact content');
        }
        setContent(text);
        setLoading(false);
      })
      .catch((fetchError: unknown) => {
        if (fetchError instanceof DOMException && fetchError.name === 'AbortError') return;
        setError(localizeErrorCode('2000509'));
        setLoading(false);
      });

    return () => {
      controller.abort();
    };
  }, [hasSource, resolving, url]);

  useEffect(() => {
    if (slot.revision < localRevision) return;
    setLocalRevision(slot.revision);
    setCurrentValue(raw);
  }, [localRevision, raw, slot.revision]);

  useEffect(() => {
    if (typeof revisionCount !== 'number') return;
    setLocalRevisionCount((previous) => Math.max(previous ?? 0, revisionCount));
  }, [revisionCount]);

  const apiListIndex = slot.list_index ?? -1;
  const displayRevision = localRevision;
  const displayRevisionCount = localRevisionCount ?? revisionCount;
  const showVersionBadge =
    displayRevisionCount !== undefined && displayRevisionCount > 0 && Boolean(sessionId && slotId);
  const resolvedSlotId = slotId ?? slot.slot;
  const showArtifactActions = !WRITER_ARTIFACT_SLOT_IDS.has(resolvedSlotId);
  const initialDelivery = slot.write_back_state === 'initial_delivery';
  const canWriteBack = resolvedSlotId === 'draft_document'
    && Boolean(sessionId)
    && apiListIndex === -1
    && !readOnly
    && slot.write_back_ready === true
    && typeof displayRevision === 'number'
    && displayRevision > 0
    && (initialDelivery || slot.write_back_state === 'synced_clean' || slot.write_back_state === 'synced_dirty');
  const writeBackDisabled = isWriterWriteBackDisabled(slot, canWriteBack, displayRevision);

  const canRewriteMarkdown = Boolean(sessionId && slotId)
    && !readOnly
    && typeof displayRevision === 'number'
    && displayRevision > 0
    && rewriteSelection === null
    && rewritePreview === null;
  const canEditMarkdown = Boolean(sessionId && slotId)
    && !readOnly
    && WRITER_ARTIFACT_SLOT_IDS.has(resolvedSlotId);

  const downloadMarkdown = useCallback(() => {
    const blob = new Blob([content], { type: 'text/markdown;charset=utf-8' });
    const objectUrl = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = objectUrl;
    anchor.download = name.toLowerCase().endsWith('.md') ? name : `${name.replace(/\.[^.]+$/, '') || 'writing_output'}.md`;
    anchor.click();
    URL.revokeObjectURL(objectUrl);
  }, [content, name]);

  const saveMarkdown = useCallback(async (markdown: string, baseRevision: number) => {
    if (!sessionId || !slotId || readOnly) {
      throw new Error(tr('chat.writerMarkdown.saveFailed'));
    }
    const filename = name.toLowerCase().endsWith('.md') || name.toLowerCase().endsWith('.markdown')
      ? name
      : `${name.replace(/\.[^.]+$/, '') || 'writing_output'}.md`;
    const file = new File([markdown], filename, { type: 'text/markdown;charset=utf-8' });
    const storedPath = await uploadFileInChunks(file);
    const nextValue: Record<string, unknown> = {
      ...(raw && typeof raw === 'object' ? raw : {}),
      path: storedPath,
      filename,
      size: file.size,
      document_format: 'markdown',
    };
    delete nextValue.url;

    const revision = await patchSlotItemValue(
      sessionId,
      slotId,
      apiListIndex,
      nextValue,
      'file',
      resolvedSlotId === 'draft_document' ? 'draft' : 'checkpoint',
      baseRevision,
    );
    setContent(markdown);
    setCurrentValue(nextValue);
    if (typeof revision === 'number' && revision > 0) {
      setLocalRevision(revision);
      setLocalRevisionCount((previous) => Math.max(previous ?? 0, revisionCount ?? 0, revision));
    }
    return revision;
  }, [apiListIndex, name, patchSlotItemValue, raw, readOnly, resolvedSlotId, revisionCount, sessionId, slotId]);

  const refreshMarkdown = useCallback(() => {
    setReloadToken((value) => value + 1);
    onRefresh?.();
  }, [onRefresh]);

  const openMarkdownRewrite = useCallback((selection: MarkdownSelection) => {
    if (!canRewriteMarkdown || !selection.text.trim()) return;
    setRewriteSelection({
      type: 'markdown',
      selected_text: selection.text.trim(),
      selectedText: selection.text.trim(),
      anchor: selection.anchor,
      paragraph: selection.paragraph,
      startOffset: selection.startOffset,
    });
  }, [canRewriteMarkdown]);

  const recordRenderedMarkdownSelection = useCallback(() => {
    const root = markdownPreviewRef.current;
    setRenderedSelection(root ? selectedMarkdownParagraph(root) : null);
  }, []);

  useEffect(() => {
    if (!canRewriteMarkdown || canEditMarkdown) return undefined;
    document.addEventListener('selectionchange', recordRenderedMarkdownSelection);
    return () => document.removeEventListener('selectionchange', recordRenderedMarkdownSelection);
  }, [canEditMarkdown, canRewriteMarkdown, recordRenderedMarkdownSelection]);

  const handleMarkdownRewriteApplied = useCallback((revision?: number) => {
    if (typeof revision === 'number' && revision > 0) {
      setLocalRevision(revision);
      setLocalRevisionCount((previous) => Math.max(previous ?? 0, revisionCount ?? 0, revision));
    }
    setRewriteSelection(null);
    setRewritePreview(null);
    setRenderedSelection(null);
    refreshMarkdown();
  }, [refreshMarkdown, revisionCount]);

  const handleMarkdownRewritePreview = useCallback((preview: RewriteSelectionPreview) => {
    if (!rewriteSelection?.paragraph) return;
    setRewritePreview({ selection: rewriteSelection, preview });
    setRenderedSelection(null);
  }, [rewriteSelection]);

  const rejectMarkdownRewrite = useCallback(() => {
    setRewritePreview(null);
  }, []);

  const markdownEditingKey = sessionId && slotId
    ? `${sessionId}:${slotId}:${apiListIndex}:markdown`
    : undefined;

  const handleMarkdownWriteBackSuccess = useCallback(() => {
    onRefresh?.();
  }, [onRefresh]);

  useRegisterWriterWriteBack({
    enabled: canWriteBack,
    initialDelivery,
    actionKey: markdownEditingKey ? `${markdownEditingKey}:writeback` : undefined,
    sessionId,
    revision: displayRevision,
    writeBackUrl: slot.write_back_url,
    disabled: writeBackDisabled,
    synced: slot.write_back_state === 'synced_clean',
    onSuccess: handleMarkdownWriteBackSuccess,
    onConflict: onRefresh,
  });

  if (!hasSource) {
    return (
      <div className='workflow-slot workflow-slot--text workflow-slot--pending'>
        <span className='workflow-slot__placeholder'>{tr('chat.slots.pendingGeneration')}</span>
      </div>
    );
  }

  if (loading || resolving) {
    return (
      <div className='workflow-slot workflow-slot--artifact workflow-slot--pending'>
        <span className='workflow-slot__placeholder'>{tr('common.loading')}</span>
      </div>
    );
  }

  if (error || !content.trim()) {
    return (
      <div className='workflow-slot workflow-slot--artifact workflow-slot--error'>
        <span className='workflow-slot__placeholder'>{error ?? tr('chat.slots.contentLoadFailed')}</span>
        <button
          className='workflow-slot__file-action-btn'
          type='button'
          onClick={refreshMarkdown}
        >
          {tr('common.retry')}
        </button>
      </div>
    );
  }

  return (
    <div className='workflow-slot workflow-slot--artifact'>
      <div className='writer-artifact__output-toolbar' hidden={!allowDownload || (!showArtifactActions && !originalUrl)}>
        {!canEditMarkdown && (
          <button
            type='button'
            className='workflow-slot__file-action-btn writer-artifact__download-btn'
            onClick={downloadMarkdown}
          >
            {tr('chat.writer.downloadMarkdown')}
          </button>
        )}
        {originalUrl ? (
          <a
            href={originalUrl}
            download={originalName}
            className='workflow-slot__file-action-btn'
            onClick={(e) => e.stopPropagation()}
          >
            {tr('chat.slots.downloadOriginalFile')}
          </a>
        ) : null}
      </div>
      <div className={`workflow-slot__artifact-body${canEditMarkdown ? ' workflow-slot__artifact-body--markdown' : ''}`}>
        {canEditMarkdown ? (
          <MarkdownArtifactEditor
            markdown={content}
            sourceRevision={displayRevision}
            editingKey={markdownEditingKey}
            onSave={saveMarkdown}
            onRefresh={refreshMarkdown}
            onDownload={downloadMarkdown}
            onRewriteSelection={rewriteSelection || rewritePreview ? undefined : openMarkdownRewrite}
            rewriteUnavailableReason={rewriteSelection || rewritePreview || canRewriteMarkdown
              ? undefined
              : tr('chat.artifactRewrite.revisionUnavailable')}
            rewritePreview={rewritePreview?.selection.paragraph ? {
              paragraph: rewritePreview.selection.paragraph,
              startOffset: rewritePreview.selection.startOffset,
              sessionId: sessionId ?? '',
              slotId: slotId ?? '',
              listIndex: apiListIndex,
              preview: rewritePreview.preview,
            } : null}
            onRewritePreviewApplied={handleMarkdownRewriteApplied}
            onRewritePreviewRejected={rejectMarkdownRewrite}
          />
        ) : (
          <div
            ref={markdownPreviewRef}
            onMouseUp={recordRenderedMarkdownSelection}
            onKeyUp={recordRenderedMarkdownSelection}
            tabIndex={canRewriteMarkdown ? 0 : undefined}
          >
            {resolvedSlotId === 'writing_output_md' ? (
              <WriterArtifactContent slotId='writing_output' data={{ content }} hideDownload />
            ) : (
              <div className='writer-artifact__markdown'>
                <MarkdownViewer>{content}</MarkdownViewer>
              </div>
            )}
          </div>
        )}
      </div>
      {canRewriteMarkdown && renderedSelection && (
        <ArtifactRewriteSelectionAction
          anchor={renderedSelection.anchor}
          label={renderedSelection.supported
            ? tr('chat.artifactRewrite.action')
            : tr('chat.artifactRewrite.singleParagraphHint')}
          disabled={!renderedSelection.supported}
          onActivate={() => openMarkdownRewrite(renderedSelection)}
          onDismiss={() => setRenderedSelection(null)}
        />
      )}
      <WriterWriteBackSummary slot={slot} revision={displayRevision} />
      <div className='workflow-slot__artifact-footer'>
        <div className='workflow-slot__artifact-footer-left'>
          {showVersionBadge && (
            <SlotVersionPopover
              sessionId={sessionId!}
              slotId={slotId!}
              listIndex={apiListIndex}
              revisionCount={displayRevisionCount!}
              currentRevision={displayRevision}
              currentValue={currentValue}
              currentChangeSource={slot.change_source}
              contentType='file'
              onRollbackDone={onRefresh}
            />
          )}
        </div>
      </div>
      <ArtifactRewriteDialog
        open={rewriteSelection !== null}
        sessionId={sessionId ?? ''}
        slotId={slotId ?? ''}
        listIndex={apiListIndex}
        baseRevision={displayRevision}
        selection={rewriteSelection}
        onClose={() => setRewriteSelection(null)}
        onApplied={handleMarkdownRewriteApplied}
        onPreviewReady={handleMarkdownRewritePreview}
      />
    </div>
  );
}

export function SlotFile({ slot, sessionId, slotId, revisionCount, onRefresh, readOnly }: SlotFileProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const raw = slot.artifact_value;
  const rawPath: string = raw?.url ?? raw?.path ?? '';
  const url: string = rawPath ? resolveCoreAssetUrl(rawPath) : '';
  const name: string = raw?.filename ?? raw?.name ?? slot.slot;
  const size: number | undefined = raw?.size;
  const { deleteSlotItem, patchSlotCaption } = useWorkflowStore();
  const [previewOpen, setPreviewOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [captionEditing, setCaptionEditing] = useState(false);
  const [captionDraft, setCaptionDraft] = useState('');

  const canEdit = Boolean(sessionId && slotId && slot.list_index !== undefined) && !readOnly;

  const handlePreview = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setPreviewOpen(true);
  }, []);

  const handleDeleteClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setConfirmDelete(true);
  }, []);

  const handleDeleteConfirm = useCallback(async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!sessionId || !slotId || slot.list_index === undefined) return;
    await deleteSlotItem(sessionId, slotId, slot.list_index);
    setConfirmDelete(false);
    onRefresh?.();
  }, [sessionId, slotId, slot.list_index, deleteSlotItem, onRefresh]);

  const handleDeleteCancel = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setConfirmDelete(false);
  }, []);

  const handleCaptionEdit = useCallback(() => {
    setCaptionDraft(slot.caption ?? '');
    setCaptionEditing(true);
  }, [slot.caption]);

  const handleCaptionSave = useCallback(async () => {
    if (!sessionId || !slotId || slot.list_index === undefined) return;
    setCaptionEditing(false);
    await patchSlotCaption(sessionId, slotId, slot.list_index, captionDraft);
    onRefresh?.();
  }, [sessionId, slotId, slot.list_index, captionDraft, patchSlotCaption, onRefresh]);

  const handleCaptionKeyDown = useCallback((e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') handleCaptionSave();
    if (e.key === 'Escape') setCaptionEditing(false);
  }, [handleCaptionSave]);

  if (!url) return <SlotPending type='file' />;

  const apiListIndex = slot.list_index ?? -1;
  const showVersionBadge =
    revisionCount !== undefined && revisionCount > 0 && Boolean(sessionId && slotId);

  return (
    <div className='workflow-slot workflow-slot--file workflow-slot--file-enhanced'>
      <div className='workflow-slot__file-card'>
        <div className='workflow-slot__file-card-header'>
          <span className='workflow-slot__file-icon' aria-hidden='true'>{getFileIcon(name)}</span>
          <div className='workflow-slot__file-card-info'>
            <span className='workflow-slot__file-name' title={name}>{name}</span>
            {size !== undefined && (
              <span className='workflow-slot__file-size'>{formatFileSize(size)}</span>
            )}
          </div>
          {showVersionBadge && (
            <SlotVersionPopover
              sessionId={sessionId!}
              slotId={slotId!}
              listIndex={apiListIndex}
              revisionCount={revisionCount!}
              currentRevision={slot.revision}
              currentValue={slot.artifact_value}
              currentChangeSource={slot.change_source}
              contentType='file'
              onRollbackDone={onRefresh}
            />
          )}
        </div>
        <div className='workflow-slot__file-card-actions'>
          <button
            className='workflow-slot__file-action-btn'
            onClick={handlePreview}
            title={tr('chat.slots.preview')}
            aria-label={tr('chat.previewNamedFile', { name })}
            type='button'
          >
            {tr('chat.slots.preview')}
          </button>
          {allowDownload && (
            <a
              href={url}
              download={name}
              className='workflow-slot__file-action-btn'
              aria-label={tr('chat.downloadNamedFile', { name })}
              onClick={(e) => e.stopPropagation()}
            >
              {tr('chat.slots.download')}
            </a>
          )}
          {canEdit && !confirmDelete && (
            <button
              className='workflow-slot__file-action-btn workflow-slot__file-action-btn--danger'
              onClick={handleDeleteClick}
              title={tr('common.delete')}
              aria-label={tr('chat.deleteNamedFile', { name })}
              type='button'
            >
              ×
            </button>
          )}
          {canEdit && confirmDelete && (
            <span className='workflow-slot__delete-confirm'>
              <button className='workflow-slot__delete-confirm-yes' onClick={handleDeleteConfirm} aria-label={tr('chat.slots.confirmDelete')}>{tr('common.delete')}</button>
              <button className='workflow-slot__delete-confirm-no' onClick={handleDeleteCancel} aria-label={tr('chat.slots.cancelDelete')}>{tr('common.cancel')}</button>
            </span>
          )}
        </div>
      </div>
      {canEdit && (
        <div className='workflow-slot__caption'>
          {captionEditing ? (
            <input
              className='workflow-slot__caption-input'
              value={captionDraft}
              onChange={(e) => setCaptionDraft(e.target.value)}
              onBlur={handleCaptionSave}
              onKeyDown={handleCaptionKeyDown}
              autoFocus
              aria-label={tr('chat.slots.editDescription')}
              placeholder={tr('chat.slots.addDescription')}
            />
          ) : (
            <span
              className='workflow-slot__caption-text'
              onClick={handleCaptionEdit}
              title={tr('chat.slots.clickToEditDescription')}
              role='button'
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
            >
              {slot.caption || <span className='workflow-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
            </span>
          )}
        </div>
      )}
      <FilePreviewDrawer
        open={previewOpen}
        filename={name}
        url={rawPath}
        onClose={() => setPreviewOpen(false)}
      />
    </div>
  );
}

/**
 * SlotRenderer dispatches to the correct slot component based on the artifact
 * content_type returned by the backend.
 * When artifact_value is absent (step not yet complete), shows a pending placeholder.
 * expectedType drives the placeholder appearance before the artifact arrives.
 */
export function SlotRenderer({
  slot,
  originalFileSlot,
  cardMode = false,
  expectedType,
  sessionId,
  slotId,
  revisionCount,
  isDraggable,
  onRefresh,
  onReference,
  readOnly,
  hideImageMutationActions,
}: {
  slot: SlotRevision;
  originalFileSlot?: SlotRevision;
  cardMode?: boolean;
  expectedType?: 'image' | 'file' | 'text';
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  isDraggable?: boolean;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  readOnly?: boolean;
  hideImageMutationActions?: boolean;
}) {
  useTranslation();
  if (slot.artifact_value === undefined || slot.artifact_value === null) {
    return <SlotPending type={expectedType ?? 'text'} cardMode={cardMode} />;
  }

  const normalized = normalizeContentType(slot.content_type ?? 'text');
  if (normalized === 'image') {
    return (
      <SlotImage
        slot={slot}
        cardMode={cardMode}
        sessionId={sessionId}
        slotId={slotId}
        revisionCount={revisionCount}
        isDraggable={isDraggable}
        onRefresh={onRefresh}
        onReference={onReference}
        readOnly={readOnly}
        hideMutationActions={hideImageMutationActions}
      />
    );
  }
  if (shouldRenderMarkdownFileAsContent(slot, expectedType)) {
    return (
      <SlotMarkdownFile
        slot={slot}
        originalFileSlot={originalFileSlot}
        sessionId={sessionId}
        slotId={slotId}
        revisionCount={revisionCount}
        onRefresh={onRefresh}
        readOnly={readOnly}
      />
    );
  }
  if (shouldRenderJsonFileAsContent(slot, expectedType)) {
    return (
      <SlotJsonFile
        slot={slot}
        sessionId={sessionId}
        slotId={slotId}
        revisionCount={revisionCount}
        onRefresh={onRefresh}
        readOnly={readOnly}
      />
    );
  }
  if (shouldRenderInlineStructuredContent(slot, expectedType, slotId)) {
    return (
      <SlotInlineStructured
        slot={slot}
        sessionId={sessionId}
        slotId={slotId}
        revisionCount={revisionCount}
        onRefresh={onRefresh}
        readOnly={readOnly}
      />
    );
  }
  if (normalized === 'file') return <SlotFile slot={slot} sessionId={sessionId} slotId={slotId} revisionCount={revisionCount} onRefresh={onRefresh} readOnly={readOnly} />;
  return (
    <SlotText
      slot={slot}
      sessionId={sessionId}
      slotId={slotId}
      revisionCount={revisionCount}
      onRefresh={onRefresh}
      readOnly={readOnly}
    />
  );
}

// --------------------------------------------------------------------------
// AddSlotItemButton — + button and create modal for list slots
// --------------------------------------------------------------------------

interface AddSlotItemButtonProps {
  sessionId: string;
  slotId: string;
  slotType: 'image' | 'file' | 'text';
  onCreated?: () => void;
}

export function AddSlotItemButton({ sessionId, slotId, slotType, onCreated }: AddSlotItemButtonProps) {
  useTranslation();
  const { createSlotItem } = useWorkflowStore();
  const [open, setOpen] = useState(false);
  const [textValue, setTextValue] = useState('');
  const [caption, setCaption] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const isFileBased = slotType === 'image' || slotType === 'file';

  const handleOpen = () => {
    if (isFileBased) {
      // For image/file slots, open the native file picker directly — no modal needed.
      fileInputRef.current?.click();
      return;
    }
    setTextValue('');
    setCaption('');
    setOpen(true);
  };

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setSubmitting(true);
    try {
      const storedPath = await uploadFileInChunks(file);
      await createSlotItem(sessionId, slotId, { path: storedPath }, undefined, undefined, slotType);
      onCreated?.();
    } catch {
      // upload failure — no-op
    } finally {
      setSubmitting(false);
    }
  };

  const handleSubmit = async () => {
    if (!textValue.trim()) return;
    setSubmitting(true);
    try {
      await createSlotItem(sessionId, slotId, { text: textValue }, caption || undefined, undefined, 'text');
      setOpen(false);
      onCreated?.();
    } finally {
      setSubmitting(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') handleSubmit();
    if (e.key === 'Escape') setOpen(false);
  };

  return (
    <>
      {/* Hidden file input for image/file slots */}
      {isFileBased && (
        <input
          ref={fileInputRef}
          type='file'
          accept={slotType === 'image' ? 'image/*' : undefined}
          style={{ display: 'none' }}
          onChange={handleFileChange}
          aria-hidden='true'
        />
      )}
      <button
        className='workflow-slot__add-btn'
        onClick={handleOpen}
        disabled={submitting}
        title={tr('chat.slots.addItem')}
        aria-label={tr('chat.slots.addItem')}
      >
        {submitting ? '…' : '+'}
      </button>
      {open && (
        <div
          className='workflow-slot__modal-overlay'
          role='dialog'
          aria-modal='true'
          aria-label={tr('chat.slots.addItem')}
          onClick={(e) => { if (e.target === e.currentTarget) setOpen(false); }}
        >
          <div className='workflow-slot__modal'>
            <div className='workflow-slot__modal-header'>
              <span>{tr('chat.slots.addItem')}</span>
              <button
                className='workflow-slot__modal-close'
                onClick={() => setOpen(false)}
                aria-label={tr('common.close')}
              >×</button>
            </div>
            <div className='workflow-slot__modal-body' onKeyDown={handleKeyDown}>
              {slotType === 'text' && (
                <textarea
                  className='workflow-slot__modal-textarea'
                  value={textValue}
                  onChange={(e) => setTextValue(e.target.value)}
                  placeholder={tr('chat.slots.enterTextContent')}
                  rows={5}
                  autoFocus
                  aria-label={tr('chat.slots.itemContent')}
                />
              )}
              <input
                className='workflow-slot__modal-caption'
                value={caption}
                onChange={(e) => setCaption(e.target.value)}
                placeholder={tr('chat.slots.optionalDescription')}
                aria-label={tr('common.description')}
              />
            </div>
            <div className='workflow-slot__modal-footer'>
              <button
                className='workflow-slot__modal-submit'
                onClick={handleSubmit}
                disabled={submitting || (slotType === 'text' && !textValue.trim())}
                aria-label={tr('chat.slots.confirmAdd')}
              >
                {submitting ? tr('chat.slots.adding') : tr('common.confirm')}
              </button>
              <button
                className='workflow-slot__modal-cancel'
                onClick={() => setOpen(false)}
                aria-label={tr('common.cancel')}
              >
                {tr('common.cancel')}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
