import { useCallback, useContext, useRef, useState } from 'react';
import ReactDOM from 'react-dom';
import i18n from '@/i18n';
import { resolveCoreAssetUrl } from '@/modules/knowledge/utils/imageUrl';
import type { SlotVersionEntry } from '@/modules/chat/store/pluginPanel';
import { draftStore, usePluginStore } from '@/modules/chat/store/pluginPanel';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import { FilePreviewDrawer } from './FilePreviewDrawer';
import { SnapshotTextDiffView, SnapshotTextPreview } from './slotDiffViews';
import { useGlobalPopoverOpen, type PopoverKey } from './slotPopoverState';
import { formatFileSize, getFileIcon } from './slotUtils';
import { SlotDownloadContext, tr } from './slotShared';

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
      <div className='plugin-slot__version-file-card'>
        <div className='plugin-slot__version-file-card-header'>
          <span className='plugin-slot__file-icon' aria-hidden='true'>
            {getFileIcon(info.name || '')}
          </span>
          <div className='plugin-slot__version-file-card-info'>
            <span className='plugin-slot__version-file-card-name' title={info.name}>
              {info.name || '—'}
            </span>
            <span className='plugin-slot__version-file-card-meta'>
              {label}
              {typeof info.size === 'number' && info.size > 0 ? ` · ${formatFileSize(info.size)}` : ''}
            </span>
          </div>
        </div>
        {info.url && (
          <div className='plugin-slot__version-file-card-actions'>
            <button
              className='plugin-slot__file-action-btn'
              onClick={() => setPreviewOpen(true)}
              type='button'
            >
              {tr('chat.slots.preview')}
            </button>
            {allowDownload && (
              <a
                className='plugin-slot__file-action-btn'
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
      className='plugin-slot__version-overlay'
      onClick={handleOverlayClick}
      role='presentation'
    >
      <div
        className={`plugin-slot__version-popover${isImage ? ' plugin-slot__version-popover--image' : ''}${isFile ? ' plugin-slot__version-popover--file' : ''}`}
        role='dialog'
        aria-label={tr('chat.slots.versionHistory')}
        aria-modal='true'
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className='plugin-slot__version-popover-header'>
          <span className='plugin-slot__version-popover-title'>
            {tr('chat.slots.versionHistory')}
          </span>
          <button
            className='plugin-slot__version-popover-close'
            onClick={handleClose}
            aria-label={tr('chat.slots.closeVersionHistory')}
          >×</button>
        </div>

        {isImage ? (
          /* ── Image mode: top-down layout ── */
          <>
            {currentVersion && (
              <div className='plugin-slot__version-meta-row'>
                <span className='plugin-slot__version-meta-label'>{tr('chat.slots.currentVersionLabel')}</span>
                <span className='plugin-slot__version-meta-badge'>V{currentVersion.revision}</span>
                <span className='plugin-slot__version-meta-time'>
                  {tr('chat.slots.createdAt', { time: formatDate(currentVersion.created_at) })}
                </span>
              </div>
            )}

            <div className='plugin-slot__version-preview-area'>
              {versions.length > 1 && (
                <button
                  className='plugin-slot__version-nav plugin-slot__version-nav--prev'
                  onClick={() => setPreviewIndex((i) => Math.max(0, i - 1))}
                  disabled={previewIndex === 0}
                  aria-label={tr('chat.slots.previousVersion')}
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
                  <span className='plugin-slot__version-preview-empty'>{tr('chat.slots.noImage')}</span>
                ) : null}
              </div>
              {versions.length > 1 && (
                <button
                  className='plugin-slot__version-nav plugin-slot__version-nav--next'
                  onClick={() => setPreviewIndex((i) => Math.min(versions.length - 1, i + 1))}
                  disabled={previewIndex === versions.length - 1}
                  aria-label={tr('chat.slots.nextVersion')}
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
                  aria-label={tr('chat.slots.versionAria', { version: `V${v.revision}` })}
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
                    <span className='plugin-slot__version-thumb-current-tag'>{tr('chat.slots.currentVersion')}</span>
                  )}
                </button>
              ))}
              {/* Upload new version card */}
              <button
                className='plugin-slot__version-thumb plugin-slot__version-thumb--upload'
                onClick={handleVersionUploadClick}
                disabled={uploading}
                aria-label={tr('chat.slots.uploadAndSelect')}
                type='button'
              >
                <span className='plugin-slot__version-thumb-upload-icon'>+</span>
                <span className='plugin-slot__version-thumb-upload-label'>
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

            <div className='plugin-slot__version-footer'>
              <div className='plugin-slot__version-footer-actions'>
                <button className='plugin-slot__version-footer-cancel' onClick={handleClose}>{tr('common.cancel')}</button>
                <button
                  className='plugin-slot__version-footer-apply'
                  disabled={rolling || isPreviewingCurrent || !previewedVersion}
                  onClick={() => previewedVersion && handleRollback(previewedVersion.revision)}
                >
                  {rolling ? tr('chat.slots.rollingBack') : tr('chat.slots.setCurrentVersion')}
                </button>
              </div>
              {previewedVersion && !isPreviewingCurrent && (
                <p className='plugin-slot__version-footer-hint'>
                  {tr('chat.slots.setCurrentVersionHint')}
                </p>
              )}
            </div>
          </>
        ) : isFile ? (
          /* ── File mode: left version list + right file preview ── */
          <div className='plugin-slot__version-popover-body'>
            <ul className='plugin-slot__version-list' role='listbox' aria-label={tr('chat.slots.versionList')}>
              {versions.map((v) => {
                const info = extractFileInfo(v.content_snapshot);
                return (
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
                        {v.change_source === 'human' ? tr('chat.slots.manual') : tr('chat.slots.ai')}
                      </span>
                      v{v.revision}
                      {v.selected && <span className='plugin-slot__version-current-tag'>{tr('chat.slots.current')}</span>}
                    </span>
                    <span className='plugin-slot__version-file-name' title={info.name}>
                      {info.name || '—'}
                    </span>
                    <span className='plugin-slot__version-time'>
                      {new Date(v.created_at).toLocaleString(i18n.language)}
                    </span>
                  </li>
                );
              })}
            </ul>

            {effectiveSelectedVersion && !effectiveSelectedVersion.selected ? (
              <div className='plugin-slot__version-compare plugin-slot__version-compare--file'>
                <FileRevisionPreview
                  info={extractFileInfo(effectiveSelectedVersion.content_snapshot)}
                  label={tr('chat.slots.versionSourceLabel', {
                    version: `v${effectiveSelectedVersion.revision}`,
                    source: effectiveSelectedVersion.change_source === 'human' ? tr('chat.slots.manualEdit') : tr('chat.slots.aiGenerated'),
                  })}
                />
                <button
                  className='plugin-slot__version-apply-btn'
                  disabled={rolling}
                  onClick={() => handleRollback(effectiveSelectedVersion.revision)}
                  aria-label={tr('chat.slots.applyVersionAria', { version: `v${effectiveSelectedVersion.revision}` })}
                >
                  {rolling ? tr('chat.slots.rollingBack') : tr('chat.slots.applyVersion', { version: `v${effectiveSelectedVersion.revision}` })}
                </button>
              </div>
            ) : (
              <div className='plugin-slot__version-compare plugin-slot__version-compare--file'>
                {effectiveSelectedVersion ? (
                  <FileRevisionPreview
                    info={extractFileInfo(activeCurrentValue)}
                    label={tr('chat.slots.currentVersion')}
                  />
                ) : (
                  <div className='plugin-slot__version-compare-hint'>{tr('chat.slots.selectVersionPreview')}</div>
                )}
              </div>
            )}
          </div>
        ) : (
          /* ── Text mode: left list + right diff (unified, with optional draft entry) ── */
          <div className='plugin-slot__version-popover-body'>
            <ul className='plugin-slot__version-list' role='listbox' aria-label={tr('chat.slots.versionList')}>
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
                      {tr('chat.slots.draft')}
                    </span>
                    {tr('chat.slots.draft')}
                  </span>
                  <span className='plugin-slot__version-time'>{tr('chat.slots.notSubmitted')}</span>
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
                      {v.change_source === 'human' ? tr('chat.slots.manual') : tr('chat.slots.ai')}
                    </span>
                    v{v.revision}
                    {v.selected && <span className='plugin-slot__version-current-tag'>{tr('chat.slots.current')}</span>}
                  </span>
                  <span className='plugin-slot__version-time'>
                    {new Date(v.created_at).toLocaleString(i18n.language)}
                  </span>
                </li>
              ))}
            </ul>

            {isDraftSelected && draftText !== undefined ? (
              /* Draft selected: show draft vs current diff with discard + flush actions */
              <div className='plugin-slot__version-compare'>
                <SnapshotTextDiffView
                  currentSnapshot={activeCurrentValue}
                  otherText={draftText}
                  otherLabel={tr('chat.slots.draft')}
                  reversed={true}
                />
                <div className='plugin-slot__version-draft-actions'>
                  <button
                    className='plugin-slot__version-discard-btn'
                    onClick={() => { onDiscardDraft?.(); handleClose(); }}
                    aria-label={tr('chat.slots.discardDraft')}
                  >
                    {tr('chat.slots.discardDraft')}
                  </button>
                  <button
                    className='plugin-slot__version-flush-btn'
                    disabled={flushing}
                    onClick={handleFlushDraft}
                    aria-label={tr('chat.slots.confirmChanges')}
                  >
                    {flushing ? tr('chat.slots.submitting') : tr('chat.slots.confirmChanges')}
                  </button>
                </div>
              </div>
            ) : effectiveSelectedVersion && !effectiveSelectedVersion.selected ? (
              <div className='plugin-slot__version-compare'>
                <SnapshotTextDiffView
                  currentSnapshot={activeCurrentValue}
                  otherSnapshot={effectiveSelectedVersion.content_snapshot}
                  otherLabel={tr('chat.slots.versionSourceLabel', {
                    version: `v${effectiveSelectedVersion.revision}`,
                    source: effectiveSelectedVersion.change_source === 'human' ? tr('chat.slots.manualEdit') : tr('chat.slots.aiGenerated'),
                  })}
                  reversed={currentVersion !== null && effectiveSelectedVersion.revision > currentVersion.revision}
                />
                <button
                  className='plugin-slot__version-apply-btn'
                  disabled={rolling}
                  onClick={() => handleRollback(effectiveSelectedVersion.revision)}
                  aria-label={tr('chat.slots.applyVersionAria', { version: `v${effectiveSelectedVersion.revision}` })}
                >
                  {rolling ? tr('chat.slots.rollingBack') : tr('chat.slots.applyVersion', { version: `v${effectiveSelectedVersion.revision}` })}
                </button>
              </div>
            ) : (
              <div className='plugin-slot__version-compare plugin-slot__version-compare--same'>
                {effectiveSelectedVersion ? (
                  <SnapshotTextPreview snapshot={activeCurrentValue} />
                ) : (
                  <div className='plugin-slot__version-compare-hint'>{tr('chat.slots.selectVersionCompare')}</div>
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
        title={draftText !== undefined ? tr('chat.slots.draftCompareHint') : tr('chat.slots.versionHistoryCount', { count: revisionCount })}
        aria-label={draftText !== undefined ? tr('chat.slots.draft') : tr('chat.slots.versionHistoryCount', { count: revisionCount })}
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
