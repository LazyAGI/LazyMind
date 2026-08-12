import {
  BlockTypeSelect,
  BoldItalicUnderlineToggles,
  ListsToggle,
  MDXEditor,
  codeBlockPlugin,
  codeMirrorPlugin,
  frontmatterPlugin,
  headingsPlugin,
  imagePlugin,
  linkDialogPlugin,
  linkPlugin,
  listsPlugin,
  markdownShortcutPlugin,
  quotePlugin,
  tablePlugin,
  thematicBreakPlugin,
  toolbarPlugin,
} from '@mdxeditor/editor';
import '@mdxeditor/editor/style.css';
import {
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from 'react';
import { useTranslation } from 'react-i18next';
import { ArtifactRewriteInlineDiff } from './ArtifactRewriteDialog';
import {
  floatingToolbarAnchor,
  selectedMarkdownParagraph,
  type FloatingToolbarAnchor,
  type MarkdownSelection,
} from './artifactRewriteSelection';
import { normalizeMarkdownForMdxEditor } from './mdxMarkdown';
import { PluginPanelTabActiveContext, SlotEditingContext } from './slotEditingContext';
import type { RewriteSelectionPreview } from '@/modules/chat/utils/request';
import './MarkdownArtifactEditor.scss';

const MARKDOWN_CODE_LANGUAGES = {
  bash: 'Shell',
  css: 'CSS',
  html: 'HTML',
  javascript: 'JavaScript',
  json: 'JSON',
  markdown: 'Markdown',
  python: 'Python',
  sql: 'SQL',
  text: 'Plain text',
  typescript: 'TypeScript',
  yaml: 'YAML',
};

export interface MarkdownRewritePreview {
  paragraph: HTMLElement;
  startOffset?: number;
  sessionId: string;
  slotId: string;
  listIndex: number;
  preview: RewriteSelectionPreview;
}

interface MarkdownArtifactEditorProps {
  markdown: string;
  sourceRevision: number;
  readOnly?: boolean;
  /** Stable key used to register flush-before-retry/continue with PluginPanel. */
  editingKey?: string;
  onSave: (markdown: string, baseRevision: number) => Promise<number | undefined>;
  onRefresh?: () => void;
  onDownload?: () => void;
  /** Reports the current draft so the write-back action can compare it with its Feishu baseline. */
  onContentChange?: (markdown: string) => void;
  onRewriteSelection?: (selection: MarkdownSelection) => void;
  rewriteUnavailableReason?: string;
  rewritePreview?: MarkdownRewritePreview | null;
  onRewritePreviewApplied?: (revision?: number) => void;
  onRewritePreviewRejected?: () => void;
}

function isRevisionConflict(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false;
  const response = (error as { response?: { status?: unknown } }).response;
  return response?.status === 409;
}

function isMarkdownToolbarInteractionTarget(node: Node | null | undefined): boolean {
  if (!(node instanceof Element)) return false;
  return Boolean(
    node.closest('.mdxeditor-toolbar')
    || node.closest('.mdxeditor-popup-container')
    || node.closest('.mdxeditor-select-content'),
  );
}

function isMarkdownToolbarDropdownOpen(): boolean {
  return Boolean(
    document.querySelector('.mdxeditor-select-content[data-state="open"]')
    || document.querySelector('.mdxeditor-toolbar [data-state="open"]'),
  );
}

export function MarkdownArtifactEditor({
  markdown,
  sourceRevision,
  readOnly = false,
  editingKey,
  onSave,
  onRefresh,
  onDownload,
  onContentChange,
  onRewriteSelection,
  rewriteUnavailableReason,
  rewritePreview,
  onRewritePreviewApplied,
  onRewritePreviewRejected,
}: MarkdownArtifactEditorProps) {
  const { t } = useTranslation();
  const tabActive = useContext(PluginPanelTabActiveContext);
  const { setEditing, registerFlush, registerFooterAction } = useContext(SlotEditingContext);
  const [baseMarkdown, setBaseMarkdown] = useState(() => normalizeMarkdownForMdxEditor(markdown));
  const [draftMarkdown, setDraftMarkdown] = useState(() => normalizeMarkdownForMdxEditor(markdown));
  const [baseRevision, setBaseRevision] = useState(sourceRevision);
  const [editorKey, setEditorKey] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [conflict, setConflict] = useState(false);
  const [selection, setSelection] = useState<MarkdownSelection | null>(null);
  const [selectionToolbar, setSelectionToolbar] = useState<FloatingToolbarAnchor | null>(null);
  const [rewriteLayer, setRewriteLayer] = useState<HTMLDivElement | null>(null);
  const rootRef = useRef<HTMLElement>(null);
  const selectionToolbarDismissedRef = useRef(false);
  const latestSourceRef = useRef({ markdown, revision: sourceRevision });
  const pendingSourceRef = useRef<{ markdown: string; revision: number }>();
  const dirtyRef = useRef(false);
  const savingRef = useRef(false);
  const conflictRef = useRef(false);
  const saveChangesRef = useRef<() => Promise<boolean>>(async () => true);

  const dirty = draftMarkdown !== baseMarkdown;
  dirtyRef.current = dirty;
  savingRef.current = saving;
  conflictRef.current = conflict;

  useEffect(() => {
    onContentChange?.(draftMarkdown);
  }, [draftMarkdown, onContentChange]);

  const dismissSelectionToolbar = useCallback(() => {
    selectionToolbarDismissedRef.current = true;
    setSelectionToolbar(null);
  }, []);

  const updateSelectionToolbar = useCallback(() => {
    if (readOnly) {
      dismissSelectionToolbar();
      return;
    }
    const root = rootRef.current;
    const surface = root?.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const editable = surface?.querySelector<HTMLElement>(
      '.mdxeditor-root-contenteditable [contenteditable="true"]',
    );
    const toolbar = surface?.querySelector<HTMLElement>('.mdxeditor-toolbar');
    const keepToolbarForInteraction = isMarkdownToolbarInteractionTarget(document.activeElement)
      || isMarkdownToolbarDropdownOpen();
    const browserSelection = globalThis.getSelection();
    const hasValidSelection = Boolean(
      browserSelection
      && !browserSelection.isCollapsed
      && browserSelection.rangeCount > 0
      && browserSelection.toString().trim()
      && editable?.contains(browserSelection.anchorNode)
      && editable?.contains(browserSelection.focusNode),
    );
    if (
      !surface
      || !editable
      || !toolbar
      || !hasValidSelection
    ) {
      if (keepToolbarForInteraction) return;
      dismissSelectionToolbar();
      return;
    }

    const range = browserSelection!.getRangeAt(0);
    const selectionRect = Array.from(range.getClientRects()).find(
      (rect) => rect.width > 0 || rect.height > 0,
    ) ?? range.getBoundingClientRect();
    const surfaceRect = surface.getBoundingClientRect();
    if (
      (selectionRect.width === 0 && selectionRect.height === 0)
      || selectionRect.bottom < surfaceRect.top
      || selectionRect.top > surfaceRect.bottom
    ) {
      if (keepToolbarForInteraction) return;
      dismissSelectionToolbar();
      return;
    }

    const nextAnchor = floatingToolbarAnchor({
      selectionRect,
      containerRect: surfaceRect,
      toolbarWidth: toolbar.offsetWidth,
      toolbarHeight: toolbar.offsetHeight,
    });
    setSelectionToolbar((current) => (
      current
      && current.top === nextAnchor.top
      && current.left === nextAnchor.left
      && current.maxWidth === nextAnchor.maxWidth
      && current.placement === nextAnchor.placement
        ? current
        : nextAnchor
    ));
  }, [dismissSelectionToolbar, readOnly]);

  const recordSelection = useCallback((showToolbar = true) => {
    const root = rootRef.current;
    setSelection(root ? selectedMarkdownParagraph(root) : null);
    if (!showToolbar) return;
    selectionToolbarDismissedRef.current = false;
    updateSelectionToolbar();
  }, [updateSelectionToolbar]);

  useEffect(() => {
    const handleSelectionChange = () => recordSelection(!selectionToolbarDismissedRef.current);
    document.addEventListener('selectionchange', handleSelectionChange);
    return () => document.removeEventListener('selectionchange', handleSelectionChange);
  }, [recordSelection]);

  useEffect(() => {
    const dismissOnOutsidePointerDown = (event: MouseEvent) => {
      const root = rootRef.current;
      const target = event.target instanceof Node ? event.target : null;
      const targetElement = target instanceof Element ? target : target?.parentElement;
      if (
        root
        && target
        && (root.contains(target) || targetElement?.closest('.mdxeditor-popup-container'))
      ) return;
      dismissSelectionToolbar();
    };
    const dismissOnScroll = (event: Event) => {
      const root = rootRef.current;
      const surface = root?.querySelector<HTMLElement>('.writer-markdown-editor__surface');
      if (event.target === surface || !root || !(event.target instanceof Node) || !root.contains(event.target)) {
        dismissSelectionToolbar();
      }
    };
    const dismissOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') dismissSelectionToolbar();
    };

    document.addEventListener('mousedown', dismissOnOutsidePointerDown, true);
    document.addEventListener('scroll', dismissOnScroll, true);
    window.addEventListener('resize', dismissSelectionToolbar);
    window.addEventListener('keydown', dismissOnEscape);
    return () => {
      document.removeEventListener('mousedown', dismissOnOutsidePointerDown, true);
      document.removeEventListener('scroll', dismissOnScroll, true);
      window.removeEventListener('resize', dismissSelectionToolbar);
      window.removeEventListener('keydown', dismissOnEscape);
    };
  }, [dismissSelectionToolbar]);

  useEffect(() => {
    const latestSource = latestSourceRef.current;
    if (
      sourceRevision === latestSource.revision
      && markdown === latestSource.markdown
    ) return;
    latestSourceRef.current = { markdown, revision: sourceRevision };

    if (dirty) {
      pendingSourceRef.current = { markdown, revision: sourceRevision };
      setConflict(true);
      return;
    }

    const normalizedMarkdown = normalizeMarkdownForMdxEditor(markdown);
    setBaseMarkdown(normalizedMarkdown);
    setDraftMarkdown(normalizedMarkdown);
    setBaseRevision(sourceRevision);
    setSaveError(undefined);
    setConflict(false);
    pendingSourceRef.current = undefined;
    setEditorKey((value) => value + 1);
  }, [dirty, markdown, sourceRevision]);

  const saveChanges = useCallback(async (): Promise<boolean> => {
    if (!dirty || saving || readOnly) return false;
    setSaving(true);
    setSaveError(undefined);

    try {
      const revision = await onSave(draftMarkdown, baseRevision);
      const savedRevision = revision ?? baseRevision;
      setBaseMarkdown(draftMarkdown);
      setBaseRevision(savedRevision);
      latestSourceRef.current = { markdown: draftMarkdown, revision: savedRevision };
      pendingSourceRef.current = undefined;
      setConflict(false);
      setEditorKey((value) => value + 1);
      return true;
    } catch (error) {
      setConflict(isRevisionConflict(error));
      setSaveError(
        isRevisionConflict(error)
          ? t('chat.writerMarkdown.revisionConflict')
          : t('chat.writerMarkdown.saveFailed'),
      );
      return false;
    } finally {
      setSaving(false);
    }
  }, [baseRevision, dirty, draftMarkdown, onSave, readOnly, saving, t]);

  saveChangesRef.current = saveChanges;

  useEffect(() => {
    if (!editingKey || readOnly) return undefined;
    setEditing(editingKey, dirty);
    return () => setEditing(editingKey, false);
  }, [dirty, editingKey, readOnly, setEditing]);

  useEffect(() => {
    if (!editingKey) return undefined;
    return registerFlush(editingKey, async () => {
      if (readOnly) return true;
      while (savingRef.current) {
        await new Promise<void>((resolve) => {
          window.setTimeout(resolve, 40);
        });
      }
      if (!dirtyRef.current) return true;
      if (conflictRef.current) return false;
      return saveChangesRef.current();
    });
  }, [editingKey, readOnly, registerFlush]);

  useEffect(() => {
    if (!editingKey || !onDownload || !tabActive) return undefined;
    return registerFooterAction(editingKey, {
      label: t('chat.writer.downloadMarkdown'),
      order: 10,
      tone: 'secondary',
      icon: 'download',
      onClick: onDownload,
    });
  }, [editingKey, onDownload, registerFooterAction, t, tabActive]);

  const showPolishAction = Boolean(onRewriteSelection || rewriteUnavailableReason);
  const polishDisabled = !onRewriteSelection
    || !selection?.supported
    || dirty
    || saving
    || conflict
    || Boolean(rewriteUnavailableReason);
  const polishTitle = !selection?.supported
    ? t('chat.artifactRewrite.singleParagraphHint')
    : dirty
      ? t('chat.artifactRewrite.saveFirstHint')
      : rewriteUnavailableReason ?? t('chat.artifactRewrite.action');
  const requestPolish = useCallback(() => {
    if (polishDisabled || !selection || !onRewriteSelection) return;
    onRewriteSelection(selection);
    dismissSelectionToolbar();
  }, [dismissSelectionToolbar, onRewriteSelection, polishDisabled, selection]);

  const selectionToolbarStyle = selectionToolbar
    ? {
      '--writer-markdown-selection-toolbar-top': `${selectionToolbar.top}px`,
      '--writer-markdown-selection-toolbar-left': `${selectionToolbar.left}px`,
      '--writer-markdown-selection-toolbar-max-width': `${selectionToolbar.maxWidth}px`,
    } as CSSProperties
    : undefined;

  return (
    <section
      className={`writer-markdown-editor${
        selectionToolbar ? ' writer-markdown-editor--selection-toolbar-visible' : ''
      }`}
      aria-label={t('chat.writerMarkdown.documentRegion')}
      ref={rootRef}
      style={selectionToolbarStyle}
      onMouseDown={(event) => {
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest('.mdxeditor-toolbar')) {
          event.preventDefault();
        }
      }}
      onMouseUp={() => recordSelection()}
      onKeyUp={(event) => {
        if (event.key !== 'Escape') recordSelection();
      }}
    >
      {conflict && (
        <div className='writer-markdown-editor__notice writer-markdown-editor__notice--warning' role='alert'>
          <span>{t('chat.writerMarkdown.externalUpdate')}</span>
          {onRefresh && (
            <button
              type='button'
              className='workflow-slot__file-action-btn'
              onClick={onRefresh}
              disabled={saving}
            >
              {t('common.refresh')}
            </button>
          )}
        </div>
      )}

      {saveError && (
        <div className='writer-markdown-editor__notice writer-markdown-editor__notice--error' role='alert'>
          <span>{saveError}</span>
          {!conflict && (
            <button
              type='button'
              className='workflow-slot__file-action-btn'
              onClick={saveChanges}
              disabled={saving || !dirty}
            >
              {t('common.retry')}
            </button>
          )}
        </div>
      )}

      <MDXEditor
        key={editorKey}
        className='writer-markdown-editor__surface'
        markdown={baseMarkdown}
        readOnly={readOnly}
        onChange={setDraftMarkdown}
        plugins={[
          headingsPlugin(),
          listsPlugin(),
          quotePlugin(),
          thematicBreakPlugin(),
          linkPlugin(),
          linkDialogPlugin(),
          tablePlugin(),
          frontmatterPlugin(),
          imagePlugin(),
          codeBlockPlugin({ defaultCodeBlockLanguage: 'text' }),
          codeMirrorPlugin({ codeBlockLanguages: MARKDOWN_CODE_LANGUAGES }),
          markdownShortcutPlugin(),
          toolbarPlugin({
            toolbarContents: () => (
              <>
                <BoldItalicUnderlineToggles />
                <BlockTypeSelect />
                {showPolishAction && (
                  <button
                    type='button'
                    className='writer-markdown-editor__polish-action'
                    disabled={polishDisabled}
                    title={polishTitle}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={requestPolish}
                  >
                    {t('chat.artifactRewrite.action')}
                  </button>
                )}
                <ListsToggle />
              </>
            ),
          }),
        ]}
      />
      <div className='writer-markdown-editor__rewrite-layer' ref={setRewriteLayer} />
      {rewritePreview && rewriteLayer && onRewritePreviewApplied && onRewritePreviewRejected && (
        <ArtifactRewriteInlineDiff
          target={rewritePreview.paragraph}
          layer={rewriteLayer}
          startOffset={rewritePreview.startOffset}
          sessionId={rewritePreview.sessionId}
          slotId={rewritePreview.slotId}
          listIndex={rewritePreview.listIndex}
          preview={rewritePreview.preview}
          onApplied={onRewritePreviewApplied}
          onReject={onRewritePreviewRejected}
        />
      )}
    </section>
  );
}
