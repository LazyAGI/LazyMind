import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode, type MutableRefObject } from 'react';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { SlotEditingContext } from '../slotEditingContext';

export async function uploadMarkdownArtifact(markdown: string, originalPath: unknown) {
  const filename = typeof originalPath === 'string' ? originalPath.split('/').pop() : undefined;
  const file = new File([markdown], filename || 'artifact.md', {
    type: 'text/markdown;charset=utf-8',
  });
  return { path: await uploadFileInChunks(file), size: file.size };
}

type Field = 'slide' | 'notes';
type Draft = Partial<Record<Field, string>>;
type PageControls = {
  update: (field: Field, text: string, original: string) => void;
  save: () => Promise<boolean>;
  dirty: boolean;
  saving: boolean;
  slide: SlotRevision;
  notes?: SlotRevision;
};
export const PptPageEditContext = createContext<PageControls | null>(null);

export function PptPageEditor({ sessionId, slide, notes, stateVersion, flushRef, refresh, children }: {
  sessionId: string; slide: SlotRevision; notes?: SlotRevision;
  flushRef: MutableRefObject<(() => Promise<boolean>) | null>;
  stateVersion?: number; refresh?: () => void; children: ReactNode;
}) {
  const root = useRef<HTMLDivElement>(null);
  const draft = useRef<Draft>({});
  const baseline = useRef({slide, notes});
  const pending = useRef<Promise<boolean> | null>(null);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const { registerFlush, setEditing } = useContext(SlotEditingContext);
  const editingKey = sessionId + ':ppt-page:' + slide.list_index;
  const savedVersion = useRef<number>();
  if (!dirty && !pending.current && (savedVersion.current === undefined
    || (stateVersion ?? -1) >= savedVersion.current
    || (slide.revision === baseline.current.slide.revision && slide.draft_version === baseline.current.slide.draft_version
      && notes?.revision === baseline.current.notes?.revision))) {
    baseline.current = {slide, notes};
    savedVersion.current = undefined;
  }

  const update = useCallback((field: Field, text: string, original: string) => {
    if (text === original) delete draft.current[field];
    else draft.current[field] = text;
    setDirty(Object.keys(draft.current).length > 0);
  }, []);

  const save = useCallback((): Promise<boolean> => {
    if (pending.current) return pending.current;
    const changes = {...draft.current};
    if (!Object.keys(changes).length) return Promise.resolve(true);
    setSaving(true);
    setError('');
    const base = baseline.current;
    const field = (revision: SlotRevision, text?: string) => ({
      revision: revision.revision, draft_version: revision.draft_version, text,
    });
    pending.current = (async () => {
      try {
        const carrier = base.notes?.artifact_value;
        const file = changes.notes !== undefined && carrier?.path && ['text', 'json'].includes(carrier.type)
          ? await uploadMarkdownArtifact(changes.notes, carrier.path) : undefined;
        const response = await WorkflowSessionApi().savePptPage(sessionId, base.slide.list_index!, {
          slide: field(base.slide, changes.slide),
          notes: base.notes && { ...field(base.notes, file ? undefined : changes.notes), file, list_index: base.notes.list_index! },
        });
        if (response.data?.code !== 0) throw new Error('save failed');
        const result = response.data.data;
        const committed = (slot: SlotRevision, text?: string) => {
          const change = result.changes?.find((item: SlotRevision) => item.slot_id === slot.slot_id);
          const revision = slot === base.slide ? result.revision : result.notes_revision;
          if (revision === slot.revision) return slot;
          let value = text === undefined ? slot.artifact_value : { text };
          if (slot === base.notes && file) {
            value = { ...carrier, ...file, type: 'text' };
            delete value.url;
          }
          return {
            ...slot, artifact_id: change?.artifact_id, revision,
            draft_version: change?.draft_version ?? 1, change_source: 'human' as const,
            created_at: change?.created_at ?? slot.created_at, artifact_value: value,
            revision_count: (slot.revision_count ?? slot.revision) + 1,
            version_number: (slot.revision_count ?? slot.revision) + 1,
          };
        };
        baseline.current = {slide: committed(base.slide, changes.slide),
          notes: base.notes && committed(base.notes, changes.notes)};
        savedVersion.current = result.state_version ?? (stateVersion ?? 0) + 1;
        draft.current = {};
        setDirty(false);
        // A refresh can fail after the server has committed this checkpoint.
        void Promise.resolve().then(() => refresh?.()).catch(() => {});
        return true;
      } catch (err) {
        const conflict = (err as {response?: {status?: number}})?.response?.status === 409;
        setError(conflict ? '本页版本已变化，请保留修改并刷新后重试。' : '本页保存失败，修改已保留。');
        return false;
      } finally {
        pending.current = null;
        setSaving(false);
      }
    })();
    return pending.current;
  }, [sessionId, refresh, stateVersion]);

  useEffect(() => {
    flushRef.current = dirty ? save : null;
    const unregister = registerFlush(editingKey, save);
    return () => { flushRef.current = null; unregister(); };
  }, [dirty, save, editingKey, flushRef, registerFlush]);
  useEffect(() => {
    setEditing(editingKey, dirty);
    return () => setEditing(editingKey, false);
  }, [dirty, editingKey, setEditing]);
  useEffect(() => {
    if (!dirty) return;
    const outside = (event: Event) => {
      const target = event.target;
      if (!(target instanceof Element) || root.current?.contains(target)
        || target.closest('.slot-html-slide__zoom-overlay, .mdxeditor-popup-container')) return;
      void save();
    };
    document.addEventListener('pointerdown', outside, true);
    document.addEventListener('focusin', outside, true);
    return () => {
      document.removeEventListener('pointerdown', outside, true);
      document.removeEventListener('focusin', outside, true);
    };
  }, [dirty, save]);
  const {slide: visibleSlide, notes: visibleNotes} = baseline.current;
  const controls = useMemo(() => ({ update, save, dirty, saving, slide: visibleSlide, notes: visibleNotes }),
    [update, save, dirty, saving, visibleSlide, visibleNotes]);
  return <PptPageEditContext.Provider value={controls}>
    <div ref={root} className="ppt-page-editor">
      <div ref={element => { if (element) element.inert = saving; }}>{children}</div>
      {(dirty || error) && <div className="workflow-slot__artifact-footer">
        {error && <span role="alert">{error}</span>}
        <button className="workflow-panel__action-btn workflow-panel__action-btn--secondary" disabled={saving}
          onClick={() => void save()}>{saving ? '保存中…' : '保存本页'}</button>
      </div>}
    </div>
  </PptPageEditContext.Provider>;
}
