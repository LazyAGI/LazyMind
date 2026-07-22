import {
  createElement,
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import {
  countWriterBlocks,
  deleteWriterBlock,
  findWriterBlock,
  getWriterSpanStyles,
  insertWriterParagraphAfter,
  moveWriterBlock,
  updateWriterBlockContent,
  updateWriterDocumentTitle,
  type WriterBlock,
  type WriterDocument,
  type WriterSpan,
} from './writerIR';
import './WriterIRControl.scss';

export interface WriterIRControlProps {
  document: WriterDocument;
  sourceRevision?: string | number;
  readOnly?: boolean;
  onSave?: (document: WriterDocument) => Promise<void>;
  onEditingChange?: (editing: boolean) => void;
}

interface BlockRenderProps {
  blocks: WriterBlock[];
  mode: 'view' | 'edit';
  selectedNodeId?: string;
  documentReadOnly: boolean;
  onSelect: (nodeId: string) => void;
  onContentChange: (block: WriterBlock, content: string) => void;
  onTextFocus: () => void;
  onTextBlur: () => void;
  onInsertAfter: (nodeId: string) => void;
  onDelete: (block: WriterBlock) => void;
  onMove: (nodeId: string, direction: 'up' | 'down') => void;
}

function asHeadingLevel(block: WriterBlock): 2 | 3 | 4 | 5 | 6 {
  const raw = Number(block.numbering?.level ?? 2);
  if (!Number.isFinite(raw)) return 2;
  return Math.min(6, Math.max(2, Math.trunc(raw))) as 2 | 3 | 4 | 5 | 6;
}

function renderMarkedText(text: string, styles: string[], key: string) {
  let content = <Fragment>{text}</Fragment>;
  if (styles.includes('code')) content = <code>{content}</code>;
  if (styles.includes('bold')) content = <strong>{content}</strong>;
  if (styles.includes('italic')) content = <em>{content}</em>;
  if (styles.includes('underline')) content = <u>{content}</u>;
  if (styles.includes('strike') || styles.includes('strikethrough')) content = <s>{content}</s>;
  return <Fragment key={key}>{content}</Fragment>;
}

function SpanContent({ block }: { block: WriterBlock }) {
  const content = block.content ?? '';
  const spans = block.spans ?? [];
  const joined = spans.map((span) => span.text).join('');
  if (spans.length === 0 || joined !== content) return <>{content}</>;
  return (
    <>
      {spans.map((span: WriterSpan, index) => (
        renderMarkedText(span.text, getWriterSpanStyles(span), `${block.node_id}-${index}`)
      ))}
    </>
  );
}

function textRows(content: string, type: string): number {
  if (type === 'code') return Math.min(18, Math.max(5, content.split('\n').length));
  return Math.min(10, Math.max(1, content.split('\n').length));
}

function BlockActions({
  block,
  onInsertAfter,
  onDelete,
  onMove,
}: {
  block: WriterBlock;
  onInsertAfter: (nodeId: string) => void;
  onDelete: (block: WriterBlock) => void;
  onMove: (nodeId: string, direction: 'up' | 'down') => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      className='writer-ir__block-actions'
      role='toolbar'
      aria-label={t('chat.writerIR.blockActions', { type: block.type })}
    >
      <button type='button' onClick={() => onInsertAfter(block.node_id)}>
        {t('chat.writerIR.addParagraph')}
      </button>
      <button type='button' onClick={() => onMove(block.node_id, 'up')}>
        {t('chat.writerIR.moveUp')}
      </button>
      <button type='button' onClick={() => onMove(block.node_id, 'down')}>
        {t('chat.writerIR.moveDown')}
      </button>
      <button
        type='button'
        className='writer-ir__delete-action'
        onClick={() => onDelete(block)}
      >
        {t('common.delete')}
      </button>
    </div>
  );
}

function EditableText({
  block,
  onContentChange,
  onTextFocus,
  onTextBlur,
}: {
  block: WriterBlock;
  onContentChange: (block: WriterBlock, content: string) => void;
  onTextFocus: () => void;
  onTextBlur: () => void;
}) {
  const { t } = useTranslation();
  return (
    <textarea
      className={`writer-ir__text-editor${block.type === 'heading' ? ' writer-ir__text-editor--heading' : ''}${block.type === 'code' ? ' writer-ir__text-editor--code' : ''}`}
      value={block.content ?? ''}
      rows={textRows(block.content ?? '', block.type)}
      aria-label={t('chat.writerIR.editBlock', { type: block.type })}
      onFocus={onTextFocus}
      onBlur={onTextBlur}
      onChange={(event) => onContentChange(block, event.target.value)}
    />
  );
}

function PreviewBlockContent({ block }: { block: WriterBlock }) {
  if (block.type === 'heading') {
    return createElement(
      `h${asHeadingLevel(block)}`,
      { className: `writer-ir__heading writer-ir__heading--${asHeadingLevel(block)}` },
      <SpanContent block={block} />,
    );
  }
  if (block.type === 'code') {
    return (
      <pre className='writer-ir__code'><code><SpanContent block={block} /></code></pre>
    );
  }
  if (block.type === 'paragraph') {
    return <p className='writer-ir__paragraph'><SpanContent block={block} /></p>;
  }
  if (block.type === 'quote') {
    return <blockquote className='writer-ir__quote'><SpanContent block={block} /></blockquote>;
  }
  if (block.type === 'divider') return <hr className='writer-ir__divider' />;
  return (
    <div className='writer-ir__fallback'>
      <SpanContent block={block} />
    </div>
  );
}

function BlockShell({
  block,
  mode,
  selectedNodeId,
  documentReadOnly,
  onSelect,
  onContentChange,
  onTextFocus,
  onTextBlur,
  onInsertAfter,
  onDelete,
  onMove,
  children,
}: Omit<BlockRenderProps, 'blocks'> & { block: WriterBlock; children?: ReactNode }) {
  const { t } = useTranslation();
  const selected = selectedNodeId === block.node_id;
  const editable = mode === 'edit' && !documentReadOnly && block.editable !== false && block.type !== 'document';

  return (
    <div
      className={`writer-ir__block${selected ? ' writer-ir__block--selected' : ''}${editable ? ' writer-ir__block--editable' : ''}`}
      data-node-id={block.node_id}
      data-node-type={block.type}
    >
      {editable ? (
        <EditableText
          block={block}
          onContentChange={onContentChange}
          onTextFocus={() => {
            onSelect(block.node_id);
            onTextFocus();
          }}
          onTextBlur={onTextBlur}
        />
      ) : (
        <PreviewBlockContent block={block} />
      )}
      {children}
      {selected && editable && (
        <BlockActions
          block={block}
          onInsertAfter={onInsertAfter}
          onDelete={onDelete}
          onMove={onMove}
        />
      )}
      {mode === 'edit' && block.editable === false && (
        <span className='writer-ir__readonly-label'>{t('chat.writerIR.readOnlyBlock')}</span>
      )}
    </div>
  );
}

function ListItemBlock({
  block,
  renderProps,
}: {
  block: WriterBlock;
  renderProps: Omit<BlockRenderProps, 'blocks'>;
}) {
  return (
    <li className='writer-ir__list-item'>
      <BlockShell block={block} {...renderProps}>
        {(block.children?.length ?? 0) > 0 && (
          <BlockSequence blocks={block.children ?? []} {...renderProps} />
        )}
      </BlockShell>
    </li>
  );
}

function BlockSequence(props: BlockRenderProps) {
  const { blocks, ...renderProps } = props;
  const rendered: ReactNode[] = [];

  for (let index = 0; index < blocks.length;) {
    const block = blocks[index];
    if (block.type === 'list_item') {
      const ordered = Boolean(block.numbering?.ordered);
      const group: WriterBlock[] = [];
      while (
        index < blocks.length
        && blocks[index].type === 'list_item'
        && Boolean(blocks[index].numbering?.ordered) === ordered
      ) {
        group.push(blocks[index]);
        index += 1;
      }
      const ListTag = ordered ? 'ol' : 'ul';
      rendered.push(
        <ListTag className='writer-ir__list' key={`list-${group[0].node_id}`}>
          {group.map((item) => (
            <ListItemBlock key={item.node_id} block={item} renderProps={renderProps} />
          ))}
        </ListTag>,
      );
      continue;
    }

    index += 1;
    if (block.type === 'document') {
      rendered.push(
        <section className='writer-ir__document-root' key={block.node_id}>
          <BlockSequence blocks={block.children ?? []} {...renderProps} />
        </section>,
      );
      continue;
    }
    rendered.push(
      <BlockShell block={block} key={block.node_id} {...renderProps}>
        {(block.children?.length ?? 0) > 0 && (
          <div className='writer-ir__children'>
            <BlockSequence blocks={block.children ?? []} {...renderProps} />
          </div>
        )}
      </BlockShell>,
    );
  }
  return <>{rendered}</>;
}

export function WriterIRControl({
  document,
  sourceRevision,
  readOnly = false,
  onSave,
  onEditingChange,
}: WriterIRControlProps) {
  const { t } = useTranslation();
  const [baseDocument, setBaseDocument] = useState(document);
  const [baseSourceRevision, setBaseSourceRevision] = useState(sourceRevision);
  const [draft, setDraft] = useState(document);
  const [history, setHistory] = useState<WriterDocument[]>([]);
  const [future, setFuture] = useState<WriterDocument[]>([]);
  const [mode, setMode] = useState<'view' | 'edit'>('view');
  const [selectedNodeId, setSelectedNodeId] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [discardPrompt, setDiscardPrompt] = useState(false);
  const [externalUpdate, setExternalUpdate] = useState(false);
  const textEditStartRef = useRef<WriterDocument | null>(null);
  const pendingExternalDocumentRef = useRef<{
    document: WriterDocument;
    sourceRevision?: string | number;
  } | null>(null);
  const rootRef = useRef<HTMLElement>(null);
  const toolbarRef = useRef<HTMLElement>(null);
  const documentElementRef = useRef<HTMLElement>(null);

  const dirty = draft !== baseDocument;
  const documentRoot = useMemo(
    () => draft.blocks.find((block) => block.type === 'document'),
    [draft.blocks],
  );
  const documentReadOnly = readOnly || documentRoot?.editable === false || !onSave;
  const editorLocked = documentReadOnly || saving;
  const blockCount = useMemo(() => countWriterBlocks(draft.blocks), [draft.blocks]);
  const stageLabel = t(`chat.writerIR.stages.${draft.stage}`, {
    defaultValue: draft.stage,
  });

  const focusToolbar = useCallback(() => {
    window.requestAnimationFrame(() => {
      toolbarRef.current
        ?.querySelector<HTMLButtonElement>('button:not(:disabled)')
        ?.focus();
    });
  }, []);

  const focusBlockEditor = useCallback((nodeId: string) => {
    window.requestAnimationFrame(() => {
      const block = Array.from(
        documentElementRef.current?.querySelectorAll<HTMLElement>('[data-node-id]') ?? [],
      ).find((element) => element.dataset.nodeId === nodeId);
      block?.querySelector<HTMLTextAreaElement>('textarea')?.focus();
    });
  }, []);

  useEffect(() => {
    const sourceMatchesBase = sourceRevision !== undefined || baseSourceRevision !== undefined
      ? sourceRevision === baseSourceRevision
      : document === baseDocument;
    if (sourceMatchesBase) {
      if (draft !== baseDocument) {
        pendingExternalDocumentRef.current = null;
        setExternalUpdate(false);
      }
      return;
    }
    if (draft === baseDocument) {
      pendingExternalDocumentRef.current = null;
      setBaseDocument(document);
      setBaseSourceRevision(sourceRevision);
      setDraft(document);
      setHistory([]);
      setFuture([]);
      setExternalUpdate(false);
      return;
    }
    pendingExternalDocumentRef.current = { document, sourceRevision };
    setExternalUpdate(true);
    // Intentionally do not depend on local edit state; incoming snapshots must never
    // overwrite a dirty draft.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [document, sourceRevision]);

  useEffect(() => {
    const pending = pendingExternalDocumentRef.current;
    if (dirty || !pending) return;
    pendingExternalDocumentRef.current = null;
    setBaseDocument(pending.document);
    setBaseSourceRevision(pending.sourceRevision);
    setDraft(pending.document);
    setHistory([]);
    setFuture([]);
    setExternalUpdate(false);
  }, [dirty]);

  useEffect(() => {
    const editing = mode === 'edit' || dirty;
    onEditingChange?.(editing);
  }, [dirty, mode, onEditingChange]);

  useEffect(
    () => () => onEditingChange?.(false),
    [onEditingChange],
  );

  useEffect(() => {
    if (!dirty) return undefined;
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [dirty]);

  const commit = useCallback((next: WriterDocument, nextSelectedId?: string) => {
    if (next === draft) return;
    setHistory((current) => [...current, draft]);
    setFuture([]);
    setDraft(next);
    if (nextSelectedId !== undefined) setSelectedNodeId(nextSelectedId);
    setSaveError(undefined);
  }, [draft]);

  const beginTextEdit = useCallback(() => {
    if (!textEditStartRef.current) textEditStartRef.current = draft;
  }, [draft]);

  const finishTextEdit = useCallback(() => {
    const start = textEditStartRef.current;
    textEditStartRef.current = null;
    if (start && start !== draft) {
      setHistory((current) => [...current, start]);
      setFuture([]);
    }
  }, [draft]);

  const handleUndo = useCallback(() => {
    if (saving) return;
    if (textEditStartRef.current) {
      setFuture((current) => [draft, ...current]);
      setDraft(textEditStartRef.current);
      textEditStartRef.current = null;
      setSaveError(undefined);
      return;
    }
    const previous = history[history.length - 1];
    if (!previous) return;
    setHistory(history.slice(0, -1));
    setFuture((current) => [draft, ...current]);
    setDraft(previous);
    setSaveError(undefined);
  }, [draft, history, saving]);

  const handleRedo = useCallback(() => {
    if (saving) return;
    const next = future[0];
    if (!next) return;
    setFuture(future.slice(1));
    setHistory((current) => [...current, draft]);
    setDraft(next);
    setSaveError(undefined);
  }, [draft, future, saving]);

  const handleSave = useCallback(async () => {
    if (!onSave || !dirty || saving || documentReadOnly) return;
    finishTextEdit();
    setSaving(true);
    setSaveError(undefined);
    try {
      await onSave(draft);
      pendingExternalDocumentRef.current = null;
      setBaseDocument(draft);
      setHistory([]);
      setFuture([]);
      setExternalUpdate(false);
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : t('chat.writerIR.saveFailed'));
    } finally {
      setSaving(false);
    }
  }, [dirty, documentReadOnly, draft, finishTextEdit, onSave, saving, t]);

  useEffect(() => {
    if (mode !== 'edit') return undefined;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (
        !(event.target instanceof Node)
        || !rootRef.current?.contains(event.target)
      ) return;
      if (!(event.metaKey || event.ctrlKey)) return;
      const key = event.key.toLowerCase();
      if (key === 's') {
        event.preventDefault();
        void handleSave();
      } else if (key === 'z' && event.shiftKey) {
        event.preventDefault();
        handleRedo();
      } else if (key === 'z') {
        event.preventDefault();
        handleUndo();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [handleRedo, handleSave, handleUndo, mode]);

  const handleDelete = useCallback((block: WriterBlock) => {
    if ((block.children?.length ?? 0) > 0 && !window.confirm(t('chat.writerIR.deleteSubtree'))) return;
    const next = deleteWriterBlock(draft, block.node_id);
    commit(next);
    if (next !== draft) {
      setSelectedNodeId(undefined);
      focusToolbar();
    }
  }, [commit, draft, focusToolbar, t]);

  const renderProps: Omit<BlockRenderProps, 'blocks'> = {
    mode,
    selectedNodeId,
    documentReadOnly: editorLocked,
    onSelect: setSelectedNodeId,
    onContentChange: (block, content) => {
      if (block.editable === false || editorLocked) return;
      setDraft((current) => {
        if (!textEditStartRef.current) textEditStartRef.current = current;
        return updateWriterBlockContent(current, block.node_id, content);
      });
      setFuture([]);
      setSaveError(undefined);
    },
    onTextFocus: beginTextEdit,
    onTextBlur: finishTextEdit,
    onInsertAfter: (nodeId) => {
      const result = insertWriterParagraphAfter(draft, nodeId);
      commit(result.document, result.insertedNodeId);
      if (result.insertedNodeId) focusBlockEditor(result.insertedNodeId);
    },
    onDelete: handleDelete,
    onMove: (nodeId, direction) => commit(moveWriterBlock(draft, nodeId, direction), nodeId),
  };

  const enterViewMode = () => {
    if (dirty) {
      setDiscardPrompt(true);
      return;
    }
    setMode('view');
    setSelectedNodeId(undefined);
    focusToolbar();
  };

  const enterEditMode = () => {
    setMode('edit');
    window.requestAnimationFrame(() => {
      documentElementRef.current
        ?.querySelector<HTMLInputElement>('.writer-ir__title-editor')
        ?.focus();
    });
  };

  const discardChanges = () => {
    const pending = pendingExternalDocumentRef.current;
    const nextDocument = pending?.document ?? baseDocument;
    pendingExternalDocumentRef.current = null;
    textEditStartRef.current = null;
    setBaseDocument(nextDocument);
    if (pending) setBaseSourceRevision(pending.sourceRevision);
    setDraft(nextDocument);
    setHistory([]);
    setFuture([]);
    setSelectedNodeId(undefined);
    setDiscardPrompt(false);
    setSaveError(undefined);
    setExternalUpdate(false);
    setMode('view');
    focusToolbar();
  };

  const selectedBlock = selectedNodeId
    ? findWriterBlock(draft.blocks, selectedNodeId)
    : undefined;

  return (
    <section
      className='writer-ir'
      aria-label={t('chat.writerIR.documentRegion')}
      ref={rootRef}
    >
      <header className='writer-ir__toolbar' ref={toolbarRef}>
        <div className='writer-ir__document-meta'>
          <span>{t('chat.writerIR.stage', { stage: stageLabel })}</span>
          <span>{t('chat.writerIR.blockCount', { count: blockCount })}</span>
          {dirty && <strong>{t('chat.writerIR.unsaved')}</strong>}
        </div>
        <div className='writer-ir__toolbar-actions'>
          {mode === 'view' ? (
            !documentReadOnly && (
              <button type='button' onClick={enterEditMode}>
                {t('common.edit')}
              </button>
            )
          ) : (
            <>
              <button
                type='button'
                onClick={handleUndo}
                disabled={saving || (!history.length && !textEditStartRef.current)}
              >
                {t('chat.writerIR.undo')}
              </button>
              <button type='button' onClick={handleRedo} disabled={saving || !future.length}>
                {t('chat.writerIR.redo')}
              </button>
              <button type='button' onClick={enterViewMode} disabled={saving}>
                {t('common.cancel')}
              </button>
              <button
                type='button'
                onClick={() => void handleSave()}
                disabled={!dirty || saving || documentReadOnly}
              >
                {saving ? t('chat.writerIR.saving') : t('common.save')}
              </button>
            </>
          )}
        </div>
      </header>

      {discardPrompt && (
        <div className='writer-ir__notice writer-ir__notice--warning' role='alert'>
          <span>{t('chat.writerIR.discardPrompt')}</span>
          <div>
            <button
              type='button'
              onClick={() => {
                setDiscardPrompt(false);
                focusToolbar();
              }}
            >
              {t('chat.writerIR.keepEditing')}
            </button>
            <button type='button' onClick={discardChanges}>{t('chat.writerIR.discard')}</button>
          </div>
        </div>
      )}
      {externalUpdate && (
        <div className='writer-ir__notice writer-ir__notice--warning' role='status'>
          {t('chat.writerIR.externalUpdate')}
        </div>
      )}
      {saveError && (
        <div className='writer-ir__notice writer-ir__notice--error' role='alert'>
          {t('chat.writerIR.saveError', { error: saveError })}
        </div>
      )}

      <div className='writer-ir__save-status' aria-live='polite' aria-atomic='true'>
        {saving
          ? t('chat.writerIR.saving')
          : dirty
            ? t('chat.writerIR.unsaved')
            : t('chat.writerIR.saved')}
      </div>

      <article className='writer-ir__document' ref={documentElementRef}>
        {mode === 'edit' && !editorLocked ? (
          <input
            className='writer-ir__title-editor'
            value={draft.title}
            aria-label={t('chat.writerIR.editTitle')}
            onFocus={beginTextEdit}
            onBlur={finishTextEdit}
            onChange={(event) => {
              const title = event.target.value;
              setDraft((current) => {
                if (!textEditStartRef.current) textEditStartRef.current = current;
                return updateWriterDocumentTitle(current, title);
              });
              setFuture([]);
              setSaveError(undefined);
            }}
          />
        ) : (
          <h1 className='writer-ir__title'>{draft.title}</h1>
        )}

        {draft.blocks.length > 0 ? (
          <BlockSequence blocks={draft.blocks} {...renderProps} />
        ) : (
          <div className='writer-ir__empty' role='status'>
            {t('chat.writerIR.emptyDocument')}
          </div>
        )}
      </article>

      {mode === 'edit' && selectedBlock && (
        <div className='writer-ir__selection-status' aria-live='polite'>
          {t('chat.writerIR.selectedBlock', { type: selectedBlock.type })}
        </div>
      )}
    </section>
  );
}
