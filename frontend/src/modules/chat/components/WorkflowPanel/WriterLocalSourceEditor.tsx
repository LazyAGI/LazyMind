import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import katex from 'katex';
import MermaidBlock from '../MarkdownViewer/MermaidBlock';
import { WriterFormula, WriterSourcePreview } from './WriterSourcePreview';
import { WriterMapPreview, WriterModelPreview } from './WriterGeometryPreview';
import './WriterLocalSourceEditor.scss';

/** A local editing surface; source remains part of the owning document. */
export function WriterLocalSourceEditor({ source, kind, inline = false, readOnly = false, onUndoRedo, onChange }: {
  source: string; kind: 'math' | 'mermaid' | 'source' | 'geojson' | 'topojson' | 'stl'; inline?: boolean;
  onUndoRedo?: (redo: boolean) => void;
  readOnly?: boolean; onChange: (source: string) => void;
}) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [preview, setPreview] = useState(source);
  const trigger = useRef<HTMLButtonElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const undoRedo = useRef(onUndoRedo);
  undoRedo.current = onUndoRedo;
  useEffect(() => {
    const timer = window.setTimeout(() => setPreview(source), 180);
    return () => window.clearTimeout(timer);
  }, [source]);
  useEffect(() => {
    const element = input.current;
    if (!editing || !element) return;
    element.focus();
    const keydown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && ['z', 'y'].includes(event.key.toLowerCase())) {
        if (!undoRedo.current) return;
        event.preventDefault(); undoRedo.current(event.shiftKey || event.key.toLowerCase() === 'y');
      }
      if (event.key === 'Escape') { event.preventDefault(); setEditing(false); trigger.current?.focus(); }
      event.stopPropagation();
    };
    element.addEventListener('keydown', keydown);
    return () => element.removeEventListener('keydown', keydown);
  }, [editing]);
  let formulaError = false;
  if (kind === 'math') {
    try { katex.renderToString(preview, { displayMode: !inline, throwOnError: true, trust: false }); }
    catch { formulaError = true; }
  }
  const label = t(`chat.writerLocal.${['geojson', 'topojson', 'stl'].includes(kind) ? 'source' : kind}`);
  const Wrapper = inline ? 'span' : 'div';
  return <Wrapper className={`writer-local-source${inline ? ' writer-local-source--inline' : ''}`}
    contentEditable={false} data-writer-local-source='true'
    onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setEditing(false); }}
    onKeyDown={(event) => {
      if (event.key === 'Escape' && editing) {
        event.preventDefault(); event.stopPropagation(); setEditing(false); trigger.current?.focus();
      }
      // Nested inputs handle typing themselves; let undo/redo reach the document editor.
      if (event.target === input.current && !((event.metaKey || event.ctrlKey) && ['z', 'y'].includes(event.key.toLowerCase()))) event.stopPropagation();
    }}>
    <Wrapper className='writer-local-source__preview'
      {...(kind === 'math' && !readOnly ? { role: 'button', tabIndex: 0, 'aria-label': t('chat.writerLocal.edit', { kind: label }),
        onClick: () => setEditing(true), onKeyDown: (event: React.KeyboardEvent) => {
          if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); event.stopPropagation(); setEditing(true); }
        } } : {})}>
      {kind === 'mermaid' ? <MermaidBlock code={preview} />
        : kind === 'math' ? <WriterFormula source={preview} block={!inline} />
          : kind === 'geojson' || kind === 'topojson' ? <WriterMapPreview source={preview} language={kind} />
            : kind === 'stl' ? <WriterModelPreview source={preview} />
          : <WriterSourcePreview source={preview} />}
    </Wrapper>
    {!readOnly && <button type='button' ref={trigger} className='writer-local-source__edit'
      aria-label={t(kind === 'math' ? 'chat.writerLocal.toggleMath' : 'chat.writerLocal.edit', { kind: label })} aria-expanded={editing}
      onClick={() => setEditing(!editing)}>{t(editing ? 'chat.writerLocal.done' : 'chat.writerSource.rich')}</button>}
    {editing && !readOnly && <Wrapper className='writer-local-source__input'>
      <textarea ref={input} aria-label={t('chat.writerLocal.input', { kind: label })} value={source}
        rows={inline ? 2 : 5} onChange={(event) => onChange(event.target.value)} spellCheck={false} />
      {source !== preview && <span role='status'>{t('chat.writerLocal.updating')}</span>}
    </Wrapper>}
    {formulaError && <span role='status'>{t('chat.writerLocal.mathError')}</span>}
  </Wrapper>;
}
