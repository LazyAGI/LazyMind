import { useState, useCallback, useRef, useEffect, useContext } from "react";
import { Image as AntImage } from 'antd';
import { EyeOutlined } from '@ant-design/icons';
import type { SlotRevision } from "@/modules/chat/store/pluginPanel";
import { usePluginStore, draftStore } from "@/modules/chat/store/pluginPanel";
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from "@/modules/knowledge/utils/imageUrl";
import { uploadFileInChunks } from "@/modules/chat/utils/chunkUpload";
import { FilePreviewDrawer } from "./FilePreviewDrawer";
import { SlotEditingContext } from './slotEditingContext';
import { useSlotImageUrl } from './slotResources';
import {
  shouldRenderInlineStructuredContent,
  shouldRenderJsonFileAsContent,
  shouldRenderMarkdownFileAsContent,
} from './slotArtifactPayload';
import {
  SlotInlineStructured,
  SlotJsonFile,
  SlotMarkdownFile,
} from './SlotStructuredFiles';
import {
  formatFileSize,
  getFileIcon,
  isSpaFallbackHtml,
  normalizeContentType,
} from './slotUtils';
import { useTranslation } from 'react-i18next';
import { localizeErrorCode } from '@/components/request';

export { SlotEditingContext } from './slotEditingContext';
export type { SlotEditingContextValue } from './slotEditingContext';

import { SlotDownloadContext, SlotPending, tr } from './slotShared';
import { SlotVersionPopover } from './SlotVersionPopover';

export { SlotDownloadContext } from './slotShared';
export { SlotVersionPopover } from './SlotVersionPopover';


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

  const handlePreviewClick = useCallback((e: React.MouseEvent) => {
    // Prevent parent list item focus/sort handlers from firing when opening preview.
    e.stopPropagation();
  }, []);

  const [previewVisible, setPreviewVisible] = useState(false);

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
        <span className='plugin-slot__delete-confirm plugin-slot__delete-confirm--overlay'>
          <span className='plugin-slot__delete-confirm-text'>{tr('chat.slots.confirmDeleteQuestion')}</span>
          <button
            className='plugin-slot__delete-confirm-yes'
            onClick={handleDeleteConfirm}
            aria-label={tr('chat.slots.confirmDelete')}
          >{tr('common.delete')}</button>
          <button
            className='plugin-slot__delete-confirm-no'
            onClick={handleDeleteCancel}
            aria-label={tr('chat.slots.cancelDelete')}
          >{tr('common.cancel')}</button>
        </span>
      ) : (
        <span className='plugin-slot__top-right-actions'>
          <button
            className='plugin-slot__upload-overlay-btn'
            onClick={handleUploadClick}
            disabled={uploading}
            title={tr('chat.slots.uploadAndSelect')}
            aria-label={tr('chat.slots.uploadAndSelect')}
          >
            {uploading ? '…' : '+'}
          </button>
          <button
            className='plugin-slot__delete-btn plugin-slot__delete-btn--overlay'
            onClick={handleDeleteClick}
            title={tr('common.delete')}
            aria-label={tr('chat.slots.deleteImage')}
          >×</button>
        </span>
      ))}

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
          title={tr('chat.slots.referenceImage')}
          aria-label={tr('chat.slots.referenceImage')}
        >📎</button>
      )}

      {/* Drag handle — bottom-left edge, shown on hover */}
      {isDraggable && (
        <span className='plugin-slot__drag-handle plugin-slot__drag-handle--overlay' title={tr('chat.slots.dragToSort')} aria-hidden='true'>⠿</span>
      )}
    </>
  ) : null;

  const previewableImage = (
    <AntImage
      src={url}
      alt={alt}
      className={cardMode ? 'plugin-slot__image-card-img' : 'plugin-slot__image'}
      rootClassName={cardMode ? 'plugin-slot__image-antd plugin-slot__image-antd--card' : 'plugin-slot__image-antd'}
      loading='lazy'
      preview={{
        mask: (
          <span className='plugin-slot__image-preview-mask'>
            <EyeOutlined className='plugin-slot__image-preview-mask-icon' />
            <span>{tr('chat.slots.preview')}</span>
          </span>
        ),
        visible: previewVisible,
        onVisibleChange: setPreviewVisible,
      }}
      role='button'
      tabIndex={0}
      aria-label={alt || tr('chat.previewImage')}
      onClick={handlePreviewClick}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          event.stopPropagation();
          setPreviewVisible(true);
        }
      }}
    />
  );

  if (cardMode) {
    return (
      <div className='plugin-slot plugin-slot--image-card-wrap'>
        <div className='plugin-slot plugin-slot--image-card'>
          {previewableImage}
          {alt && <div className='plugin-slot__image-card-caption'>{alt}</div>}
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
          <div className='plugin-slot__caption'>
            {captionEditing ? (
              <input
                className='plugin-slot__caption-input'
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
                className='plugin-slot__caption-text'
                onClick={handleCaptionEdit}
                title={tr('chat.slots.clickToEditDescription')}
                role='button'
                tabIndex={0}
                onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
              >
                {slot.caption || <span className='plugin-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
              </span>
            )}
          </div>
        )}
      </div>
    );
  }
  return (
    <div className='plugin-slot plugin-slot--image'>
      {previewableImage}
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
              aria-label={tr('chat.slots.editDescription')}
              placeholder={tr('chat.slots.addDescription')}
            />
          ) : (
            <span
              className='plugin-slot__caption-text'
              onClick={handleCaptionEdit}
              title={tr('chat.slots.clickToEditDescription')}
              role='button'
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
            >
              {slot.caption || <span className='plugin-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
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
          aria-label={tr('chat.slots.editText')}
        />
      ) : (
        <>
          <p
            className={`plugin-slot__text${canEdit ? ' plugin-slot__text--editable' : ''}`}
            onClick={canEdit ? handleEdit : undefined}
            title={canEdit ? tr('chat.slots.clickToEdit') : undefined}
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
                  aria-label={tr('chat.slots.editDescription')}
                  placeholder={tr('chat.slots.addDescription')}
                />
              ) : (
                <span
                  className='plugin-slot__caption-text'
                  onClick={handleCaptionEdit}
                  title={tr('chat.slots.clickToEditDescription')}
                  role='button'
                  tabIndex={0}
                  onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
                >
                  {slot.caption || <span className='plugin-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
                </span>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
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



export function SlotFile({ slot, sessionId, slotId, revisionCount, onRefresh, readOnly }: SlotFileProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const raw = slot.artifact_value;
  const rawPath: string = raw?.url ?? raw?.path ?? '';
  const url: string = rawPath ? resolveCoreAssetUrl(rawPath) : '';
  const name: string = raw?.filename ?? raw?.name ?? slot.slot;
  const size: number | undefined = raw?.size;
  const { deleteSlotItem, patchSlotCaption } = usePluginStore();
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
        <div className='plugin-slot__file-card-actions'>
          <button
            className='plugin-slot__file-action-btn'
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
              className='plugin-slot__file-action-btn'
              aria-label={tr('chat.downloadNamedFile', { name })}
              onClick={(e) => e.stopPropagation()}
            >
              {tr('chat.slots.download')}
            </a>
          )}
          {canEdit && !confirmDelete && (
            <button
              className='plugin-slot__file-action-btn plugin-slot__file-action-btn--danger'
              onClick={handleDeleteClick}
              title={tr('common.delete')}
              aria-label={tr('chat.deleteNamedFile', { name })}
              type='button'
            >
              ×
            </button>
          )}
          {canEdit && confirmDelete && (
            <span className='plugin-slot__delete-confirm'>
              <button className='plugin-slot__delete-confirm-yes' onClick={handleDeleteConfirm} aria-label={tr('chat.slots.confirmDelete')}>{tr('common.delete')}</button>
              <button className='plugin-slot__delete-confirm-no' onClick={handleDeleteCancel} aria-label={tr('chat.slots.cancelDelete')}>{tr('common.cancel')}</button>
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
              aria-label={tr('chat.slots.editDescription')}
              placeholder={tr('chat.slots.addDescription')}
            />
          ) : (
            <span
              className='plugin-slot__caption-text'
              onClick={handleCaptionEdit}
              title={tr('chat.slots.clickToEditDescription')}
              role='button'
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && handleCaptionEdit()}
            >
              {slot.caption || <span className='plugin-slot__caption-placeholder'>{tr('chat.slots.addDescription')}</span>}
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
  readOnly,
  hideImageMutationActions,
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
        sessionId={sessionId}
        slotId={slotId}
        revisionCount={revisionCount}
        onRefresh={onRefresh}
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
        title={tr('chat.slots.addItem')}
        aria-label={tr('chat.slots.addItem')}
      >
        {submitting ? '…' : '+'}
      </button>
      {open && (
        <div
          className='plugin-slot__modal-overlay'
          role='dialog'
          aria-modal='true'
          aria-label={tr('chat.slots.addItem')}
          onClick={(e) => { if (e.target === e.currentTarget) setOpen(false); }}
        >
          <div className='plugin-slot__modal'>
            <div className='plugin-slot__modal-header'>
              <span>{tr('chat.slots.addItem')}</span>
              <button
                className='plugin-slot__modal-close'
                onClick={() => setOpen(false)}
                aria-label={tr('common.close')}
              >×</button>
            </div>
            <div className='plugin-slot__modal-body' onKeyDown={handleKeyDown}>
              {slotType === 'text' && (
                <textarea
                  className='plugin-slot__modal-textarea'
                  value={textValue}
                  onChange={(e) => setTextValue(e.target.value)}
                  placeholder={tr('chat.slots.enterTextContent')}
                  rows={5}
                  autoFocus
                  aria-label={tr('chat.slots.itemContent')}
                />
              )}
              <input
                className='plugin-slot__modal-caption'
                value={caption}
                onChange={(e) => setCaption(e.target.value)}
                placeholder={tr('chat.slots.optionalDescription')}
                aria-label={tr('common.description')}
              />
            </div>
            <div className='plugin-slot__modal-footer'>
              <button
                className='plugin-slot__modal-submit'
                onClick={handleSubmit}
                disabled={submitting || (slotType === 'text' && !textValue.trim())}
                aria-label={tr('chat.slots.confirmAdd')}
              >
                {submitting ? tr('chat.slots.adding') : tr('common.confirm')}
              </button>
              <button
                className='plugin-slot__modal-cancel'
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
