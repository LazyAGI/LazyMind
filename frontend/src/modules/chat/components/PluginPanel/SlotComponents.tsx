import { useState, useCallback, useRef, useEffect, useMemo } from "react";
import ReactDOM from "react-dom";
import type { SlotRevision, SlotVersionEntry } from "@/modules/chat/store/pluginPanel";
import { usePluginStore, draftStore } from "@/modules/chat/store/pluginPanel";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";
import { buildDiffLines } from "@/modules/memory/shared";

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
}

function TextDiffView({ currentText, otherText, otherLabel }: TextDiffViewProps) {
  const diffLines = useMemo(
    () => buildDiffLines(otherText, currentText),
    [currentText, otherText],
  );

  return (
    <div className='plugin-slot__version-diff'>
      <div className='plugin-slot__version-diff-header'>
        <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--remove'>
          {otherLabel}
        </span>
        <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--add'>
          当前版本
        </span>
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
            <code>{line.text}</code>
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

type PopoverKey = string; // `${sessionId}:${slotId}:${sortOrder}`
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
  sortOrder: number;
  revisionCount: number;
  currentValue?: any;
  currentChangeSource?: 'ai' | 'human';
  contentType?: string;
  onRollbackDone?: () => void;
}

export function SlotVersionPopover({
  sessionId,
  slotId,
  sortOrder,
  revisionCount,
  currentValue,
  currentChangeSource,
  contentType,
  onRollbackDone,
}: SlotVersionPopoverProps) {
  const popoverKey: PopoverKey = `${sessionId}:${slotId}:${sortOrder}`;
  const [open, setOpen] = useGlobalPopoverOpen(popoverKey);
  const [versions, setVersions] = useState<SlotVersionEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedRevision, setSelectedRevision] = useState<SlotVersionEntry | null>(null);
  const [rolling, setRolling] = useState(false);
  const { getSlotVersions, rollbackSlotItem } = usePluginStore();

  const handleOpen = useCallback(async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (open) {
      setOpen(false);
      return;
    }
    setLoading(true);
    try {
      const vs = await getSlotVersions(sessionId, slotId, sortOrder);
      setVersions(vs);
      const current = vs.find((v) => v.selected) ?? vs[vs.length - 1] ?? null;
      setSelectedRevision(current);
      setOpen(true);
    } finally {
      setLoading(false);
    }
  }, [open, sessionId, slotId, sortOrder, getSlotVersions, setOpen]);

  const handleClose = useCallback(() => setOpen(false), [setOpen]);

  const handleOverlayClick = useCallback((e: React.MouseEvent) => {
    if (e.target === e.currentTarget) handleClose();
  }, [handleClose]);

  const handleRollback = useCallback(async (revision: number) => {
    setRolling(true);
    try {
      await rollbackSlotItem(sessionId, slotId, sortOrder, revision);
      setOpen(false);
      onRollbackDone?.();
    } finally {
      setRolling(false);
    }
  }, [sessionId, slotId, sortOrder, rollbackSlotItem, setOpen, onRollbackDone]);

  const isImage = contentType === 'image';
  const badge = currentChangeSource === 'human' ? '✏' : undefined;

  // Extract plain text from a content_snapshot or artifact_value.
  // Prefers .text field so JSON wrapper ({"text": "..."}) is never shown.
  const extractText = (snapshot: any): string => {
    if (!snapshot) return '';
    if (typeof snapshot === 'string') return snapshot;
    if (snapshot?.text !== undefined) return String(snapshot.text);
    if (snapshot?.data !== undefined) {
      return typeof snapshot.data === 'string' ? snapshot.data : JSON.stringify(snapshot.data, null, 2);
    }
    const url = snapshot?.url || snapshot?.path;
    if (url) return url;
    return JSON.stringify(snapshot, null, 2);
  };

  const popoverContent = open ? ReactDOM.createPortal(
    <div
      className='plugin-slot__version-overlay'
      onClick={handleOverlayClick}
      role='presentation'
    >
      <div
        className='plugin-slot__version-popover'
        role='dialog'
        aria-label='版本历史'
        aria-modal='true'
        onClick={(e) => e.stopPropagation()}
      >
        <div className='plugin-slot__version-popover-header'>
          <span>版本历史</span>
          <button
            className='plugin-slot__version-popover-close'
            onClick={handleClose}
            aria-label='关闭版本历史'
          >×</button>
        </div>
        <div className='plugin-slot__version-popover-body'>
          <ul className='plugin-slot__version-list' role='listbox' aria-label='版本列表'>
            {versions.map((v) => (
              <li
                key={v.revision}
                role='option'
                aria-selected={selectedRevision?.revision === v.revision}
                className={[
                  'plugin-slot__version-item',
                  v.selected ? 'plugin-slot__version-item--current' : '',
                  selectedRevision?.revision === v.revision ? 'plugin-slot__version-item--focused' : '',
                ].join(' ')}
                onClick={() => setSelectedRevision(v)}
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
          {selectedRevision && !selectedRevision.selected ? (
            <div className='plugin-slot__version-compare'>
              {isImage ? (
                <div className='plugin-slot__version-compare-cols'>
                  <div className='plugin-slot__version-compare-col'>
                    <div className='plugin-slot__version-compare-label'>当前版本</div>
                    <img
                      className='plugin-slot__version-compare-img'
                      src={extractText(currentValue)}
                      alt='当前版本'
                    />
                  </div>
                  <div className='plugin-slot__version-compare-col'>
                    <div className='plugin-slot__version-compare-label'>
                      {`v${selectedRevision.revision} · ${selectedRevision.change_source === 'human' ? '手动编辑' : 'AI 生成'}`}
                    </div>
                    <img
                      className='plugin-slot__version-compare-img'
                      src={extractText(selectedRevision.content_snapshot)}
                      alt={`v${selectedRevision.revision}`}
                    />
                  </div>
                </div>
              ) : (
                <TextDiffView
                  currentText={extractText(currentValue)}
                  otherText={extractText(selectedRevision.content_snapshot)}
                  otherLabel={`v${selectedRevision.revision} · ${selectedRevision.change_source === 'human' ? '手动编辑' : 'AI 生成'}`}
                />
              )}
              <button
                className='plugin-slot__version-apply-btn'
                disabled={rolling}
                onClick={() => handleRollback(selectedRevision.revision)}
                aria-label={`应用 v${selectedRevision.revision}`}
              >
                {rolling ? '回退中…' : `应用此版本 (v${selectedRevision.revision})`}
              </button>
            </div>
          ) : (
            <div className='plugin-slot__version-compare plugin-slot__version-compare--same'>
              {selectedRevision ? (
                isImage ? (
                  <img
                    className='plugin-slot__version-compare-img plugin-slot__version-compare-img--solo'
                    src={extractText(currentValue)}
                    alt='当前版本'
                  />
                ) : (
                  <pre className='plugin-slot__version-current-text'>
                    {extractText(currentValue) || '（无内容）'}
                  </pre>
                )
              ) : (
                <div className='plugin-slot__version-compare-hint'>选择版本查看对比</div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>,
    document.body,
  ) : null;

  return (
    <div className='plugin-slot__version-wrap'>
      <button
        className='plugin-slot__version-btn'
        onClick={handleOpen}
        title={`版本历史 (${revisionCount})`}
        aria-label={`版本历史 (${revisionCount})`}
        disabled={loading}
      >
        {badge && <span className='plugin-slot__version-badge' aria-hidden='true'>{badge}</span>}
        <span className='plugin-slot__version-count'>{revisionCount > 1 ? `v${revisionCount}` : 'v1'}</span>
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
  const url: string = raw?.url || (raw?.path ? resolveCoreAssetUrl(raw.path) : '');
  const alt: string = slot.caption ?? raw?.alt ?? '';
  const { deleteSlotItem, patchSlotCaption } = usePluginStore();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [captionEditing, setCaptionEditing] = useState(false);
  const [captionDraft, setCaptionDraft] = useState('');

  const handleDeleteClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setConfirmDelete(true);
  }, []);

  const handleDeleteConfirm = useCallback(async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!sessionId || !slotId || slot.sort_order === undefined) return;
    await deleteSlotItem(sessionId, slotId, slot.sort_order);
    setConfirmDelete(false);
    onRefresh?.();
  }, [sessionId, slotId, slot.sort_order, deleteSlotItem, onRefresh]);

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
    if (!sessionId || !slotId || slot.sort_order === undefined) return;
    setCaptionEditing(false);
    await patchSlotCaption(sessionId, slotId, slot.sort_order, captionDraft);
    onRefresh?.();
  }, [sessionId, slotId, slot.sort_order, captionDraft, patchSlotCaption, onRefresh]);

  const handleCaptionKeyDown = useCallback((e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') handleCaptionSave();
    if (e.key === 'Escape') setCaptionEditing(false);
  }, [handleCaptionSave]);

  if (!url) return <SlotPending type='image' cardMode={cardMode} />;

  const actions = (sessionId && slotId && slot.sort_order !== undefined) ? (
    <div className='plugin-slot__image-actions'>
      {isDraggable && (
        <span className='plugin-slot__drag-handle' title='拖拽排序' aria-hidden='true'>⠿</span>
      )}
      {onReference && (
        <button
          className='plugin-slot__ref-btn'
          onClick={handleReference}
          title='引用此图片'
          aria-label='引用此图片'
        >
          📎
        </button>
      )}
      {confirmDelete ? (
        <span className='plugin-slot__delete-confirm'>
          <span className='plugin-slot__delete-confirm-text'>确认删除？</span>
          <button
            className='plugin-slot__delete-confirm-yes'
            onClick={handleDeleteConfirm}
            aria-label='确认删除'
          >
            删除
          </button>
          <button
            className='plugin-slot__delete-confirm-no'
            onClick={handleDeleteCancel}
            aria-label='取消删除'
          >
            取消
          </button>
        </span>
      ) : (
        <button
          className='plugin-slot__delete-btn'
          onClick={handleDeleteClick}
          title='删除'
          aria-label='删除图片'
        >
          🗑
        </button>
      )}
      {revisionCount !== undefined && revisionCount > 0 && (
        <SlotVersionPopover
          sessionId={sessionId}
          slotId={slotId}
          sortOrder={slot.sort_order}
          revisionCount={revisionCount}
          currentValue={slot.artifact_value}
          currentChangeSource={slot.change_source}
          contentType='image'
          onRollbackDone={onRefresh}
        />
      )}
    </div>
  ) : null;

  if (cardMode) {
    return (
      <div className='plugin-slot plugin-slot--image-card'>
        <img src={url} alt={alt} className='plugin-slot__image-card-img' loading='lazy' />
        {alt && <div className='plugin-slot__image-card-caption'>{alt}</div>}
        {actions}
        {sessionId && slotId && slot.sort_order !== undefined && (
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
      {actions}
      {sessionId && slotId && slot.sort_order !== undefined && (
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
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [offloadedText, setOffloadedText] = useState<string | null>(null);
  const [offloadLoading, setOffloadLoading] = useState(false);
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

  const canEdit = Boolean(sessionId && slotId && slot.sort_order !== undefined);

  // On mount: restore localStorage draft only if it differs from the current artifact text.
  // A draft equal to the artifact text carries no unsaved change and should be discarded
  // to avoid triggering a spurious flush later.
  useEffect(() => {
    if (!canEdit || !sessionId || !slotId || slot.sort_order === undefined) return;
    const saved = draftStore.getLocalDraft(sessionId, slotId, slot.sort_order);
    if (saved?.text !== undefined && String(saved.text) !== text) {
      setDraft(String(saved.text));
    } else if (saved?.text !== undefined) {
      // Draft matches artifact — discard it so no flush is scheduled.
      draftStore.cancelDraft(sessionId, slotId, slot.sort_order);
    }
  // Run only on mount (stable deps).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleEdit = () => {
    // Prefer an in-memory draft over the persisted artifact value, but only when
    // the draft actually contains a change; otherwise start fresh from artifact text.
    const saved = (sessionId && slotId && slot.sort_order !== undefined)
      ? draftStore.getLocalDraft(sessionId, slotId, slot.sort_order)
      : null;
    const savedText = saved?.text !== undefined ? String(saved.text) : undefined;
    setDraft(savedText !== undefined && savedText !== text ? savedText : text);
    setEditing(true);
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value;
    setDraft(val);
    if (sessionId && slotId && slot.sort_order !== undefined) {
      draftStore.setDraft(sessionId, slotId, slot.sort_order, { text: val });
    }
  };

  // Save / Ctrl+S / onBlur: persist to localStorage only when content has changed.
  // If draft equals the original artifact text, discard the draft so no backend
  // revision is created (flushDraft would otherwise fire after the delay).
  const handleSave = () => {
    if (cancelledRef.current) {
      cancelledRef.current = false;
      return;
    }
    if (sessionId && slotId && slot.sort_order !== undefined) {
      if (draft !== text) {
        draftStore.setDraft(sessionId, slotId, slot.sort_order, { text: draft });
      } else {
        // No change — cancel any pending draft so the 60s flush never fires.
        draftStore.cancelDraft(sessionId, slotId, slot.sort_order);
      }
    }
    setEditing(false);
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
    if (sessionId && slotId && slot.sort_order !== undefined) {
      draftStore.cancelDraft(sessionId, slotId, slot.sort_order);
    }
    setEditing(false);
  };

  // Caption helpers.
  const handleCaptionEdit = () => {
    setCaptionDraft(slot.caption ?? '');
    setCaptionEditing(true);
  };

  const handleCaptionSave = async () => {
    if (!sessionId || !slotId || slot.sort_order === undefined) return;
    setCaptionEditing(false);
    await patchSlotCaption(sessionId, slotId, slot.sort_order, captionDraft);
    onRefresh?.();
  };

  const handleCaptionKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') handleCaptionSave();
    if (e.key === 'Escape') setCaptionEditing(false);
  };

  // Determine display text: prefer draft if user is not editing (shows unsaved draft).
  const displayText = (() => {
    if (editing) return draft;
    if (sessionId && slotId && slot.sort_order !== undefined) {
      const saved = draftStore.getLocalDraft(sessionId, slotId, slot.sort_order);
      if (saved?.text !== undefined) return String(saved.text);
    }
    return text;
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
            {revisionCount !== undefined && revisionCount > 0 && sessionId && slotId && slot.sort_order !== undefined && (
              <SlotVersionPopover
                sessionId={sessionId}
                slotId={slotId}
                sortOrder={slot.sort_order}
                revisionCount={revisionCount}
                currentValue={slot.artifact_value}
                currentChangeSource={slot.change_source}
                contentType='text'
                onRollbackDone={onRefresh}
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

export function SlotFile({ slot }: { slot: SlotRevision }) {
  const raw = slot.artifact_value;
  const url: string = raw?.url || (raw?.path ? resolveCoreAssetUrl(raw.path) : '');
  const name: string = raw?.filename ?? raw?.name ?? slot.artifact_key;
  const size: number | undefined = raw?.size;

  if (!url) return <SlotPending type='file' />;

  return (
    <div className='plugin-slot plugin-slot--file'>
      <a
        href={url}
        download={name}
        target='_blank'
        rel='noopener noreferrer'
        className='plugin-slot__file-link'
        aria-label={`Download ${name}`}
      >
        <span className='plugin-slot__file-icon' aria-hidden='true'>📄</span>
        <span className='plugin-slot__file-name'>{name}</span>
        {size !== undefined && (
          <span className='plugin-slot__file-size'>({(size / 1024).toFixed(1)} KB)</span>
        )}
      </a>
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
  if (normalized === 'file') return <SlotFile slot={slot} />;
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

  const handleOpen = () => {
    setTextValue('');
    setCaption('');
    setOpen(true);
  };

  const handleSubmit = async () => {
    if (slotType === 'text' && !textValue.trim()) return;
    setSubmitting(true);
    try {
      const value = slotType === 'text' ? { text: textValue } : { text: textValue };
      await createSlotItem(sessionId, slotId, value, caption || undefined);
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
      <button
        className='plugin-slot__add-btn'
        onClick={handleOpen}
        title='添加条目'
        aria-label='添加条目'
      >
        +
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
              {(slotType === 'image' || slotType === 'file') && (
                <p className='plugin-slot__modal-hint'>
                  请先上传文件，将 stored_path 填入下方（或使用文件上传流程）。
                </p>
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
