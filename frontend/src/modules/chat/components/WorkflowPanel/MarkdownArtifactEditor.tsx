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
  jsxPlugin,
  linkDialogPlugin,
  linkPlugin,
  listsPlugin,
  markdownShortcutPlugin,
  quotePlugin,
  tablePlugin,
  thematicBreakPlugin,
  toolbarPlugin,
  GenericJsxEditor,
  type MDXEditorMethods,
  type JsxEditorProps,
} from '@mdxeditor/editor';
import {
  DownOutlined,
  HighlightOutlined,
  LinkOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
} from '@ant-design/icons';
import { Dropdown } from 'antd';
import '@mdxeditor/editor/style.css';
import {
  useCallback,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from 'react';
import { useTranslation } from 'react-i18next';
import { ArtifactRewriteInlineDiff } from './ArtifactRewriteDialog';
import { ArtifactRewriteSelectionHighlight } from './ArtifactRewriteSelectionHighlight';
import {
  floatingToolbarAnchor,
  selectedMarkdownParagraph,
  type FloatingToolbarAnchor,
  type MarkdownSelection,
} from './artifactRewriteSelection';
import { WorkflowPanelTabActiveContext, SlotEditingContext } from './slotEditingContext';
import type { RewriteSelectionPreview } from '@/modules/chat/utils/request';
import {
  applyWriterMarkdownInternalReference,
  collectWriterMarkdownOutline,
  writerMarkdownForEditor,
  writerMarkdownForSave,
} from './writerMarkdownAnchors';
import './MarkdownArtifactEditor.scss';

function WriterAnchorEditor(props: JsxEditorProps) {
  const id = props.mdastNode.attributes.find(
    (attribute) => attribute.type === 'mdxJsxAttribute' && attribute.name === 'id',
  )?.value;
  if (typeof id === 'string' && id.startsWith('block-')) {
    return <span id={id} className='writer-markdown-editor__system-anchor' aria-hidden='true' />;
  }
  return <GenericJsxEditor {...props} />;
}

function internalWriterReferenceLink(target: EventTarget | null): HTMLAnchorElement | null {
  return target instanceof Element
    ? target.closest<HTMLAnchorElement>('a[href^="#block-"]')
    : null;
}

function backtickRunLength(value: string, start: number): number {
  let end = start;
  while (value[end] === '`') end += 1;
  return end - start;
}

function isEscaped(value: string, index: number): boolean {
  let backslashes = 0;
  for (let cursor = index - 1; cursor >= 0 && value[cursor] === '\\'; cursor -= 1) {
    backslashes += 1;
  }
  return backslashes % 2 === 1;
}

function escapeMdxLessThanInLine(line: string): string {
  let result = '';
  let inlineCodeFence = 0;

  for (let index = 0; index < line.length;) {
    if (line[index] === '`') {
      const runLength = backtickRunLength(line, index);
      if (inlineCodeFence === 0) inlineCodeFence = runLength;
      else if (inlineCodeFence === runLength) inlineCodeFence = 0;
      result += line.slice(index, index + runLength);
      index += runLength;
      continue;
    }

    if (line[index] === '<' && inlineCodeFence === 0 && !isEscaped(line, index)) {
      const next = line[index + 1] ?? '';
      // MDX treats "<" as a JSX opener. Escape comparison/plain-text uses.
      if (!/[A-Za-z_$/>!?]/.test(next)) result += '\\';
    }
    result += line[index];
    index += 1;
  }
  return result;
}

function normalizeMarkdownForMdxEditor(markdown: string): string {
  let fenceCharacter = '';
  let fenceLength = 0;

  return writerMarkdownForEditor(markdown).split('\n').map((line) => {
    const fence = line.match(/^ {0,3}(`{3,}|~{3,})/);
    if (fence) {
      const marker = fence[1];
      if (!fenceCharacter) {
        fenceCharacter = marker[0];
        fenceLength = marker.length;
      } else if (marker[0] === fenceCharacter && marker.length >= fenceLength) {
        fenceCharacter = '';
        fenceLength = 0;
      }
      return line;
    }
    return fenceCharacter ? line : escapeMdxLessThanInLine(line);
  }).join('\n');
}

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
  /** Stable key used to register flush-before-retry/continue with WorkflowPanel. */
  editingKey?: string;
  onSave: (markdown: string, baseRevision: number) => Promise<number | undefined>;
  onRefresh?: () => void;
  onDownload?: () => void;
  /** Reports the current draft so the write-back action can compare it with its Feishu baseline. */
  onContentChange?: (markdown: string) => void;
  onRewriteSelection?: (selection: MarkdownSelection) => void;
  rewriteUnavailableReason?: string;
  rewriteDialogOpen?: boolean;
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
    || node.closest('.mdxeditor-select-content')
    || node.closest('.writer-markdown-editor__reference-dropdown'),
  );
}

function isMarkdownToolbarDropdownOpen(): boolean {
  return Boolean(
    document.querySelector('.mdxeditor-select-content[data-state="open"]')
    || document.querySelector('.mdxeditor-toolbar [data-state="open"]')
    || document.querySelector(
      '.writer-markdown-editor__reference-dropdown:not(.ant-dropdown-hidden)',
    ),
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
  rewriteDialogOpen = false,
  rewritePreview,
  onRewritePreviewApplied,
  onRewritePreviewRejected,
}: MarkdownArtifactEditorProps) {
  const { t } = useTranslation();
  const tabActive = useContext(WorkflowPanelTabActiveContext);
  const { setEditing, registerFlush, registerFooterAction } = useContext(SlotEditingContext);
  const [baseMarkdown, setBaseMarkdown] = useState(() => normalizeMarkdownForMdxEditor(markdown));
  const [draftMarkdown, setDraftMarkdown] = useState(() => normalizeMarkdownForMdxEditor(markdown));
  const [baseRevision, setBaseRevision] = useState(sourceRevision);
  const [editorKey, setEditorKey] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [conflict, setConflict] = useState(false);
  const [outlineOpen, setOutlineOpen] = useState(false);
  const [selection, setSelection] = useState<MarkdownSelection | null>(null);
  const [selectionToolbar, setSelectionToolbar] = useState<FloatingToolbarAnchor | null>(null);
  const [rewriteLayer, setRewriteLayer] = useState<HTMLDivElement | null>(null);
  const [rewriteSelectionPinned, setRewriteSelectionPinned] = useState(false);
  const rootRef = useRef<HTMLElement>(null);
  const editorRef = useRef<MDXEditorMethods>(null);
  const referenceSelectionRef = useRef<MarkdownSelection | null>(null);
  const pinnedRewriteRangeRef = useRef<Range | null>(null);
  const selectionToolbarDismissedRef = useRef(false);
  const latestSourceRef = useRef({ markdown, revision: sourceRevision });
  const pendingSourceRef = useRef<{ markdown: string; revision: number }>();
  const dirtyRef = useRef(false);
  const savingRef = useRef(false);
  const conflictRef = useRef(false);
  const saveChangesRef = useRef<() => Promise<boolean>>(async () => true);
  const outlineId = useId();

  const dirty = draftMarkdown !== baseMarkdown;
  const markdownOutline = useMemo(
    () => collectWriterMarkdownOutline(draftMarkdown),
    [draftMarkdown],
  );
  const referenceTargets = markdownOutline.items;
  const outlineBaseLevel = Math.min(
    ...markdownOutline.items.map((item) => item.level),
    6,
  );
  dirtyRef.current = dirty;
  savingRef.current = saving;
  conflictRef.current = conflict;

  useEffect(() => {
    onContentChange?.(writerMarkdownForSave(draftMarkdown));
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
    const nextSelection = root ? selectedMarkdownParagraph(root) : null;
    if (nextSelection?.supported) referenceSelectionRef.current = nextSelection;
    setSelection(nextSelection);
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
        && (
          root.contains(target)
          || targetElement?.closest('.mdxeditor-popup-container')
          || targetElement?.closest('.writer-markdown-editor__reference-dropdown')
        )
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

  const persistMarkdown = useCallback(async (
    nextDraft: string,
    revisionBeforeSave: number,
  ): Promise<boolean> => {
    if (savingRef.current || readOnly) return false;
    savingRef.current = true;
    setSaving(true);
    setSaveError(undefined);

    try {
      const savedMarkdown = writerMarkdownForSave(nextDraft);
      const revision = await onSave(savedMarkdown, revisionBeforeSave);
      const savedRevision = revision ?? revisionBeforeSave;
      setBaseMarkdown(nextDraft);
      setDraftMarkdown(nextDraft);
      setBaseRevision(savedRevision);
      latestSourceRef.current = { markdown: savedMarkdown, revision: savedRevision };
      pendingSourceRef.current = undefined;
      setConflict(false);
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
      savingRef.current = false;
      setSaving(false);
    }
  }, [onSave, readOnly, t]);

  const saveChanges = useCallback(async (): Promise<boolean> => {
    if (!dirty || savingRef.current || readOnly) return false;
    return persistMarkdown(draftMarkdown, baseRevision);
  }, [baseRevision, dirty, draftMarkdown, persistMarkdown, readOnly]);

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
      label: t('chat.slots.download'),
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
  useEffect(() => {
    if (!rewriteDialogOpen) {
      pinnedRewriteRangeRef.current = null;
      setRewriteSelectionPinned(false);
    }
  }, [rewriteDialogOpen]);
  const getPinnedRewriteRange = useCallback((): Range | null => {
    const range = pinnedRewriteRangeRef.current;
    if (!range) return null;
    try {
      return range.cloneRange();
    } catch {
      return null;
    }
  }, []);
  const requestPolish = useCallback(() => {
    if (polishDisabled || !selection || !onRewriteSelection) return;
    const browserSelection = globalThis.getSelection();
    if (browserSelection?.rangeCount && !browserSelection.isCollapsed) {
      pinnedRewriteRangeRef.current = browserSelection.getRangeAt(0).cloneRange();
      setRewriteSelectionPinned(true);
    }
    onRewriteSelection(selection);
    dismissSelectionToolbar();
  }, [dismissSelectionToolbar, onRewriteSelection, polishDisabled, selection]);
  const referenceDisabled = readOnly
    || !selection?.supported
    || saving
    || conflict
    || referenceTargets.length === 0;
  const applyCrossReference = useCallback((anchorId: string) => {
    const editor = editorRef.current;
    const referenceSelection = referenceSelectionRef.current;
    if (
      !editor
      || !referenceSelection?.supported
      || !anchorId
      || savingRef.current
      || conflictRef.current
      || readOnly
    ) return;
    const currentMarkdown = normalizeMarkdownForMdxEditor(editor.getMarkdown());
    const paragraphText = referenceSelection.paragraph?.textContent ?? '';
    const nextDraft = applyWriterMarkdownInternalReference(
      currentMarkdown,
      paragraphText,
      referenceSelection.startOffset ?? -1,
      referenceSelection.text,
      anchorId,
    );
    if (nextDraft === currentMarkdown) return;

    const surface = rootRef.current?.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const scrollTop = surface?.scrollTop;
    editor.setMarkdown(nextDraft);
    window.requestAnimationFrame(() => {
      if (surface && scrollTop !== undefined) surface.scrollTop = scrollTop;
      setDraftMarkdown(nextDraft);
      void persistMarkdown(nextDraft, baseRevision);
    });
    dismissSelectionToolbar();
  }, [baseRevision, dismissSelectionToolbar, persistMarkdown, readOnly]);

  const scrollToMarkdownTarget = useCallback((target: HTMLElement | null) => {
    const surface = rootRef.current?.querySelector<HTMLElement>(
      '.writer-markdown-editor__surface',
    );
    if (!surface || !target) return;
    const artifactBody = surface.closest<HTMLElement>('.workflow-slot__artifact-body');
    const scrollContainer = [surface, artifactBody].find(
      (element): element is HTMLElement => Boolean(
        element && element.scrollHeight > element.clientHeight + 1,
      ),
    ) ?? surface;
    const containerRect = scrollContainer.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    const reduceMotion = globalThis.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    scrollContainer.scrollTo({
      top: Math.max(
        0,
        scrollContainer.scrollTop + targetRect.top - containerRect.top - 8,
      ),
      behavior: reduceMotion ? 'auto' : 'smooth',
    });
  }, []);

  const navigateToOutlineItem = useCallback((anchorId: string) => {
    const target = Array.from(
      rootRef.current?.querySelectorAll<HTMLElement>('[id]') ?? [],
    ).find((element) => element.id === anchorId) ?? null;
    scrollToMarkdownTarget(target);
  }, [scrollToMarkdownTarget]);

  const navigateToDocumentTitle = useCallback(() => {
    const target = rootRef.current?.querySelector<HTMLElement>(
      '.mdxeditor-root-contenteditable h1, .mdxeditor-root-contenteditable h2, '
      + '.mdxeditor-root-contenteditable h3, .mdxeditor-root-contenteditable h4, '
      + '.mdxeditor-root-contenteditable h5, .mdxeditor-root-contenteditable h6',
    ) ?? null;
    scrollToMarkdownTarget(target);
  }, [scrollToMarkdownTarget]);

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
        outlineOpen ? ' writer-markdown-editor--outline-open' : ''
      }${
        selectionToolbar ? ' writer-markdown-editor--selection-toolbar-visible' : ''
      }`}
      aria-label={t('chat.writerMarkdown.documentRegion')}
      ref={rootRef}
      style={selectionToolbarStyle}
      onMouseDownCapture={(event) => {
        if (!internalWriterReferenceLink(event.target)) return;
        event.preventDefault();
        event.stopPropagation();
      }}
      onClickCapture={(event) => {
        const link = internalWriterReferenceLink(event.target);
        if (!link) return;
        event.preventDefault();
        event.stopPropagation();
        const anchorId = decodeURIComponent(link.hash.slice(1));
        navigateToOutlineItem(anchorId);
      }}
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

      <div className='writer-markdown-editor__document-layout'>
        <aside
          className='writer-markdown-editor__outline-rail'
          id={outlineId}
          onClick={(event) => event.stopPropagation()}
        >
          {outlineOpen ? (
            <nav
              className='writer-markdown-editor__outline'
              aria-label={t('chat.writerIR.outline')}
            >
              <button
                type='button'
                className='writer-markdown-editor__outline-toggle'
                title={t('chat.writerIR.collapseOutline')}
                aria-label={t('chat.writerIR.collapseOutline')}
                aria-controls={outlineId}
                aria-expanded='true'
                onClick={() => setOutlineOpen(false)}
              >
                <MenuFoldOutlined aria-hidden />
              </button>
              {markdownOutline.title && (
                <button
                  type='button'
                  className='writer-markdown-editor__outline-document-link'
                  title={markdownOutline.title}
                  aria-label={t('chat.writerIR.jumpToHeading', {
                    title: markdownOutline.title,
                  })}
                  onClick={navigateToDocumentTitle}
                >
                  {markdownOutline.title}
                </button>
              )}
              {markdownOutline.items.length > 0 ? (
                <ol className='writer-markdown-editor__outline-list'>
                  {markdownOutline.items.map((item) => (
                    <li key={item.anchorId}>
                      <button
                        type='button'
                        className={
                          `writer-markdown-editor__outline-link `
                          + `writer-markdown-editor__outline-link--level-${
                            Math.max(1, item.level - outlineBaseLevel + 1)
                          }`
                        }
                        title={item.label}
                        aria-label={t('chat.writerIR.jumpToHeading', { title: item.label })}
                        onClick={() => navigateToOutlineItem(item.anchorId)}
                      >
                        {item.label}
                      </button>
                    </li>
                  ))}
                </ol>
              ) : (
                <div className='writer-markdown-editor__outline-empty' role='status'>
                  {t('chat.writerIR.noHeadings')}
                </div>
              )}
            </nav>
          ) : (
            <button
              type='button'
              className={
                'writer-markdown-editor__outline-toggle '
                + 'writer-markdown-editor__outline-toggle--collapsed'
              }
              title={t('chat.writerIR.expandOutline')}
              aria-label={t('chat.writerIR.expandOutline')}
              aria-controls={outlineId}
              aria-expanded='false'
              onClick={() => setOutlineOpen(true)}
            >
              <MenuUnfoldOutlined aria-hidden />
            </button>
          )}
        </aside>
        <div className='writer-markdown-editor__main'>
          <MDXEditor
            ref={editorRef}
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
              jsxPlugin({
                jsxComponentDescriptors: [{
                  name: 'a',
                  kind: 'flow',
                  props: [{ name: 'id', type: 'string' }],
                  hasChildren: true,
                  Editor: WriterAnchorEditor,
                }],
              }),
              imagePlugin(),
              codeBlockPlugin({ defaultCodeBlockLanguage: 'text' }),
              codeMirrorPlugin({ codeBlockLanguages: MARKDOWN_CODE_LANGUAGES }),
              markdownShortcutPlugin(),
              toolbarPlugin({
                toolbarContents: () => (
                  <>
                    <div className='writer-markdown-editor__toolbar-group writer-markdown-editor__toolbar-group--block'>
                      <BlockTypeSelect />
                    </div>
                    <span className='writer-markdown-editor__toolbar-divider' aria-hidden='true' />
                    <div
                      className='writer-markdown-editor__toolbar-group'
                      role='group'
                      aria-label={t('chat.writerIR.formatToolbar')}
                    >
                      <BoldItalicUnderlineToggles />
                      <ListsToggle />
                    </div>
                    <span className='writer-markdown-editor__toolbar-divider' aria-hidden='true' />
                    <div className='writer-markdown-editor__toolbar-group writer-markdown-editor__toolbar-group--actions'>
                      {showPolishAction && (
                        <button
                          type='button'
                          className='writer-markdown-editor__polish-action'
                          disabled={polishDisabled}
                          title={polishTitle}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={requestPolish}
                        >
                          <HighlightOutlined aria-hidden />
                          <span>{t('chat.artifactRewrite.action')}</span>
                        </button>
                      )}
                      <Dropdown
                        trigger={['click']}
                        placement='bottomLeft'
                        overlayClassName='writer-markdown-editor__reference-dropdown'
                        disabled={referenceDisabled}
                        menu={{
                          items: referenceTargets.map((target) => ({
                            key: target.anchorId,
                            label: (
                              <span
                                className='writer-markdown-editor__reference-option'
                                title={target.label}
                              >
                                {target.label}
                              </span>
                            ),
                          })),
                          onClick: ({ key }) => applyCrossReference(String(key)),
                        }}
                      >
                        <button
                          type='button'
                          className='writer-markdown-editor__reference-select'
                          disabled={referenceDisabled}
                          aria-label={t('chat.writerIR.crossReference')}
                          aria-haspopup='menu'
                          title={t('chat.writerIR.crossReference')}
                          onMouseDown={(event) => {
                            event.preventDefault();
                            event.stopPropagation();
                            if (selection?.supported) referenceSelectionRef.current = selection;
                          }}
                        >
                          <LinkOutlined aria-hidden />
                          <span>{t('chat.writerIR.crossReference')}</span>
                          <DownOutlined
                            className='writer-markdown-editor__reference-caret'
                            aria-hidden
                          />
                        </button>
                      </Dropdown>
                    </div>
                  </>
                ),
              }),
            ]}
          />
        </div>
      </div>
      <div className='writer-markdown-editor__rewrite-layer' ref={setRewriteLayer} />
      <ArtifactRewriteSelectionHighlight
        layer={rewriteLayer}
        getRange={getPinnedRewriteRange}
        active={rewriteSelectionPinned}
      />
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
