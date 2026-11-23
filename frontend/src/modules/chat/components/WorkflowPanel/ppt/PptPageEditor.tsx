import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode, type MutableRefObject } from 'react';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { SlotEditingContext } from '../slotEditingContext';

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

export function PptPageEditor({ sessionId, slide, notes, flushRef, refresh, children }: {
  sessionId: string; slide: SlotRevision; notes?: SlotRevision;
  flushRef: MutableRefObject<(() => Promise<boolean>) | null>;
  refresh?: () => void; children: ReactNode;
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
  if (!dirty && !pending.current) baseline.current = {slide, notes};

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
        const response = await WorkflowSessionApi().savePptPage(sessionId, base.slide.list_index!, {
          slide: field(base.slide, changes.slide),
          notes: base.notes && { ...field(base.notes, changes.notes), list_index: base.notes.list_index! },
        });
        if (response.data?.code !== 0) throw new Error('save failed');
        await refresh?.();
        draft.current = {};
        setDirty(false);
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
  }, [sessionId, refresh]);

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
