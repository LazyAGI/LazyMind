import { useState, useCallback, useRef, useEffect } from "react";
import type { SlotRevision, SlotVersionEntry } from "@/modules/chat/store/pluginPanel";
import { usePluginStore } from "@/modules/chat/store/pluginPanel";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";

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

// --------------------------------------------------------------------------
// SlotVersionPopover — 版本历史浮层
// 左侧版本列表 + 右侧当前 vs 选中版本并排对比
// --------------------------------------------------------------------------

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
  const [open, setOpen] = useState(false);
  const [versions, setVersions] = useState<SlotVersionEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedRevision, setSelectedRevision] = useState<SlotVersionEntry | null>(null);
  const [rolling, setRolling] = useState(false);
  const openRef = useRef(false);
  const { getSlotVersions, rollbackSlotItem } = usePluginStore();

  const handleOpen = useCallback(async () => {
    if (openRef.current) {
      openRef.current = false;
      setOpen(false);
      return;
    }
    setLoading(true);
    try {
      const vs = await getSlotVersions(sessionId, slotId, sortOrder);
      setVersions(vs);
      const current = vs.find((v) => v.selected) ?? vs[vs.length - 1] ?? null;
      setSelectedRevision(current);
      openRef.current = true;
      setOpen(true);
    } finally {
      setLoading(false);
    }
  }, [sessionId, slotId, sortOrder, getSlotVersions]);

  const handleRollback = useCallback(async (revision: number) => {
    setRolling(true);
    try {
      await rollbackSlotItem(sessionId, slotId, sortOrder, revision);
      openRef.current = false;
      setOpen(false);
      onRollbackDone?.();
    } finally {
      setRolling(false);
    }
  }, [sessionId, slotId, sortOrder, rollbackSlotItem, onRollbackDone]);

  const isImage = contentType === 'image';
  const badge = currentChangeSource === 'human' ? '✏' : undefined;

  // Resolve a preview URL/text from a content_snapshot value.
  const resolvePreview = (snapshot: any): { url?: string; text?: string } => {
    if (!snapshot) return {};
    if (typeof snapshot === 'string') {
      if (snapshot.startsWith('http') || snapshot.startsWith('/')) return { url: snapshot };
      return { text: snapshot.slice(0, 300) };
    }
    const url = snapshot?.url || snapshot?.path;
    if (url) return { url };
    return { text: JSON.stringify(snapshot, null, 2).slice(0, 300) };
  };

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
      {open && (
        <div className='plugin-slot__version-popover' role='dialog' aria-label='版本历史' aria-modal='true'>
          <div className='plugin-slot__version-popover-header'>
            <span>版本历史</span>
            <button
              className='plugin-slot__version-popover-close'
              onClick={() => setOpen(false)}
              aria-label='关闭版本历史'
            >×</button>
          </div>
          <div className='plugin-slot__version-popover-body'>
            {/* Left pane: version list */}
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
            {/* Right pane: comparison */}
            <div className='plugin-slot__version-compare'>
              <div className='plugin-slot__version-compare-cols'>
                {/* Current version */}
                <div className='plugin-slot__version-compare-col'>
                  <div className='plugin-slot__version-compare-label'>当前版本</div>
                  {isImage ? (
                    <img
                      className='plugin-slot__version-compare-img'
                      src={resolvePreview(currentValue).url ?? ''}
                      alt='当前版本'
                    />
                  ) : (
                    <pre className='plugin-slot__version-compare-text'>
                      {resolvePreview(currentValue).text ?? '（无内容）'}
                    </pre>
                  )}
                </div>
                {/* Selected historical version */}
                <div className='plugin-slot__version-compare-col'>
                  <div className='plugin-slot__version-compare-label'>
                    {selectedRevision
                      ? `v${selectedRevision.revision} · ${selectedRevision.change_source === 'human' ? '手动编辑' : 'AI 生成'}`
                      : '选择版本'}
                  </div>
                  {selectedRevision ? (
                    isImage ? (
                      <img
                        className='plugin-slot__version-compare-img'
                        src={resolvePreview(selectedRevision.content_snapshot).url ?? ''}
                        alt={`v${selectedRevision.revision}`}
                      />
                    ) : (
                      <pre className='plugin-slot__version-compare-text'>
                        {resolvePreview(selectedRevision.content_snapshot).text ?? '（无内容）'}
                      </pre>
                    )
                  ) : (
                    <div className='plugin-slot__version-compare-empty'>点击左侧版本预览</div>
                  )}
                </div>
              </div>
              {selectedRevision && !selectedRevision.selected && (
                <button
                  className='plugin-slot__version-apply-btn'
                  disabled={rolling}
                  onClick={() => handleRollback(selectedRevision.revision)}
                  aria-label={`应用 v${selectedRevision.revision}`}
                >
                  {rolling ? '回退中…' : `应用此版本 (v${selectedRevision.revision})`}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
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
  const { deleteSlotItem } = usePluginStore();
  const [confirmDelete, setConfirmDelete] = useState(false);

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
      </div>
    );
  }
  return (
    <div className='plugin-slot plugin-slot--image'>
      <img src={url} alt={alt} className='plugin-slot__image' loading='lazy' />
      {actions}
    </div>
  );
}

// --------------------------------------------------------------------------
// SlotText with inline editing and version badge
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
  const { patchSlotItemValue } = usePluginStore();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [offloadedText, setOffloadedText] = useState<string | null>(null);
  const [offloadLoading, setOffloadLoading] = useState(false);

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

  const handleEdit = () => {
    setDraft(text);
    setEditing(true);
  };

  const handleSave = async () => {
    if (!sessionId || !slotId || slot.sort_order === undefined) return;
    await patchSlotItemValue(sessionId, slotId, slot.sort_order, { text: draft });
    setEditing(false);
    onRefresh?.();
  };

  const handleCancel = () => setEditing(false);

  return (
    <div className='plugin-slot plugin-slot--text'>
      {editing ? (
        <div className='plugin-slot__text-edit'>
          <textarea
            className='plugin-slot__text-editor'
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={6}
            aria-label='编辑文本'
          />
          <div className='plugin-slot__text-edit-actions'>
            <button className='plugin-slot__text-save' onClick={handleSave}>保存</button>
            <button className='plugin-slot__text-cancel' onClick={handleCancel}>取消</button>
          </div>
        </div>
      ) : (
        <>
          <p className='plugin-slot__text'>{text}</p>
          <div className='plugin-slot__text-meta'>
            {canEdit && (
              <button className='plugin-slot__text-edit-btn' onClick={handleEdit} title='编辑' aria-label='编辑文本'>
                ✏
              </button>
            )}
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
