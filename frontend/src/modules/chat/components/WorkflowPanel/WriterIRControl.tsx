import {
  createElement,
  Fragment,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import {
  getWriterSpanStyles,
  normalizeWriterCodeLanguage,
  repairWriterCodeToolbarPollution,
  sameWriterDocument,
  sameWriterDocumentForSync,
  type WriterBlock,
  type WriterDocument,
  type WriterSpan,
} from './writerIR';
import {
  WriterIRDocumentEditor,
  type WriterIRRewritePreview,
  type WriterIRRewriteSelection,
} from './WriterIRDocumentEditor';
import { ArtifactRewriteSelectionAction } from './ArtifactRewriteSelectionAction';
import { ArtifactRewriteSelectionHighlight } from './ArtifactRewriteSelectionHighlight';
import {
  selectionActionAnchor,
  type SelectionActionAnchor,
} from './artifactRewriteSelection';
import { highlightCode } from '../MarkdownViewer/syntaxHighlight';
import { SlotEditingContext } from './slotEditingContext';
import './WriterIRControl.scss';

/** Idle debounce after the latest edit before draft autosave. */
const WRITER_IR_AUTOSAVE_IDLE_MS = 3_000;
/** Max time a dirty draft can wait before a draft save is forced. */
const WRITER_IR_AUTOSAVE_MAX_WAIT_MS = 15_000;
/** Coalesce follow-up saves after an in-flight request finishes. */
const WRITER_IR_SAVE_FOLLOWUP_MS = 400;

type WriterIRSaveRunResult = 'noop' | 'saved' | 'error' | 'busy';
export type WriterIRSaveMode = 'draft' | 'checkpoint';

export interface WriterIRControlProps {
  document: WriterDocument;
  sourceRevision?: string | number;
  readOnly?: boolean;
  /** Stable key used to register flush-before-retry with WorkflowPanel. */
  editingKey?: string;
  /**
   * When true (draft_document), local Save / flush never create a version.
   * Versions are created only by successful Feishu write-back.
   */
  feishuVersionOnly?: boolean;
  onSave?: (
    sourceDocument: WriterDocument,
    revisedDocument: WriterDocument,
    sourceRevision?: string | number,
    mode?: WriterIRSaveMode,
  ) => Promise<WriterIRSaveResult | void>;
  onEditingChange?: (editing: boolean) => void;
  /** Reports the current draft so the write-back action can compare it with its Feishu baseline. */
  onDocumentChange?: (document: WriterDocument) => void;
  onRewriteSelection?: (selection: WriterIRRewriteSelection) => void;
  rewriteDialogOpen?: boolean;
  rewritePreview?: WriterIRRewritePreview | null;
  onRewritePreviewApplied?: (revision?: number) => void;
  onRewritePreviewRejected?: () => void;
}

export interface WriterIRSaveResult {
  document: WriterDocument;
  sourceRevision?: string | number;
}

function asHeadingLevel(block: WriterBlock): 1 | 2 | 3 | 4 | 5 | 6 {
  const raw = Number(block.numbering?.level ?? 2);
  if (!Number.isFinite(raw)) return 2;
  return Math.min(6, Math.max(1, Math.trunc(raw))) as 1 | 2 | 3 | 4 | 5 | 6;
}

function hasEditableWriterBlock(blocks: WriterBlock[]): boolean {
  return blocks.some(
    (block) => block.editable !== false || hasEditableWriterBlock(block.children ?? []),
  );
}

function renderMarkedText(text: string, styles: string[], key: string) {
  let content = <Fragment>{text}</Fragment>;
  if (styles.includes('code')) content = <code>{content}</code>;
  if (styles.includes('strong') || styles.includes('bold')) content = <strong>{content}</strong>;
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
    const language = normalizeWriterCodeLanguage(block.language);
    const highlighted = highlightCode(block.content ?? '', language);
    return (
      <div className='writer-ir__code-shell'>
        <div className='writer-ir__code-header'>{language}</div>
        <pre className='writer-ir__code'>
          {highlighted ? (
            <code
              className={`language-${language}`}
              dangerouslySetInnerHTML={{ __html: highlighted }}
            />
          ) : (
            <code><SpanContent block={block} /></code>
          )}
        </pre>
      </div>
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
            <ListItemBlock
              key={item.node_id}
              block={item}
            />
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
      <BlockShell
        block={block}
        key={block.node_id}
      >
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
  editingKey,
  feishuVersionOnly = false,
  onSave,
  onEditingChange,
  onDocumentChange,
  onRewriteSelection,
  rewriteDialogOpen = false,
  rewritePreview,
  onRewritePreviewApplied,
  onRewritePreviewRejected,
}: WriterIRControlProps) {
  const { t } = useTranslation();
  const { registerFlush } = useContext(SlotEditingContext);
  const [baseDocument, setBaseDocument] = useState(document);
  const [baseSourceRevision, setBaseSourceRevision] = useState(sourceRevision);
  const [draft, setDraft] = useState(() => repairWriterCodeToolbarPollution(document));
  const [history, setHistory] = useState<WriterDocument[]>([]);
  const [future, setFuture] = useState<WriterDocument[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [externalUpdate, setExternalUpdate] = useState(false);
  const [readOnlySelection, setReadOnlySelection] = useState<
    (WriterIRRewriteSelection & { anchor: SelectionActionAnchor }) | null
  >(null);
  const [readOnlyRewriteLayer, setReadOnlyRewriteLayer] = useState<HTMLDivElement | null>(null);
  const [readOnlyRewritePinned, setReadOnlyRewritePinned] = useState(false);
  const pinnedReadOnlyRangeRef = useRef<Range | null>(null);
  const textEditStartRef = useRef<WriterDocument | null>(null);
  const pendingExternalDocumentRef = useRef<{
    document: WriterDocument;
    sourceRevision?: string | number;
  } | null>(null);
  const rootRef = useRef<HTMLElement>(null);
  const autoSaveIdleTimerRef = useRef<number | undefined>(undefined);
  const autoSaveMaxTimerRef = useRef<number | undefined>(undefined);
  const saveFollowupTimerRef = useRef<number | undefined>(undefined);
  const dirtyStartedAtRef = useRef<number | null>(null);
  const mountedRef = useRef(true);
  const draftRef = useRef(draft);
  const baseDocumentRef = useRef(baseDocument);
  const baseSourceRevisionRef = useRef(sourceRevision);
  const lastSavedDocumentRef = useRef<WriterDocument | undefined>(undefined);
  /** Content last captured as a versioned revision (Save button / conversation flush). */
  const lastCheckpointDocumentRef = useRef<WriterDocument>(document);
  const saveInFlightRef = useRef(false);
  const saveQueuedRef = useRef(false);
  /** Highest pending save mode; checkpoint wins over draft until consumed. */
  const pendingSaveModeRef = useRef<WriterIRSaveMode>('draft');
  const saveRunnerRef = useRef<() => Promise<WriterIRSaveRunResult>>(async () => 'noop');
  const onSaveRef = useRef(onSave);
  const historyRef = useRef(history);
  const futureRef = useRef(future);

  const clearAutoSaveTimers = useCallback(() => {
    if (autoSaveIdleTimerRef.current !== undefined) {
      window.clearTimeout(autoSaveIdleTimerRef.current);
      autoSaveIdleTimerRef.current = undefined;
    }
    if (autoSaveMaxTimerRef.current !== undefined) {
      window.clearTimeout(autoSaveMaxTimerRef.current);
      autoSaveMaxTimerRef.current = undefined;
    }
    if (saveFollowupTimerRef.current !== undefined) {
      window.clearTimeout(saveFollowupTimerRef.current);
      saveFollowupTimerRef.current = undefined;
    }
  }, []);

  const escalateSaveMode = useCallback((mode: WriterIRSaveMode) => {
    if (mode === 'checkpoint' || pendingSaveModeRef.current !== 'checkpoint') {
      pendingSaveModeRef.current = mode;
    }
  }, []);

  const scheduleFollowupSave = useCallback(() => {
    if (saveFollowupTimerRef.current !== undefined) {
      window.clearTimeout(saveFollowupTimerRef.current);
    }
    saveFollowupTimerRef.current = window.setTimeout(() => {
      saveFollowupTimerRef.current = undefined;
      void saveRunnerRef.current();
    }, WRITER_IR_SAVE_FOLLOWUP_MS);
  }, []);

  const dirty = draft !== baseDocument;
  const documentReadOnly = readOnly || !hasEditableWriterBlock(draft.blocks) || !onSave;

  draftRef.current = draft;
  baseDocumentRef.current = baseDocument;
  baseSourceRevisionRef.current = baseSourceRevision;
  onSaveRef.current = onSave;
  historyRef.current = history;
  futureRef.current = future;

  useEffect(() => {
    const sourceMatchesBase = sourceRevision !== undefined || baseSourceRevision !== undefined
      ? sourceRevision === baseSourceRevision
      : document === baseDocument;
    if (sourceMatchesBase) {
      const documentChanged = !sameWriterDocument(document, baseDocument)
        && !sameWriterDocumentForSync(document, baseDocument);
      if (documentChanged && draft === baseDocument) {
        pendingExternalDocumentRef.current = null;
        setBaseDocument(document);
        baseDocumentRef.current = document;
        lastCheckpointDocumentRef.current = document;
        setDraft(document);
        draftRef.current = document;
        setHistory([]);
        setFuture([]);
        setExternalUpdate(false);
      } else if (draft !== baseDocument) {
        pendingExternalDocumentRef.current = null;
        setExternalUpdate(false);
      }
      return;
    }
    const savedDocument = lastSavedDocumentRef.current;
    if (
      sameWriterDocument(document, baseDocument)
      || sameWriterDocumentForSync(document, baseDocument)
      || (savedDocument && (
        sameWriterDocument(document, savedDocument)
        || sameWriterDocumentForSync(document, savedDocument)
      ))
      // Parent echoed the live draft (common right after save); only advance revision.
      || document === draftRef.current
      || sameWriterDocumentForSync(document, draftRef.current)
    ) {
      setBaseSourceRevision(sourceRevision);
      baseSourceRevisionRef.current = sourceRevision;
      if (
        document === draftRef.current
        || sameWriterDocumentForSync(document, draftRef.current)
      ) {
        baseDocumentRef.current = draftRef.current;
        setBaseDocument(draftRef.current);
      }
      return;
    }
    if (draft === baseDocument) {
      pendingExternalDocumentRef.current = null;
      setBaseDocument(document);
      baseDocumentRef.current = document;
      setBaseSourceRevision(sourceRevision);
      baseSourceRevisionRef.current = sourceRevision;
      lastCheckpointDocumentRef.current = document;
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
    baseSourceRevisionRef.current = pending.sourceRevision;
    setDraft(pending.document);
    setHistory([]);
    setFuture([]);
    setExternalUpdate(false);
  }, [dirty]);

  useEffect(() => {
    // Only surface local dirty state. In-flight saves must not lock parent UI.
    onEditingChange?.(dirty);
  }, [dirty, onEditingChange]);

  useEffect(() => {
    onDocumentChange?.(draft);
  }, [draft, onDocumentChange]);

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
      clearAutoSaveTimers();
    };
  }, [clearAutoSaveTimers]);

  useEffect(() => {
    const current = draftRef.current;
    const repaired = repairWriterCodeToolbarPollution(current);
    if (repaired === current) return;
    draftRef.current = repaired;
    setDraft(repaired);
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

  const runSave = useCallback(async (): Promise<WriterIRSaveRunResult> => {
    const saveDocument = onSaveRef.current;
    if (!saveDocument || documentReadOnly) return 'noop';
    if (saveInFlightRef.current) {
      // Keep editing; coalesce into one follow-up with the latest draft.
      saveQueuedRef.current = true;
      return 'busy';
    }

    const snapshot = draftRef.current;
    const saveMode = pendingSaveModeRef.current;
    const sameAsBase = snapshot === baseDocumentRef.current
      || sameWriterDocumentForSync(snapshot, baseDocumentRef.current);
    const sameAsCheckpoint = sameWriterDocumentForSync(
      snapshot,
      lastCheckpointDocumentRef.current,
    );
    // Draft only when dirty. Checkpoint (Save / conversation flush) may still
    // run after draft autosave when content is not yet versioned.
    if (sameAsCheckpoint || (sameAsBase && saveMode !== 'checkpoint')) {
      pendingSaveModeRef.current = 'draft';
      return 'noop';
    }
    pendingSaveModeRef.current = 'draft';
    clearAutoSaveTimers();
    saveInFlightRef.current = true;
    saveQueuedRef.current = false;
    const previousRevision = baseSourceRevisionRef.current;
    // Mark the in-flight snapshot so parent prop echoes during save do not look
    // like external updates.
    const previousSavedDocument = lastSavedDocumentRef.current;
    lastSavedDocumentRef.current = snapshot;
    if (mountedRef.current) {
      setSaving(true);
      setSaveError(undefined);
    }
    let saved = false;
    let hasNewerDraft = false;
    try {
      const result = await saveDocument(
        baseDocumentRef.current,
        snapshot,
        baseSourceRevisionRef.current,
        saveMode,
      );
      if (!mountedRef.current) return 'saved';
      saved = true;
      // If the user edited while this request was pending, keep their draft
      // and only advance the persisted base / revision.
      hasNewerDraft = draftRef.current !== snapshot;
      const savedDocument = result?.document ?? snapshot;
      const savedSourceRevision = result?.sourceRevision ?? baseSourceRevisionRef.current;
      lastSavedDocumentRef.current = savedDocument;
      baseSourceRevisionRef.current = savedSourceRevision;
      pendingExternalDocumentRef.current = null;
      setBaseSourceRevision(savedSourceRevision);
      // Versioned save becomes the checkpoint baseline. For Feishu-only
      // versioning, every successful local persist is the flush baseline.
      if (
        feishuVersionOnly
        || saveMode === 'checkpoint'
        || savedSourceRevision !== previousRevision
      ) {
        lastCheckpointDocumentRef.current = sameWriterDocumentForSync(snapshot, savedDocument)
          ? snapshot
          : savedDocument;
      }
      if (hasNewerDraft) {
        // Keep the live draft; only advance the persisted base/revision.
        baseDocumentRef.current = savedDocument;
        setBaseDocument(savedDocument);
        dirtyStartedAtRef.current = Date.now();
      } else if (sameWriterDocumentForSync(snapshot, savedDocument)) {
        // Semantically unchanged: keep the current draft reference so the
        // contentEditable DOM is not rewritten after save.
        baseDocumentRef.current = snapshot;
        setBaseDocument(snapshot);
        lastSavedDocumentRef.current = snapshot;
        dirtyStartedAtRef.current = null;
      } else {
        // Server normalized the document; adopt it and let the editor sync.
        baseDocumentRef.current = savedDocument;
        draftRef.current = savedDocument;
        setBaseDocument(savedDocument);
        setDraft(savedDocument);
        dirtyStartedAtRef.current = null;
      }
      setExternalUpdate(false);
      return 'saved';
    } catch (error) {
      lastSavedDocumentRef.current = previousSavedDocument;
      if (mountedRef.current) {
        setSaveError(error instanceof Error ? error.message : t('chat.writerIR.saveFailed'));
      }
      return 'error';
    } finally {
      saveInFlightRef.current = false;
      if (mountedRef.current) setSaving(false);
      if (saved && (saveQueuedRef.current || hasNewerDraft)) {
        saveQueuedRef.current = false;
        scheduleFollowupSave();
      }
    }
  }, [clearAutoSaveTimers, documentReadOnly, feishuVersionOnly, scheduleFollowupSave, t]);

  saveRunnerRef.current = runSave;

  /** Ctrl/Cmd+S and error retry: persist draft only (no new version). */
  const requestDraftSave = useCallback(() => {
    escalateSaveMode('draft');
    clearAutoSaveTimers();
    void saveRunnerRef.current();
  }, [clearAutoSaveTimers, escalateSaveMode]);

  /** Explicit Save button: create a versioned checkpoint. */
  const requestCheckpointSave = useCallback(() => {
    escalateSaveMode(feishuVersionOnly ? 'draft' : 'checkpoint');
    clearAutoSaveTimers();
    void saveRunnerRef.current();
  }, [clearAutoSaveTimers, escalateSaveMode, feishuVersionOnly]);

  /** Flush before conversation actions (continue/retry → chat): version the draft. */
  const flushPendingSave = useCallback(async (): Promise<boolean> => {
    if (documentReadOnly || !onSaveRef.current) return true;
    escalateSaveMode(feishuVersionOnly ? 'draft' : 'checkpoint');
    clearAutoSaveTimers();
    if (saveFollowupTimerRef.current !== undefined) {
      window.clearTimeout(saveFollowupTimerRef.current);
      saveFollowupTimerRef.current = undefined;
    }

    for (let attempt = 0; attempt < 8; attempt += 1) {
      while (saveInFlightRef.current) {
        await new Promise<void>((resolve) => {
          window.setTimeout(resolve, 40);
        });
      }
      if (saveFollowupTimerRef.current !== undefined) {
        window.clearTimeout(saveFollowupTimerRef.current);
        saveFollowupTimerRef.current = undefined;
      }
      // Skip only when this content is already a versioned checkpoint.
      if (sameWriterDocumentForSync(draftRef.current, lastCheckpointDocumentRef.current)) {
        return true;
      }

      escalateSaveMode(feishuVersionOnly ? 'draft' : 'checkpoint');
      const result = await runSave();
      if (result === 'error') return false;
      if (result === 'noop') return true;
    }
    return sameWriterDocumentForSync(draftRef.current, lastCheckpointDocumentRef.current);
  }, [clearAutoSaveTimers, documentReadOnly, escalateSaveMode, feishuVersionOnly, runSave]);

  useEffect(() => {
    if (!editingKey) return undefined;
    return registerFlush(editingKey, flushPendingSave);
  }, [editingKey, flushPendingSave, registerFlush]);

  useEffect(() => {
    if (autoSaveIdleTimerRef.current !== undefined) {
      window.clearTimeout(autoSaveIdleTimerRef.current);
      autoSaveIdleTimerRef.current = undefined;
    }
    if (!dirty || documentReadOnly || saveError || externalUpdate || saving) {
      if (!dirty) {
        dirtyStartedAtRef.current = null;
        if (autoSaveMaxTimerRef.current !== undefined) {
          window.clearTimeout(autoSaveMaxTimerRef.current);
          autoSaveMaxTimerRef.current = undefined;
        }
      }
      return undefined;
    }

    if (dirtyStartedAtRef.current === null) {
      dirtyStartedAtRef.current = Date.now();
    }

    autoSaveIdleTimerRef.current = window.setTimeout(() => {
      autoSaveIdleTimerRef.current = undefined;
      escalateSaveMode('draft');
      void saveRunnerRef.current();
    }, WRITER_IR_AUTOSAVE_IDLE_MS);

    if (autoSaveMaxTimerRef.current === undefined) {
      const elapsed = Date.now() - (dirtyStartedAtRef.current ?? Date.now());
      const remaining = Math.max(0, WRITER_IR_AUTOSAVE_MAX_WAIT_MS - elapsed);
      autoSaveMaxTimerRef.current = window.setTimeout(() => {
        autoSaveMaxTimerRef.current = undefined;
        escalateSaveMode('draft');
        void saveRunnerRef.current();
      }, remaining);
    }

    return () => {
      if (autoSaveIdleTimerRef.current !== undefined) {
        window.clearTimeout(autoSaveIdleTimerRef.current);
        autoSaveIdleTimerRef.current = undefined;
      }
    };
  }, [dirty, documentReadOnly, draft, escalateSaveMode, externalUpdate, saveError, saving]);

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
        requestDraftSave();
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
  }, [handleRedo, handleUndo, requestDraftSave]);

  // Blur only closes the text-edit history group; autosave idle/max timers persist.
  const handleTextBlur = useCallback(() => {
    finishTextEdit();
  }, [finishTextEdit]);

  const handleDocumentChange = useCallback((nextDocument: WriterDocument) => {
    draftRef.current = nextDocument;
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
    lastCheckpointDocumentRef.current = nextDocument;
    setBaseDocument(nextDocument);
    if (pending) {
      baseSourceRevisionRef.current = pending.sourceRevision;
      setBaseSourceRevision(pending.sourceRevision);
    }
    setDraft(nextDocument);
    setHistory([]);
    setFuture([]);
    setSaveError(undefined);
    setExternalUpdate(false);
  };

  const saveLocalVersion = () => {
    const pending = pendingExternalDocumentRef.current;
    pendingExternalDocumentRef.current = null;
    if (pending) {
      baseDocumentRef.current = pending.document;
      baseSourceRevisionRef.current = pending.sourceRevision;
      setBaseDocument(pending.document);
      setBaseSourceRevision(pending.sourceRevision);
    }
    setExternalUpdate(false);
    setSaveError(undefined);
    requestCheckpointSave();
  };

  const recordReadOnlySelection = useCallback(() => {
    const root = rootRef.current;
    const selection = globalThis.getSelection();
    if (!root || !selection || selection.rangeCount === 0 || selection.isCollapsed) {
      setReadOnlySelection(null);
      return;
    }
    const range = selection.getRangeAt(0);
    if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) {
      setReadOnlySelection(null);
      return;
    }
    const elementFor = (node: Node) => node instanceof HTMLElement ? node : node.parentElement;
    const startBlock = elementFor(range.startContainer)?.closest<HTMLElement>('[data-node-id]');
    const endBlock = elementFor(range.endContainer)?.closest<HTMLElement>('[data-node-id]');
    const selectedText = selection.toString().trim();
    if (!startBlock || startBlock !== endBlock || !selectedText) {
      setReadOnlySelection(null);
      return;
    }
    const nodeId = startBlock.dataset.nodeId;
    const anchor = selectionActionAnchor(range);
    if (!nodeId || !anchor) {
      setReadOnlySelection(null);
      return;
    }
    setReadOnlySelection({ nodeId, selectedText, anchor });
  }, []);

  useEffect(() => {
    if (!documentReadOnly || !onRewriteSelection) return undefined;
    globalThis.document.addEventListener('selectionchange', recordReadOnlySelection);
    return () => globalThis.document.removeEventListener('selectionchange', recordReadOnlySelection);
  }, [documentReadOnly, onRewriteSelection, recordReadOnlySelection]);

  useEffect(() => {
    if (!rewriteDialogOpen) {
      pinnedReadOnlyRangeRef.current = null;
      setReadOnlyRewritePinned(false);
    }
  }, [rewriteDialogOpen]);

  const getPinnedReadOnlyRange = useCallback((): Range | null => {
    const range = pinnedReadOnlyRangeRef.current;
    if (!range) return null;
    try {
      return range.cloneRange();
    } catch {
      return null;
    }
  }, []);

  return (
    <section
      className='writer-ir'
      aria-label={t('chat.writerIR.documentRegion')}
      ref={rootRef}
    >
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
            <button type='button' onClick={requestDraftSave}>{t('common.retry')}</button>
            <button type='button' onClick={discardChanges}>{t('chat.writerIR.discard')}</button>
          </div>
        </div>
      )}

      {documentReadOnly ? (
        <div className='writer-ir__editor-shell'>
          <article
            className='writer-ir__document'
            onMouseUp={recordReadOnlySelection}
            onKeyUp={recordReadOnlySelection}
            tabIndex={onRewriteSelection ? 0 : undefined}
          >
            <h1 className='writer-ir__title'>{draft.title}</h1>
            {draft.blocks.length > 0 ? (
              <BlockSequence blocks={draft.blocks} />
            ) : (
              <div className='writer-ir__empty' role='status'>
                {t('chat.writerIR.emptyDocument')}
              </div>
            )}
          </article>
          <div className='writer-ir__rewrite-layer' ref={setReadOnlyRewriteLayer} />
          <ArtifactRewriteSelectionHighlight
            layer={readOnlyRewriteLayer}
            getRange={getPinnedReadOnlyRange}
            active={readOnlyRewritePinned}
          />
          {onRewriteSelection && readOnlySelection && (
            <ArtifactRewriteSelectionAction
              anchor={readOnlySelection.anchor}
              label={t('chat.artifactRewrite.action')}
              onActivate={() => {
                const browserSelection = globalThis.getSelection();
                if (browserSelection?.rangeCount && !browserSelection.isCollapsed) {
                  pinnedReadOnlyRangeRef.current = browserSelection.getRangeAt(0).cloneRange();
                  setReadOnlyRewritePinned(true);
                }
                onRewriteSelection(readOnlySelection);
                setReadOnlySelection(null);
              }}
              onDismiss={() => setReadOnlySelection(null)}
            />
          )}
        </div>
      ) : (
        <WriterIRDocumentEditor
          document={draft}
          ariaLabel={t('chat.writerIR.documentRegion')}
          onChange={handleDocumentChange}
          onFocus={beginTextEdit}
          onBlur={handleTextBlur}
          rewriteDialogOpen={rewriteDialogOpen}
          onRewriteSelection={
            !dirty && !saving && !externalUpdate ? onRewriteSelection : undefined
          }
          rewritePreview={rewritePreview}
          onRewritePreviewApplied={onRewritePreviewApplied}
          onRewritePreviewRejected={onRewritePreviewRejected}
        />
      )}
    </section>
  );
}
