import { useState, useCallback, useRef, useEffect, useMemo, createContext, useContext } from "react";
import ReactDOM from "react-dom";
import type { SlotRevision, SlotVersionEntry } from "@/modules/chat/store/pluginPanel";
import { usePluginStore, draftStore } from "@/modules/chat/store/pluginPanel";
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync } from "@/modules/knowledge/utils/imageUrl";
import { buildDiffLinesWithInline } from "@/modules/memory/shared";
import { DiffLineContent } from "@/modules/memory/components/DiffLineContent";
import { uploadFileInChunks } from "@/modules/chat/utils/chunkUpload";
import { FilePreviewDrawer } from "./FilePreviewDrawer";

/**
 * Context for notifying the parent PluginPanel when any text slot enters/exits editing mode.
 * The parent uses this to disable the Retry / Continue footer buttons.
 */
interface SlotEditingContextValue {
  setEditing: (key: string, editing: boolean) => void;
}
export const SlotEditingContext = createContext<SlotEditingContextValue>({
  setEditing: () => {},
});

/**
 * Normalize the content_type returned by the Python backend.
 * Python stores short forms: 'text', 'json', 'image', 'file', 'file_list'.
 */
function normalizeContentType(ct: string): 'image' | 'file' | 'text' {
  if (ct === 'image' || ct.startsWith('image/')) return 'image';
  if (ct === 'file' || ct === 'file_list' || ct.startsWith('application/')) return 'file';
  return 'text';
}

/** Shown when the slot has no artifact yet (backend returned no artifact_value). */
function SlotPending({ type, cardMode }: { type: 'image' | 'file' | 'text'; cardMode?: boolean }) {
  if (type === 'image') {
    return (
      <div className={`plugin-slot plugin-slot--image plugin-slot--pending${cardMode ? ' plugin-slot--image-card' : ''}`}>
        <span className='plugin-slot__placeholder-icon' aria-hidden='true'>🖼</span>
        <span className='plugin-slot__placeholder'>进行中…</span>
      </div>
    );
  }
  if (type === 'file') {
    return (
      <div className='plugin-slot plugin-slot--file plugin-slot--pending'>
        <span className='plugin-slot__placeholder'>待生成…</span>
      </div>
    );
  }
  return (
    <div className='plugin-slot plugin-slot--text plugin-slot--pending'>
      <p className='plugin-slot__text plugin-slot__text--pending'>待计算…</p>
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
    <div className='plugin-slot__version-diff'>
      <div className='plugin-slot__version-diff-header'>
        {reversed ? (
          <>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--remove'>
              当前版本
            </span>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--add'>
              {otherLabel}
            </span>
          </>
        ) : (
          <>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--remove'>
              {otherLabel}
            </span>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--add'>
              当前版本
            </span>
          </>
        )}
      </div>
      <div className='plugin-slot__version-diff-body'>
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
          <div className='plugin-slot__version-diff-empty'>内容完全相同</div>
        )}
      </div>
    </div>
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
  currentChangeSource?: 'ai' | 'human';
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
  const { getSlotVersions, rollbackSlotItem, patchSlotItemValue } = usePluginStore();

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

  const previewedVersion = versions[previewIndex] ?? null;
  // The currently-selected (active) version
  const currentVersion = versions.find((v) => v.selected) ?? versions[0] ?? null;
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
      className='plugin-slot__version-overlay'
      onClick={handleOverlayClick}
      role='presentation'
    >
      <div
        className={`plugin-slot__version-popover${isImage ? ' plugin-slot__version-popover--image' : ''}`}
        role='dialog'
        aria-label='版本历史'
        aria-modal='true'
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className='plugin-slot__version-popover-header'>
          <span className='plugin-slot__version-popover-title'>
            版本历史
          </span>
          <button
            className='plugin-slot__version-popover-close'
            onClick={handleClose}
            aria-label='关闭版本历史'
          >×</button>
        </div>

        {isImage ? (
          /* ── Image mode: top-down layout ── */
          <>
            {currentVersion && (
              <div className='plugin-slot__version-meta-row'>
                <span className='plugin-slot__version-meta-label'>当前版本：</span>
                <span className='plugin-slot__version-meta-badge'>V{currentVersion.revision}</span>
                <span className='plugin-slot__version-meta-time'>
                  创建时间：{formatDate(currentVersion.created_at)}
                </span>
              </div>
            )}

            <div className='plugin-slot__version-preview-area'>
              {versions.length > 1 && (
                <button
                  className='plugin-slot__version-nav plugin-slot__version-nav--prev'
                  onClick={() => setPreviewIndex((i) => Math.max(0, i - 1))}
                  disabled={previewIndex === 0}
                  aria-label='上一版本'
                >‹</button>
              )}
              <div className='plugin-slot__version-preview-img-wrap'>
                {previewedVersion && extractText(previewedVersion.content_snapshot) ? (
                  <img
                    key={previewedVersion.revision}
                    className='plugin-slot__version-preview-img'
                    src={extractText(previewedVersion.content_snapshot)}
                    alt=''
                  />
                ) : previewedVersion ? (
                  <span className='plugin-slot__version-preview-empty'>暂无图片</span>
                ) : null}
              </div>
              {versions.length > 1 && (
                <button
                  className='plugin-slot__version-nav plugin-slot__version-nav--next'
                  onClick={() => setPreviewIndex((i) => Math.min(versions.length - 1, i + 1))}
                  disabled={previewIndex === versions.length - 1}
                  aria-label='下一版本'
                >›</button>
              )}
            </div>

            <div className='plugin-slot__version-strip'>
              {versions.map((v, idx) => (
                <button
                  key={v.revision}
                  className={[
                    'plugin-slot__version-thumb',
                    idx === previewIndex ? 'plugin-slot__version-thumb--active' : '',
                    v.selected ? 'plugin-slot__version-thumb--current' : '',
                  ].join(' ')}
                  onClick={() => setPreviewIndex(idx)}
                  aria-label={`版本 V${v.revision}`}
                >
                  <div className='plugin-slot__version-thumb-img-wrap'>
                    {extractText(v.content_snapshot) ? (
                      <img
                        className='plugin-slot__version-thumb-img'
                        src={extractText(v.content_snapshot)}
                        alt=''
                      />
                    ) : (
                      <span className='plugin-slot__version-thumb-empty'>—</span>
                    )}
                    <span className='plugin-slot__version-thumb-badge'>V{v.revision}</span>
                  </div>
                  {v.selected && (
                    <span className='plugin-slot__version-thumb-current-tag'>当前版本</span>
                  )}
                </button>
              ))}
              {/* Upload new version card */}
              <button
                className='plugin-slot__version-thumb plugin-slot__version-thumb--upload'
                onClick={handleVersionUploadClick}
                disabled={uploading}
                aria-label='上传并选择'
                type='button'
              >
                <span className='plugin-slot__version-thumb-upload-icon'>+</span>
                <span className='plugin-slot__version-thumb-upload-label'>
                  {uploading ? '上传中…' : '上传并选择'}
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

            <div className='plugin-slot__version-footer'>
              <div className='plugin-slot__version-footer-actions'>
                <button className='plugin-slot__version-footer-cancel' onClick={handleClose}>取消</button>
                <button
                  className='plugin-slot__version-footer-apply'
                  disabled={rolling || isPreviewingCurrent || !previewedVersion}
                  onClick={() => previewedVersion && handleRollback(previewedVersion.revision)}
                >
                  {rolling ? '回退中…' : '设为当前版本'}
                </button>
              </div>
              {previewedVersion && !isPreviewingCurrent && (
                <p className='plugin-slot__version-footer-hint'>
                  设为当前版本后，内容将更新为该版本，其他版本不受影响
                </p>
              )}
            </div>
          </>
        ) : (
          /* ── Text mode: left list + right diff (unified, with optional draft entry) ── */
          <div className='plugin-slot__version-popover-body'>
            <ul className='plugin-slot__version-list' role='listbox' aria-label='版本列表'>
              {/* Draft entry — only shown when there is a pending local draft */}
              {draftText !== undefined && (
                <li
                  role='option'
                  aria-selected={isDraftSelected}
                  className={[
                    'plugin-slot__version-item',
                    'plugin-slot__version-item--draft',
                    isDraftSelected ? 'plugin-slot__version-item--focused' : '',
                  ].join(' ')}
                  onClick={() => setSelectedRevision(DRAFT_REVISION)}
                >
                  <span className='plugin-slot__version-label'>
                    <span className='plugin-slot__version-source-badge plugin-slot__version-source-badge--human'>
                      草稿
                    </span>
                    草稿
                  </span>
                  <span className='plugin-slot__version-time'>未提交</span>
                </li>
              )}
              {versions.map((v) => (
                <li
                  key={v.revision}
                  role='option'
                  aria-selected={!isDraftSelected && effectiveSelectedVersion?.revision === v.revision}
                  className={[
                    'plugin-slot__version-item',
                    v.selected ? 'plugin-slot__version-item--current' : '',
                    !isDraftSelected && effectiveSelectedVersion?.revision === v.revision ? 'plugin-slot__version-item--focused' : '',
                  ].join(' ')}
                  onClick={() => setSelectedRevision(v.revision)}
                >
                  <span className='plugin-slot__version-label'>
                    <span className={`plugin-slot__version-source-badge plugin-slot__version-source-badge--${v.change_source}`}>
                      {v.change_source === 'human' ? '手动' : 'AI'}
                    </span>
                    v{v.revision}
                    {v.selected && <span className='plugin-slot__version-current-tag'>当前</span>}
                  </span>
                  <span className='plugin-slot__version-time'>
                    {new Date(v.created_at).toLocaleString()}
                  </span>
                </li>
              ))}
            </ul>

            {isDraftSelected && draftText !== undefined ? (
              /* Draft selected: show draft vs current diff with discard + flush actions */
              <div className='plugin-slot__version-compare'>
                <TextDiffView
                  currentText={extractText(currentValue)}
                  otherText={draftText}
                  otherLabel='草稿'
                  reversed={true}
                />
                <div className='plugin-slot__version-draft-actions'>
                  <button
                    className='plugin-slot__version-discard-btn'
                    onClick={() => { onDiscardDraft?.(); handleClose(); }}
                    aria-label='丢弃草稿'
                  >
                    丢弃草稿
                  </button>
                  <button
                    className='plugin-slot__version-flush-btn'
                    disabled={flushing}
                    onClick={handleFlushDraft}
                    aria-label='确定变更'
                  >
                    {flushing ? '提交中…' : '确定变更'}
                  </button>
                </div>
              </div>
            ) : effectiveSelectedVersion && !effectiveSelectedVersion.selected ? (
              <div className='plugin-slot__version-compare'>
                <TextDiffView
                  currentText={extractText(currentValue)}
                  otherText={extractText(effectiveSelectedVersion.content_snapshot)}
                  otherLabel={`v${effectiveSelectedVersion.revision} · ${effectiveSelectedVersion.change_source === 'human' ? '手动编辑' : 'AI 生成'}`}
                  reversed={currentVersion !== null && effectiveSelectedVersion.revision > currentVersion.revision}
                />
                <button
                  className='plugin-slot__version-apply-btn'
                  disabled={rolling}
                  onClick={() => handleRollback(effectiveSelectedVersion.revision)}
                  aria-label={`应用 v${effectiveSelectedVersion.revision}`}
                >
                  {rolling ? '回退中…' : `应用此版本 (v${effectiveSelectedVersion.revision})`}
                </button>
              </div>
            ) : (
              <div className='plugin-slot__version-compare plugin-slot__version-compare--same'>
                {effectiveSelectedVersion ? (
                  <pre className='plugin-slot__version-current-text'>
                    {extractText(currentValue) || '（无内容）'}
                  </pre>
                ) : (
                  <div className='plugin-slot__version-compare-hint'>选择版本查看对比</div>
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
    <div className='plugin-slot__version-wrap'>
      <button
        className={`plugin-slot__version-btn${draftText !== undefined ? ' plugin-slot__version-btn--draft' : ''}`}
        onClick={handleOpen}
        title={draftText !== undefined ? '草稿（点击查看与当前版本的对比）' : `版本历史 (${revisionCount})`}
        aria-label={draftText !== undefined ? '草稿' : `版本历史 (${revisionCount})`}
        disabled={loading}
      >
        <span className='plugin-slot__version-count'>
          {draftText !== undefined ? 'draft' : (currentRevision !== undefined ? `v${currentRevision}` : revisionCount > 1 ? `v${revisionCount}` : 'v1')}
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
}: SlotImageProps) {
  const raw = slot.artifact_value;
  const rawPath: string = raw?.url ?? raw?.path ?? '';
  const syncUrl: string = rawPath ? resolveCoreAssetUrl(rawPath) : '';
  // For absolute upload paths that resolveCoreAssetUrl cannot serve directly,
  // asynchronously sign the path and replace the URL.
  const [resolvedUrl, setResolvedUrl] = useState<string>(syncUrl);
  useEffect(() => {
    setResolvedUrl(syncUrl);
    // syncUrl === rawPath means resolveCoreAssetUrl returned it unchanged (not http/static-files),
    // so it's an absolute filesystem path that needs signing via the core API.
    if (rawPath && syncUrl === rawPath && !rawPath.startsWith('http')) {
      resolveMarkdownImageUrlAsync(rawPath).then(setResolvedUrl).catch(() => {});
    }
  }, [rawPath, syncUrl]);
  const url = resolvedUrl;
  const alt: string = slot.caption ?? raw?.alt ?? '';
  const { deleteSlotItem, patchSlotCaption, patchSlotItemValue } = usePluginStore();
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

  if (!url) return <SlotPending type='image' cardMode={cardMode} />;

  const hasActions = Boolean(sessionId && slotId && slot.list_index !== undefined);

  // Overlays rendered directly on top of the image (no separate action bar)
  const overlays = hasActions ? (
    <>
      {/* Delete + Upload buttons — top-right, shown on hover via CSS */}
      {confirmDelete ? (
        <span className='plugin-slot__delete-confirm plugin-slot__delete-confirm--overlay'>
          <span className='plugin-slot__delete-confirm-text'>确认删除？</span>
          <button
            className='plugin-slot__delete-confirm-yes'
            onClick={handleDeleteConfirm}
            aria-label='确认删除'
          >删除</button>
          <button
            className='plugin-slot__delete-confirm-no'
            onClick={handleDeleteCancel}
            aria-label='取消删除'
          >取消</button>
        </span>
      ) : (
        <span className='plugin-slot__top-right-actions'>
          <button
            className='plugin-slot__upload-overlay-btn'
            onClick={handleUploadClick}
            disabled={uploading}
            title='上传并选择'
            aria-label='上传并选择'
          >
            {uploading ? '…' : '+'}
          </button>
          <button
            className='plugin-slot__delete-btn plugin-slot__delete-btn--overlay'
            onClick={handleDeleteClick}
            title='删除'
            aria-label='删除图片'
          >×</button>
        </span>
      )}

      {/* Version badge — bottom-left, always visible, overlaid on image */}
      {revisionCount !== undefined && revisionCount > 0 && (
        <div className='plugin-slot__version-overlay-badge'>
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
          className='plugin-slot__ref-btn plugin-slot__ref-btn--overlay'
          onClick={handleReference}
          title='引用此图片'
          aria-label='引用此图片'
        >📎</button>
      )}

      {/* Drag handle — bottom-left edge, shown on hover */}
      {isDraggable && (
        <span className='plugin-slot__drag-handle plugin-slot__drag-handle--overlay' title='拖拽排序' aria-hidden='true'>⠿</span>
      )}
    </>
  ) : null;

  if (cardMode) {
    return (
      <div className='plugin-slot plugin-slot--image-card-wrap'>
        <div className='plugin-slot plugin-slot--image-card'>
          <img src={url} alt={alt} className='plugin-slot__image-card-img' loading='lazy' />
          {alt && <div className='plugin-slot__image-card-caption'>{alt}</div>}
          {overlays}
        </div>
        {/* Hidden file input */}
        {hasActions && (
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
          <div className='plugin-slot__caption'>
            {captionEditing ? (
              <input
                className='plugin-slot__caption-input'
                value={captionDraft}
                onChange={(e) => setCaptionDraft(e.target.value)}
                onBlur={handleCaptionSave}
                onKeyDown={handleCaptionKeyDown}
                autoFocus
                aria-label='编辑描述'
                placeholder='添加描述…'
              />
            ) : (
              <span
                className='plugin-slot__caption-text'
                onClick={handleCaptionEdit}
                title='点击编辑描述'
                role='button'
                tabIndex={0}
                onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
              >
                {slot.caption || <span className='plugin-slot__caption-placeholder'>添加描述…</span>}
              </span>
            )}
          </div>
        )}
      </div>
    );
  }
  return (
    <div className='plugin-slot plugin-slot--image'>
      <img src={url} alt={alt} className='plugin-slot__image' loading='lazy' />
      {overlays}
      {hasActions && (
        <div className='plugin-slot__caption'>
          {captionEditing ? (
            <input
              className='plugin-slot__caption-input'
              value={captionDraft}
              onChange={(e) => setCaptionDraft(e.target.value)}
              onBlur={handleCaptionSave}
              onKeyDown={handleCaptionKeyDown}
              autoFocus
              aria-label='编辑描述'
              placeholder='添加描述…'
            />
          ) : (
            <span
              className='plugin-slot__caption-text'
              onClick={handleCaptionEdit}
              title='点击编辑描述'
              role='button'
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
            >
              {slot.caption || <span className='plugin-slot__caption-placeholder'>添加描述…</span>}
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
}

export function SlotText({ slot, sessionId, slotId, revisionCount, onRefresh }: SlotTextProps) {
  const raw = slot.artifact_value;
  const { patchSlotCaption } = usePluginStore();
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

  // Detect large-content offload: {"type":"text"|"json","path":"...","size":N}
  const isOffloaded = raw && typeof raw === 'object' && raw.path && (raw.type === 'text' || raw.type === 'json');

  // Fetch offloaded file content on mount (or when path changes).
  useEffect(() => {
    if (!isOffloaded) return;
    setOffloadLoading(true);
    fetch(resolveCoreAssetUrl(raw.path))
      .then((r) => r.text())
      .then((t) => setOffloadedText(t))
      .catch(() => setOffloadedText('[无法加载文件内容]'))
      .finally(() => setOffloadLoading(false));
  }, [isOffloaded, raw?.path]);

  let text: string;
  if (isOffloaded) {
    if (offloadLoading) return <SlotPending type='text' />;
    text = offloadedText ?? '';
  } else if (raw?.text !== undefined) {
    text = String(raw.text);
  } else if (raw?.data !== undefined) {
    text = typeof raw.data === 'string' ? raw.data : JSON.stringify(raw.data, null, 2);
  } else if (raw !== undefined && raw !== null) {
    text = JSON.stringify(raw);
  } else {
    return <SlotPending type='text' />;
  }

  const canEdit = Boolean(sessionId && slotId);
  // For single slots, list_index is undefined from the backend; use 0 as the canonical index
  // for localStorage keys (front-end only convention).
  const effectiveListIndex = slot.list_index ?? 0;
  // For API calls, single slots must use -1 so the backend queries list_index IS NULL.
  const apiListIndex = slot.list_index ?? -1;

  // On mount: restore localStorage draft only if it differs from the current artifact text.
  // Also restart the 60s flush timer so the draft doesn't stay in localStorage forever.
  useEffect(() => {
    if (!canEdit || !sessionId || !slotId) return;
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

  return (
    <div className='plugin-slot plugin-slot--text'>
      {editing ? (
        <textarea
          className='plugin-slot__text-editor'
          value={draft}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          onBlur={handleSave}
          autoFocus
          rows={6}
          aria-label='编辑文本'
        />
      ) : (
        <>
          <p
            className={`plugin-slot__text${canEdit ? ' plugin-slot__text--editable' : ''}`}
            onClick={canEdit ? handleEdit : undefined}
            title={canEdit ? '点击编辑' : undefined}
            role={canEdit ? 'button' : undefined}
            tabIndex={canEdit ? 0 : undefined}
            onKeyDown={canEdit ? (e) => e.key === 'Enter' && handleEdit() : undefined}
          >{displayText}</p>
          <div className='plugin-slot__text-meta'>
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
            <div className='plugin-slot__caption'>
              {captionEditing ? (
                <input
                  className='plugin-slot__caption-input'
                  value={captionDraft}
                  onChange={(e) => setCaptionDraft(e.target.value)}
                  onBlur={handleCaptionSave}
                  onKeyDown={handleCaptionKeyDown}
                  autoFocus
                  aria-label='编辑描述'
                  placeholder='添加描述…'
                />
              ) : (
                <span
                  className='plugin-slot__caption-text'
                  onClick={handleCaptionEdit}
                  title='点击编辑描述'
                  role='button'
                  tabIndex={0}
                  onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
                >
                  {slot.caption || <span className='plugin-slot__caption-placeholder'>添加描述…</span>}
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
  if (ext === 'json' || ext === 'csv') return '📋';
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
  onRefresh?: () => void;
}

export function SlotFile({ slot, sessionId, slotId, onRefresh }: SlotFileProps) {
  const raw = slot.artifact_value;
  const rawPath: string = raw?.url ?? raw?.path ?? '';
  const url: string = rawPath ? resolveCoreAssetUrl(rawPath) : '';
  const name: string = raw?.filename ?? raw?.name ?? slot.artifact_key;
  const size: number | undefined = raw?.size;
  const { deleteSlotItem, patchSlotCaption } = usePluginStore();
  const [previewOpen, setPreviewOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [captionEditing, setCaptionEditing] = useState(false);
  const [captionDraft, setCaptionDraft] = useState('');

  const canEdit = Boolean(sessionId && slotId && slot.list_index !== undefined);

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

  return (
    <div className='plugin-slot plugin-slot--file plugin-slot--file-enhanced'>
      <div className='plugin-slot__file-card'>
        <div className='plugin-slot__file-card-header'>
          <span className='plugin-slot__file-icon' aria-hidden='true'>{getFileIcon(name)}</span>
          <div className='plugin-slot__file-card-info'>
            <span className='plugin-slot__file-name' title={name}>{name}</span>
            {size !== undefined && (
              <span className='plugin-slot__file-size'>{formatFileSize(size)}</span>
            )}
          </div>
        </div>
        <div className='plugin-slot__file-card-actions'>
          <button
            className='plugin-slot__file-action-btn'
            onClick={handlePreview}
            title='预览'
            aria-label={`预览 ${name}`}
            type='button'
          >
            预览
          </button>
          <a
            href={url}
            download={name}
            className='plugin-slot__file-action-btn'
            aria-label={`下载 ${name}`}
            onClick={(e) => e.stopPropagation()}
          >
            下载
          </a>
          {canEdit && !confirmDelete && (
            <button
              className='plugin-slot__file-action-btn plugin-slot__file-action-btn--danger'
              onClick={handleDeleteClick}
              title='删除'
              aria-label={`删除 ${name}`}
              type='button'
            >
              ×
            </button>
          )}
          {canEdit && confirmDelete && (
            <span className='plugin-slot__delete-confirm'>
              <button className='plugin-slot__delete-confirm-yes' onClick={handleDeleteConfirm} aria-label='确认删除'>删除</button>
              <button className='plugin-slot__delete-confirm-no' onClick={handleDeleteCancel} aria-label='取消删除'>取消</button>
            </span>
          )}
        </div>
      </div>
      {canEdit && (
        <div className='plugin-slot__caption'>
          {captionEditing ? (
            <input
              className='plugin-slot__caption-input'
              value={captionDraft}
              onChange={(e) => setCaptionDraft(e.target.value)}
              onBlur={handleCaptionSave}
              onKeyDown={handleCaptionKeyDown}
              autoFocus
              aria-label='编辑描述'
              placeholder='添加描述…'
            />
          ) : (
            <span
              className='plugin-slot__caption-text'
              onClick={handleCaptionEdit}
              title='点击编辑描述'
              role='button'
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
            >
              {slot.caption || <span className='plugin-slot__caption-placeholder'>添加描述…</span>}
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
  cardMode = false,
  expectedType,
  sessionId,
  slotId,
  revisionCount,
  isDraggable,
  onRefresh,
  onReference,
}: {
  slot: SlotRevision;
  cardMode?: boolean;
  expectedType?: 'image' | 'file' | 'text';
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  isDraggable?: boolean;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
}) {
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
      />
    );
  }
  if (normalized === 'file') return <SlotFile slot={slot} sessionId={sessionId} slotId={slotId} onRefresh={onRefresh} />;
  return (
    <SlotText
      slot={slot}
      sessionId={sessionId}
      slotId={slotId}
      revisionCount={revisionCount}
      onRefresh={onRefresh}
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
  const { createSlotItem } = usePluginStore();
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
        className='plugin-slot__add-btn'
        onClick={handleOpen}
        disabled={submitting}
        title='添加条目'
        aria-label='添加条目'
      >
        {submitting ? '…' : '+'}
      </button>
      {open && (
        <div
          className='plugin-slot__modal-overlay'
          role='dialog'
          aria-modal='true'
          aria-label='添加条目'
          onClick={(e) => { if (e.target === e.currentTarget) setOpen(false); }}
        >
          <div className='plugin-slot__modal'>
            <div className='plugin-slot__modal-header'>
              <span>添加条目</span>
              <button
                className='plugin-slot__modal-close'
                onClick={() => setOpen(false)}
                aria-label='关闭'
              >×</button>
            </div>
            <div className='plugin-slot__modal-body' onKeyDown={handleKeyDown}>
              {slotType === 'text' && (
                <textarea
                  className='plugin-slot__modal-textarea'
                  value={textValue}
                  onChange={(e) => setTextValue(e.target.value)}
                  placeholder='输入文本内容…'
                  rows={5}
                  autoFocus
                  aria-label='条目内容'
                />
              )}
              <input
                className='plugin-slot__modal-caption'
                value={caption}
                onChange={(e) => setCaption(e.target.value)}
                placeholder='描述（可选）…'
                aria-label='描述'
              />
            </div>
            <div className='plugin-slot__modal-footer'>
              <button
                className='plugin-slot__modal-submit'
                onClick={handleSubmit}
                disabled={submitting || (slotType === 'text' && !textValue.trim())}
                aria-label='确认添加'
              >
                {submitting ? '添加中…' : '确认'}
              </button>
              <button
                className='plugin-slot__modal-cancel'
                onClick={() => setOpen(false)}
                aria-label='取消'
              >
                取消
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
