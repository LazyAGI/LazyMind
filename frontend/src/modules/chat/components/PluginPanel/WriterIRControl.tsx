import {
  createContext,
  createElement,
  Fragment,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import {
  countWriterBlocks,
  getWriterSpanStyles,
  type WriterBlock,
  type WriterDocument,
  type WriterSpan,
} from './writerIR';
import { WriterIRDocumentEditor } from './WriterIRDocumentEditor';
import './WriterIRControl.scss';

const WRITER_IR_AUTOSAVE_DELAY_MS = 2_000;

export const WriterIRToolbarTargetContext = createContext<HTMLElement | null | undefined>(
  undefined,
);

function sameWriterDocument(left: WriterDocument, right: WriterDocument): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

export interface WriterIRControlProps {
  document: WriterDocument;
  sourceRevision?: string | number;
  readOnly?: boolean;
  onSave?: (document: WriterDocument) => Promise<void>;
  onEditingChange?: (editing: boolean) => void;
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
  children,
}: { block: WriterBlock; children?: ReactNode }) {
  return (
    <div
      className='writer-ir__block'
      data-node-id={block.node_id}
      data-node-type={block.type}
    >
      <PreviewBlockContent block={block} />
      {children}
    </div>
  );
}

function ListItemBlock({ block }: { block: WriterBlock }) {
  return (
    <li className='writer-ir__list-item'>
      <BlockShell block={block}>
        {(block.children?.length ?? 0) > 0 && (
          <BlockSequence blocks={block.children ?? []} />
        )}
      </BlockShell>
    </li>
  );
}

function BlockSequence({ blocks }: { blocks: WriterBlock[] }) {
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
            <ListItemBlock key={item.node_id} block={item} />
          ))}
        </ListTag>,
      );
      continue;
    }

    index += 1;
    if (block.type === 'document') {
      rendered.push(
        <section className='writer-ir__document-root' key={block.node_id}>
          <BlockSequence blocks={block.children ?? []} />
        </section>,
      );
      continue;
    }
    rendered.push(
      <BlockShell block={block} key={block.node_id}>
        {(block.children?.length ?? 0) > 0 && (
          <div className='writer-ir__children'>
            <BlockSequence blocks={block.children ?? []} />
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
  const toolbarTarget = useContext(WriterIRToolbarTargetContext);
  const [baseDocument, setBaseDocument] = useState(document);
  const [baseSourceRevision, setBaseSourceRevision] = useState(sourceRevision);
  const [draft, setDraft] = useState(document);
  const [history, setHistory] = useState<WriterDocument[]>([]);
  const [future, setFuture] = useState<WriterDocument[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [externalUpdate, setExternalUpdate] = useState(false);
  const textEditStartRef = useRef<WriterDocument | null>(null);
  const pendingExternalDocumentRef = useRef<{
    document: WriterDocument;
    sourceRevision?: string | number;
  } | null>(null);
  const rootRef = useRef<HTMLElement>(null);
  const toolbarRef = useRef<HTMLElement>(null);
  const autoSaveTimerRef = useRef<number | undefined>(undefined);
  const mountedRef = useRef(true);
  const draftRef = useRef(draft);
  const baseDocumentRef = useRef(baseDocument);
  const lastSavedDocumentRef = useRef<WriterDocument | undefined>(undefined);
  const saveInFlightRef = useRef(false);
  const saveQueuedRef = useRef(false);
  const saveRunnerRef = useRef<() => Promise<void>>(async () => undefined);
  const onSaveRef = useRef(onSave);
  const historyRef = useRef(history);
  const futureRef = useRef(future);

  const dirty = draft !== baseDocument;
  const documentRoot = useMemo(
    () => draft.blocks.find((block) => block.type === 'document'),
    [draft.blocks],
  );
  const documentReadOnly = readOnly || documentRoot?.editable === false || !onSave;
  const blockCount = useMemo(() => countWriterBlocks(draft.blocks), [draft.blocks]);
  const stageLabel = t(`chat.writerIR.stages.${draft.stage}`, {
    defaultValue: draft.stage,
  });

  draftRef.current = draft;
  baseDocumentRef.current = baseDocument;
  onSaveRef.current = onSave;
  historyRef.current = history;
  futureRef.current = future;

  const focusToolbar = useCallback(() => {
    window.requestAnimationFrame(() => {
      toolbarRef.current
        ?.querySelector<HTMLButtonElement>('button:not(:disabled)')
        ?.focus();
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
    const savedDocument = lastSavedDocumentRef.current;
    if (
      sameWriterDocument(document, baseDocument)
      || (savedDocument && sameWriterDocument(document, savedDocument))
    ) {
      setBaseSourceRevision(sourceRevision);
      return;
    }
    if (draft === baseDocument) {
      pendingExternalDocumentRef.current = null;
      setBaseDocument(document);
      baseDocumentRef.current = document;
      setBaseSourceRevision(sourceRevision);
      setDraft(document);
      draftRef.current = document;
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
    onEditingChange?.(dirty || saving);
  }, [dirty, onEditingChange, saving]);

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

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (autoSaveTimerRef.current !== undefined) {
        window.clearTimeout(autoSaveTimerRef.current);
      }
    };
  }, []);

  const beginTextEdit = useCallback(() => {
    if (!textEditStartRef.current) textEditStartRef.current = draft;
  }, [draft]);

  const finishTextEdit = useCallback(() => {
    const start = textEditStartRef.current;
    textEditStartRef.current = null;
    if (start && start !== draft) {
      const nextHistory = [...historyRef.current, start];
      historyRef.current = nextHistory;
      futureRef.current = [];
      setHistory(nextHistory);
      setFuture([]);
    }
  }, [draft]);

  const handleUndo = useCallback(() => {
    if (textEditStartRef.current) {
      const nextFuture = [draft, ...futureRef.current];
      futureRef.current = nextFuture;
      setFuture(nextFuture);
      setDraft(textEditStartRef.current);
      textEditStartRef.current = null;
      setSaveError(undefined);
      return;
    }
    const currentHistory = historyRef.current;
    const previous = currentHistory[currentHistory.length - 1];
    if (!previous) return;
    const nextHistory = currentHistory.slice(0, -1);
    const nextFuture = [draft, ...futureRef.current];
    historyRef.current = nextHistory;
    futureRef.current = nextFuture;
    setHistory(nextHistory);
    setFuture(nextFuture);
    setDraft(previous);
    setSaveError(undefined);
  }, [draft]);

  const handleRedo = useCallback(() => {
    const currentFuture = futureRef.current;
    const next = currentFuture[0];
    if (!next) return;
    const nextFuture = currentFuture.slice(1);
    const nextHistory = [...historyRef.current, draft];
    futureRef.current = nextFuture;
    historyRef.current = nextHistory;
    setFuture(nextFuture);
    setHistory(nextHistory);
    setDraft(next);
    setSaveError(undefined);
  }, [draft]);

  const runSave = useCallback(async () => {
    const saveDocument = onSaveRef.current;
    if (!saveDocument || documentReadOnly) return;
    if (saveInFlightRef.current) {
      saveQueuedRef.current = true;
      return;
    }

    const snapshot = draftRef.current;
    if (snapshot === baseDocumentRef.current) return;
    saveInFlightRef.current = true;
    saveQueuedRef.current = false;
    if (mountedRef.current) {
      setSaving(true);
      setSaveError(undefined);
    }
    let saved = false;
    try {
      await saveDocument(snapshot);
      if (!mountedRef.current) return;
      saved = true;
      lastSavedDocumentRef.current = snapshot;
      baseDocumentRef.current = snapshot;
      pendingExternalDocumentRef.current = null;
      setBaseDocument(snapshot);
      setExternalUpdate(false);
    } catch (error) {
      if (mountedRef.current) {
        setSaveError(error instanceof Error ? error.message : t('chat.writerIR.saveFailed'));
      }
    } finally {
      saveInFlightRef.current = false;
      if (mountedRef.current) setSaving(false);
      const hasNewerDraft = draftRef.current !== snapshot;
      if (saved && (saveQueuedRef.current || hasNewerDraft)) {
        saveQueuedRef.current = false;
        window.setTimeout(() => void saveRunnerRef.current(), 0);
      }
    }
  }, [documentReadOnly, t]);

  saveRunnerRef.current = runSave;

  const requestImmediateSave = useCallback(() => {
    if (autoSaveTimerRef.current !== undefined) {
      window.clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = undefined;
    }
    void saveRunnerRef.current();
  }, []);

  useEffect(() => {
    if (autoSaveTimerRef.current !== undefined) {
      window.clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = undefined;
    }
    if (!dirty || documentReadOnly || saveError || externalUpdate) return undefined;
    autoSaveTimerRef.current = window.setTimeout(() => {
      autoSaveTimerRef.current = undefined;
      void saveRunnerRef.current();
    }, WRITER_IR_AUTOSAVE_DELAY_MS);
    return () => {
      if (autoSaveTimerRef.current !== undefined) {
        window.clearTimeout(autoSaveTimerRef.current);
        autoSaveTimerRef.current = undefined;
      }
    };
  }, [dirty, documentReadOnly, draft, externalUpdate, saveError]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (
        !(event.target instanceof Node)
        || !rootRef.current?.contains(event.target)
      ) return;
      if (!(event.metaKey || event.ctrlKey)) return;
      const key = event.key.toLowerCase();
      if (key === 's') {
        event.preventDefault();
        requestImmediateSave();
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
  }, [handleRedo, handleUndo, requestImmediateSave]);

  const handleTextBlur = useCallback(() => {
    finishTextEdit();
    if (!externalUpdate) requestImmediateSave();
  }, [externalUpdate, finishTextEdit, requestImmediateSave]);

  const handleDocumentChange = useCallback((nextDocument: WriterDocument) => {
    setDraft((current) => {
      if (!textEditStartRef.current) textEditStartRef.current = current;
      return nextDocument;
    });
    futureRef.current = [];
    setFuture([]);
    setSaveError(undefined);
  }, []);

  const discardChanges = () => {
    const pending = pendingExternalDocumentRef.current;
    const nextDocument = pending?.document ?? baseDocument;
    pendingExternalDocumentRef.current = null;
    textEditStartRef.current = null;
    baseDocumentRef.current = nextDocument;
    draftRef.current = nextDocument;
    setBaseDocument(nextDocument);
    if (pending) setBaseSourceRevision(pending.sourceRevision);
    setDraft(nextDocument);
    setHistory([]);
    setFuture([]);
    setSaveError(undefined);
    setExternalUpdate(false);
    focusToolbar();
  };

  const saveLocalVersion = () => {
    pendingExternalDocumentRef.current = null;
    setExternalUpdate(false);
    setSaveError(undefined);
    requestImmediateSave();
  };

  const saveStatus = saving
    ? t('chat.writerIR.saving')
    : dirty
      ? t('chat.writerIR.unsaved')
      : t('chat.writerIR.saved');

  const toolbar = (
    <header
      className={`writer-ir__toolbar${toolbarTarget !== undefined ? ' writer-ir__toolbar--external' : ''}`}
      ref={toolbarRef}
    >
      <div className='writer-ir__document-meta'>
        <span>{t('chat.writerIR.stage', { stage: stageLabel })}</span>
        <span>{t('chat.writerIR.blockCount', { count: blockCount })}</span>
        <strong
          className={`writer-ir__autosave-status${dirty ? ' writer-ir__autosave-status--dirty' : ''}`}
          aria-live='polite'
          aria-atomic='true'
        >
          {saveStatus}
        </strong>
      </div>
      <div className='writer-ir__toolbar-actions'>
        {!documentReadOnly && (
          <>
            <button
              type='button'
              onMouseDown={(event) => event.preventDefault()}
              onClick={handleUndo}
              disabled={!history.length && !textEditStartRef.current}
            >
              {t('chat.writerIR.undo')}
            </button>
            <button
              type='button'
              onMouseDown={(event) => event.preventDefault()}
              onClick={handleRedo}
              disabled={!future.length}
            >
              {t('chat.writerIR.redo')}
            </button>
          </>
        )}
      </div>
    </header>
  );

  return (
    <section
      className='writer-ir'
      aria-label={t('chat.writerIR.documentRegion')}
      ref={rootRef}
    >
      {toolbarTarget === undefined
        ? toolbar
        : toolbarTarget
          ? createPortal(toolbar, toolbarTarget)
          : null}

      {externalUpdate && (
        <div className='writer-ir__notice writer-ir__notice--warning' role='alert'>
          <span>{t('chat.writerIR.externalUpdate')}</span>
          <div>
            <button type='button' onClick={saveLocalVersion}>{t('common.save')}</button>
            <button type='button' onClick={discardChanges}>{t('chat.writerIR.discard')}</button>
          </div>
        </div>
      )}
      {saveError && (
        <div className='writer-ir__notice writer-ir__notice--error' role='alert'>
          <span>{t('chat.writerIR.saveError', { error: saveError })}</span>
          <div>
            <button type='button' onClick={requestImmediateSave}>{t('common.retry')}</button>
            <button type='button' onClick={discardChanges}>{t('chat.writerIR.discard')}</button>
          </div>
        </div>
      )}

      {documentReadOnly ? (
        <article className='writer-ir__document'>
          <h1 className='writer-ir__title'>{draft.title}</h1>
          {draft.blocks.length > 0 ? (
            <BlockSequence blocks={draft.blocks} />
          ) : (
            <div className='writer-ir__empty' role='status'>
              {t('chat.writerIR.emptyDocument')}
            </div>
          )}
        </article>
      ) : (
        <WriterIRDocumentEditor
          document={draft}
          ariaLabel={t('chat.writerIR.documentRegion')}
          onChange={handleDocumentChange}
          onFocus={beginTextEdit}
          onBlur={handleTextBlur}
        />
      )}
    </section>
  );
}
