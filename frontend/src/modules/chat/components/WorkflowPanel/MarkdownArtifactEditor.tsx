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
  DisconnectOutlined,
  DownOutlined,
  FontSizeOutlined,
  HighlightOutlined,
  LinkOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  PictureOutlined,
} from '@ant-design/icons';
import { Dropdown } from 'antd';
import '@mdxeditor/editor/style.css';
import {
  useCallback,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
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
import { resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';
import {
  applyWriterMarkdownInternalReference,
  collectWriterMarkdownDomAnchors,
  collectWriterMarkdownOutline,
  collectWriterMarkdownReferenceTargets,
  protectWriterMarkdownAnchors,
  removeWriterMarkdownInternalReference,
  writerMarkdownForEditing,
  writerMarkdownPersistenceIdentity,
  writerMarkdownForSave,
} from './writerMarkdownAnchors';
import './MarkdownArtifactEditor.scss';

/** Idle debounce after the latest edit before a silent draft save. */
const MARKDOWN_AUTOSAVE_IDLE_MS = 1_000;

function WriterAnchorEditor(props: JsxEditorProps) {
  const id = props.mdastNode.attributes.find(
    (attribute) => attribute.type === 'mdxJsxAttribute' && attribute.name === 'id',
  )?.value;
  if (typeof id === 'string' && id.startsWith('block-')) {
    return (
      <span
        id={id}
        className='writer-markdown-editor__system-anchor'
        aria-hidden='true'
        contentEditable={false}
      />
    );
  }
  return <GenericJsxEditor {...props} />;
}

function internalWriterReferenceLink(target: EventTarget | null): HTMLAnchorElement | null {
  return target instanceof Element
    ? target.closest<HTMLAnchorElement>('a[href^="#block-"]')
    : null;
}

interface MarkdownSelectionRestorePoint {
  paragraphIndex: number;
  startOffset: number;
  endOffset: number;
  text: string;
}

interface MarkdownEditorSelectionRestorePoint {
  startOffset: number;
  endOffset: number;
  selectedText: string;
}

function markdownEditable(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>(
    '.mdxeditor-root-contenteditable [contenteditable="true"]',
  );
}

function markdownSelectionRestorePoint(
  root: HTMLElement,
  selection: MarkdownSelection,
): MarkdownSelectionRestorePoint | null {
  const editable = markdownEditable(root);
  const paragraph = selection.paragraph;
  const startOffset = selection.startOffset;
  if (!editable || !paragraph || startOffset === undefined || !editable.contains(paragraph)) {
    return null;
  }
  const paragraphIndex = Array.from(editable.querySelectorAll('p'))
    .findIndex((candidate) => candidate === paragraph);
  if (paragraphIndex < 0) return null;
  return {
    paragraphIndex,
    startOffset,
    endOffset: startOffset + selection.text.length,
    text: selection.text,
  };
}

function markdownTextBoundary(
  paragraph: HTMLElement,
  offset: number,
): { node: Node; offset: number } {
  const walker = globalThis.document.createTreeWalker(paragraph, NodeFilter.SHOW_TEXT);
  let remaining = Math.max(0, offset);
  let textNode = walker.nextNode();
  while (textNode) {
    const length = textNode.textContent?.length ?? 0;
    if (remaining <= length) return { node: textNode, offset: remaining };
    remaining -= length;
    textNode = walker.nextNode();
  }
  return { node: paragraph, offset: paragraph.childNodes.length };
}

function restoreMarkdownSelection(
  root: HTMLElement,
  restorePoint: MarkdownSelectionRestorePoint,
): void {
  const editable = markdownEditable(root);
  const paragraph = editable?.querySelectorAll('p')[restorePoint.paragraphIndex];
  if (
    !editable
    || !(paragraph instanceof HTMLElement)
    || (paragraph.textContent ?? '').slice(restorePoint.startOffset, restorePoint.endOffset)
      !== restorePoint.text
  ) return;

  const start = markdownTextBoundary(paragraph, restorePoint.startOffset);
  const end = markdownTextBoundary(paragraph, restorePoint.endOffset);
  const range = globalThis.document.createRange();
  range.setStart(start.node, start.offset);
  range.setEnd(end.node, end.offset);
  editable.focus({ preventScroll: true });
  const browserSelection = globalThis.getSelection();
  browserSelection?.removeAllRanges();
  browserSelection?.addRange(range);
}

function markdownEditorSelectionRestorePoint(
  editable: HTMLElement,
): MarkdownEditorSelectionRestorePoint | null {
  const selection = globalThis.getSelection();
  if (!selection || selection.rangeCount === 0) return null;
  const range = selection.getRangeAt(0);
  if (!editable.contains(range.startContainer) || !editable.contains(range.endContainer)) {
    return null;
  }
  const offsetBefore = (node: Node, offset: number) => {
    const prefix = globalThis.document.createRange();
    prefix.selectNodeContents(editable);
    prefix.setEnd(node, offset);
    return prefix.toString().length;
  };
  return {
    startOffset: offsetBefore(range.startContainer, range.startOffset),
    endOffset: offsetBefore(range.endContainer, range.endOffset),
    selectedText: range.toString(),
  };
}

function restoreMarkdownEditorSelection(
  editable: HTMLElement,
  restorePoint: MarkdownEditorSelectionRestorePoint,
): boolean {
  const text = editable.textContent ?? '';
  const startOffset = Math.min(restorePoint.startOffset, text.length);
  const endOffset = Math.min(restorePoint.endOffset, text.length);
  if (
    restorePoint.selectedText
    && text.slice(startOffset, endOffset) !== restorePoint.selectedText
  ) return false;

  const start = markdownTextBoundary(editable, startOffset);
  const end = markdownTextBoundary(editable, endOffset);
  const range = globalThis.document.createRange();
  range.setStart(start.node, start.offset);
  range.setEnd(end.node, end.offset);
  editable.focus({ preventScroll: true });
  const selection = globalThis.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
  return true;
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

  return writerMarkdownForEditing(markdown).split('\n').map((line) => {
    const fence = line.match(/^\s*(`{3,}|~{3,})/);
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
  onSave: (
    markdown: string,
    baseRevision: number,
  ) => Promise<number | { markdown: string; revision?: number } | undefined>;
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
  const [anchorSourceMarkdown, setAnchorSourceMarkdown] = useState(markdown);
  const [baseRevision, setBaseRevision] = useState(sourceRevision);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [conflict, setConflict] = useState(false);
  const [outlineOpen, setOutlineOpen] = useState(false);
  const [pageWidth, setPageWidth] = useState<'default' | 'wide'>('default');
  const [selection, setSelection] = useState<MarkdownSelection | null>(null);
  const [selectionToolbar, setSelectionToolbar] = useState<FloatingToolbarAnchor | null>(null);
  const [referenceDropdownOpen, setReferenceDropdownOpen] = useState(false);
  const [rewriteLayer, setRewriteLayer] = useState<HTMLDivElement | null>(null);
  const [rewriteSelectionPinned, setRewriteSelectionPinned] = useState(false);
  const rootRef = useRef<HTMLElement>(null);
  const editorRef = useRef<MDXEditorMethods>(null);
  const referenceSelectionRef = useRef<MarkdownSelection | null>(null);
  const pinnedRewriteRangeRef = useRef<Range | null>(null);
  const selectionToolbarDismissedRef = useRef(false);
  const latestSourceRef = useRef({ markdown, revision: sourceRevision });
  const staleSourceEchoRef = useRef<{ markdown: string; revision: number }>();
  const pendingSourceRef = useRef<{ markdown: string; revision: number }>();
  const autoSaveTimerRef = useRef<number | undefined>(undefined);
  const viewRestoreFrameRef = useRef<number | undefined>(undefined);
  const draftMarkdownRef = useRef(draftMarkdown);
  const dirtyRef = useRef(false);
  const savingRef = useRef(false);
  const conflictRef = useRef(false);
  const saveChangesRef = useRef<() => Promise<boolean>>(async () => true);
  const outlineId = useId();

  // Lexical can hold a real empty paragraph even though Markdown cannot
  // persist one. Only persistable document changes participate in autosave;
  // the transient paragraph remains owned by the editor until it has content.
  const dirty = writerMarkdownPersistenceIdentity(draftMarkdown)
    !== writerMarkdownPersistenceIdentity(baseMarkdown);
  const materializedDraftMarkdown = useMemo(
    () => protectWriterMarkdownAnchors(
      anchorSourceMarkdown,
      draftMarkdown,
      false,
    ),
    [anchorSourceMarkdown, draftMarkdown],
  );
  const markdownOutline = useMemo(
    () => collectWriterMarkdownOutline(materializedDraftMarkdown),
    [materializedDraftMarkdown],
  );
  const referenceTargets = useMemo(
    () => collectWriterMarkdownReferenceTargets(materializedDraftMarkdown),
    [materializedDraftMarkdown],
  );
  const outlineBaseLevel = Math.min(
    ...markdownOutline.items.map((item) => item.level),
    6,
  );
  dirtyRef.current = dirty;
  draftMarkdownRef.current = draftMarkdown;
  savingRef.current = saving;
  conflictRef.current = conflict;

  useEffect(() => {
    onContentChange?.(writerMarkdownForSave(materializedDraftMarkdown));
  }, [materializedDraftMarkdown, onContentChange]);

  useLayoutEffect(() => {
    const root = rootRef.current;
    if (!root) return undefined;
    let frame: number | undefined;
    const applyDomAnchors = () => {
      const editable = root.querySelector<HTMLElement>('.mdxeditor-root-contenteditable');
      if (!editable) return;
      editable.querySelectorAll<HTMLElement>('[data-writer-system-anchor="true"]')
        .forEach((element) => {
          element.removeAttribute('id');
          delete element.dataset.writerSystemAnchor;
        });
      const headings = editable.querySelectorAll<HTMLElement>('h1, h2, h3, h4, h5, h6');
      const images = editable.querySelectorAll<HTMLElement>('img');
      collectWriterMarkdownDomAnchors(materializedDraftMarkdown).forEach((anchor) => {
        const target = anchor.type === 'heading'
          ? headings.item(anchor.targetIndex)
          : images.item(anchor.targetIndex);
        if (!target) return;
        target.id = anchor.anchorId;
        target.dataset.writerSystemAnchor = 'true';
      });
    };
    const scheduleDomAnchors = () => {
      if (frame !== undefined) return;
      frame = window.requestAnimationFrame(() => {
        frame = undefined;
        applyDomAnchors();
      });
    };
    const observer = new MutationObserver(scheduleDomAnchors);
    observer.observe(root, {
      childList: true,
      subtree: true,
      // Image previews resolve asynchronously. Reconcile again when the
      // editor updates the real image node after the Markdown render.
      attributes: true,
      attributeFilter: ['src'],
    });
    applyDomAnchors();
    scheduleDomAnchors();
    return () => {
      observer.disconnect();
      if (frame !== undefined) window.cancelAnimationFrame(frame);
    };
  }, [materializedDraftMarkdown]);

  const replaceMarkdownSilently = useCallback((nextMarkdown: string) => {
    const root = rootRef.current;
    const editor = editorRef.current;
    const surface = root?.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    if (!root || !editor || !surface) return;
    const artifactBody = surface.closest<HTMLElement>('.workflow-slot__artifact-body');
    const editable = markdownEditable(root);
    const hadFocus = Boolean(editable?.contains(globalThis.document.activeElement));
    const selection = editable && hadFocus
      ? markdownEditorSelectionRestorePoint(editable)
      : null;
    const surfaceScroll = { top: surface.scrollTop, left: surface.scrollLeft };
    const artifactScroll = artifactBody
      ? { top: artifactBody.scrollTop, left: artifactBody.scrollLeft }
      : null;

    const restoreView = () => {
      if (!root.isConnected) return;
      const nextSurface = root.querySelector<HTMLElement>('.writer-markdown-editor__surface');
      const nextEditable = markdownEditable(root);
      if (hadFocus && nextEditable) {
        if (!selection || !restoreMarkdownEditorSelection(nextEditable, selection)) {
          nextEditable.focus({ preventScroll: true });
        }
      }
      if (nextSurface) {
        nextSurface.scrollTop = surfaceScroll.top;
        nextSurface.scrollLeft = surfaceScroll.left;
      }
      if (artifactBody && artifactScroll) {
        artifactBody.scrollTop = artifactScroll.top;
        artifactBody.scrollLeft = artifactScroll.left;
      }
    };

    editor.setMarkdown(nextMarkdown);
    restoreView();
    if (viewRestoreFrameRef.current !== undefined) {
      window.cancelAnimationFrame(viewRestoreFrameRef.current);
    }
    viewRestoreFrameRef.current = window.requestAnimationFrame(() => {
      viewRestoreFrameRef.current = undefined;
      restoreView();
    });
  }, []);

  useEffect(() => () => {
    if (viewRestoreFrameRef.current !== undefined) {
      window.cancelAnimationFrame(viewRestoreFrameRef.current);
    }
  }, []);

  const dismissSelectionToolbar = useCallback(() => {
    selectionToolbarDismissedRef.current = true;
    setSelectionToolbar(null);
    setReferenceDropdownOpen(false);
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

  useLayoutEffect(() => {
    const latestSource = latestSourceRef.current;
    if (
      sourceRevision === latestSource.revision
      && markdown === latestSource.markdown
    ) {
      staleSourceEchoRef.current = undefined;
      return;
    }
    // A save may resolve before its parent prop echo; ignore that exact stale
    // snapshot without blocking a later intentional history revision change.
    const staleSource = staleSourceEchoRef.current;
    if (
      staleSource
      && sourceRevision === staleSource.revision
      && markdown === staleSource.markdown
    ) return;
    staleSourceEchoRef.current = undefined;
    latestSourceRef.current = { markdown, revision: sourceRevision };

    if (dirty) {
      pendingSourceRef.current = { markdown, revision: sourceRevision };
      setConflict(true);
      return;
    }

    const normalizedMarkdown = normalizeMarkdownForMdxEditor(markdown);
    if (normalizedMarkdown !== draftMarkdownRef.current) {
      replaceMarkdownSilently(normalizedMarkdown);
    }
    setBaseMarkdown(normalizedMarkdown);
    setAnchorSourceMarkdown(markdown);
    draftMarkdownRef.current = normalizedMarkdown;
    setDraftMarkdown(normalizedMarkdown);
    setBaseRevision(sourceRevision);
    setSaveError(undefined);
    setConflict(false);
    pendingSourceRef.current = undefined;
  }, [dirty, markdown, replaceMarkdownSilently, sourceRevision]);

  const persistMarkdown = useCallback(async (
    nextDraft: string,
    revisionBeforeSave: number,
  ): Promise<boolean> => {
    if (savingRef.current || readOnly) return false;
    savingRef.current = true;
    setSaving(true);
    setSaveError(undefined);

    try {
      const sourceBeforeSave = latestSourceRef.current;
      // Keep typing entirely under MDXEditor's control. Anchor repair belongs
      // at the persistence boundary so pressing Enter never reloads the whole
      // editor merely to restore hidden system metadata.
      const protectedDraft = protectWriterMarkdownAnchors(
        sourceBeforeSave.markdown,
        nextDraft,
      );
      const savedMarkdown = writerMarkdownForSave(protectedDraft);
      const result = await onSave(savedMarkdown, revisionBeforeSave);
      const savedRevision = typeof result === 'number'
        ? result
        : result?.revision ?? revisionBeforeSave;
      const persistedMarkdown = typeof result === 'object'
        ? result.markdown
        : savedMarkdown;
      const backendMarkdown = normalizeMarkdownForMdxEditor(persistedMarkdown);
      const hasNewerDraft = draftMarkdownRef.current !== nextDraft;
      setBaseMarkdown(backendMarkdown);
      if (!hasNewerDraft) {
        if (backendMarkdown !== draftMarkdownRef.current) {
          replaceMarkdownSilently(backendMarkdown);
        }
        draftMarkdownRef.current = backendMarkdown;
        setDraftMarkdown(backendMarkdown);
      }
      setBaseRevision(savedRevision);
      setAnchorSourceMarkdown(persistedMarkdown);
      staleSourceEchoRef.current = sourceBeforeSave;
      latestSourceRef.current = {
        markdown: persistedMarkdown,
        revision: savedRevision,
      };
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
  }, [onSave, readOnly, replaceMarkdownSilently, t]);

  const saveChanges = useCallback(async (): Promise<boolean> => {
    if (!dirty || savingRef.current || readOnly) return false;
    return persistMarkdown(draftMarkdown, baseRevision);
  }, [baseRevision, dirty, draftMarkdown, persistMarkdown, readOnly]);

  saveChangesRef.current = saveChanges;

  useEffect(() => {
    if (autoSaveTimerRef.current !== undefined) {
      window.clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = undefined;
    }
    if (!dirty || readOnly || saving || saveError || conflict) return undefined;

    autoSaveTimerRef.current = window.setTimeout(() => {
      autoSaveTimerRef.current = undefined;
      void saveChangesRef.current();
    }, MARKDOWN_AUTOSAVE_IDLE_MS);

    return () => {
      if (autoSaveTimerRef.current !== undefined) {
        window.clearTimeout(autoSaveTimerRef.current);
        autoSaveTimerRef.current = undefined;
      }
    };
  }, [conflict, dirty, draftMarkdown, readOnly, saveError, saving]);

  const handleMarkdownChange = useCallback((nextDraft: string) => {
    draftMarkdownRef.current = nextDraft;
    setDraftMarkdown(nextDraft);
    if (!conflictRef.current) setSaveError(undefined);
  }, []);

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
  const removableReferenceMarkdown = useMemo(() => {
    if (!selection?.supported) return null;
    const nextMarkdown = removeWriterMarkdownInternalReference(
      draftMarkdown,
      selection.paragraph?.textContent ?? '',
      selection.startOffset ?? -1,
      selection.text,
    );
    return nextMarkdown === draftMarkdown ? null : nextMarkdown;
  }, [draftMarkdown, selection]);
  const referenceDisabled = readOnly
    || !selection?.supported
    || saving
    || conflict
    || Boolean(removableReferenceMarkdown)
    || referenceTargets.length === 0;
  const removeReferenceDisabled = readOnly
    || !selection?.supported
    || saving
    || conflict
    || !removableReferenceMarkdown;
  const persistReferenceEdit = useCallback((
    nextDraft: string,
    selectionToRestore: MarkdownSelection,
  ) => {
    const editor = editorRef.current;
    if (!editor) return;
    const root = rootRef.current;
    const surface = root?.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const scrollTop = surface?.scrollTop;
    const restorePoint = root
      ? markdownSelectionRestorePoint(root, selectionToRestore)
      : null;
    editor.setMarkdown(nextDraft);
    window.requestAnimationFrame(() => {
      if (root && restorePoint) restoreMarkdownSelection(root, restorePoint);
      if (surface && scrollTop !== undefined) surface.scrollTop = scrollTop;
      draftMarkdownRef.current = nextDraft;
      setDraftMarkdown(nextDraft);
      void persistMarkdown(nextDraft, baseRevision);
    });
    dismissSelectionToolbar();
  }, [baseRevision, dismissSelectionToolbar, persistMarkdown]);
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
    persistReferenceEdit(nextDraft, referenceSelection);
  }, [persistReferenceEdit, readOnly]);

  const removeCrossReference = useCallback(() => {
    const editor = editorRef.current;
    const referenceSelection = referenceSelectionRef.current ?? selection;
    if (
      !editor
      || !referenceSelection?.supported
      || savingRef.current
      || conflictRef.current
      || readOnly
    ) return;
    const currentMarkdown = normalizeMarkdownForMdxEditor(editor.getMarkdown());
    const nextDraft = removeWriterMarkdownInternalReference(
      currentMarkdown,
      referenceSelection.paragraph?.textContent ?? '',
      referenceSelection.startOffset ?? -1,
      referenceSelection.text,
    );
    if (nextDraft === currentMarkdown) return;
    persistReferenceEdit(nextDraft, referenceSelection);
  }, [persistReferenceEdit, readOnly, selection]);

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
      className={`writer-markdown-editor writer-markdown-editor--width-${pageWidth}${
        outlineOpen ? ' writer-markdown-editor--outline-open' : ''
      }${
        selectionToolbar ? ' writer-markdown-editor--selection-toolbar-visible' : ''
      }`}
      aria-label={t('chat.writerMarkdown.documentRegion')}
      ref={rootRef}
      style={selectionToolbarStyle}
      onMouseDownCapture={(event) => {
        if (!internalWriterReferenceLink(event.target)) return;
        event.stopPropagation();
      }}
      onClickCapture={(event) => {
        const link = internalWriterReferenceLink(event.target);
        if (!link) return;
        event.preventDefault();
        event.stopPropagation();
        const browserSelection = globalThis.getSelection();
        if (
          browserSelection
          && !browserSelection.isCollapsed
          && (
            link.contains(browserSelection.anchorNode)
            || link.contains(browserSelection.focusNode)
          )
        ) {
          recordSelection();
          return;
        }
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
          <div
            className='writer-markdown-editor__display-toolbar'
            role='toolbar'
            aria-label={t('chat.writerIR.displaySettings')}
            onClick={(event) => event.stopPropagation()}
          >
            <div className='writer-markdown-editor__width-control'>
              <span className='writer-markdown-editor__width-label'>
                {t('chat.writerIR.pageWidth')}
              </span>
              <div
                className='writer-markdown-editor__width-options'
                role='group'
                aria-label={t('chat.writerIR.pageWidth')}
              >
                {(['default', 'wide'] as const).map((width) => (
                  <button
                    key={width}
                    type='button'
                    className='writer-markdown-editor__width-option'
                    aria-pressed={pageWidth === width}
                    onClick={() => setPageWidth(width)}
                  >
                    {t(`chat.writerIR.pageWidths.${width}`)}
                  </button>
                ))}
              </div>
            </div>
          </div>
          <MDXEditor
            ref={editorRef}
            className='writer-markdown-editor__surface'
            markdown={baseMarkdown}
            readOnly={readOnly}
            onChange={handleMarkdownChange}
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
              imagePlugin({
                imagePreviewHandler: resolveMarkdownImageUrlAsync,
              }),
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
                        open={referenceDropdownOpen}
                        onOpenChange={(open: boolean) => {
                          setReferenceDropdownOpen(open && !referenceDisabled);
                        }}
                        menu={{
                          items: referenceTargets.map((target) => ({
                            key: target.anchorId,
                            label: (
                              <span
                                className='writer-markdown-editor__reference-option'
                                title={target.label}
                              >
                                <span
                                  className='writer-markdown-editor__reference-option-icon'
                                  aria-hidden='true'
                                >
                                  {target.type === 'image'
                                    ? <PictureOutlined />
                                    : <FontSizeOutlined />}
                                </span>
                                <span className='writer-markdown-editor__reference-option-label'>
                                  {target.label}
                                </span>
                              </span>
                            ),
                          })),
                          onClick: ({ key }: { key: string | number }) => {
                            applyCrossReference(String(key));
                          },
                        }}
                      >
                        <button
                          type='button'
                          className='writer-markdown-editor__reference-select'
                          disabled={referenceDisabled}
                          aria-label={t('chat.writerIR.crossReference')}
                          aria-haspopup='menu'
                          aria-expanded={referenceDropdownOpen}
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
                      <button
                        type='button'
                        className={
                          'writer-markdown-editor__reference-select '
                          + 'writer-markdown-editor__reference-remove'
                        }
                        disabled={removeReferenceDisabled}
                        aria-label={t('chat.writerIR.removeCrossReference')}
                        title={t('chat.writerIR.removeCrossReference')}
                        onMouseDown={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          if (selection?.supported) referenceSelectionRef.current = selection;
                        }}
                        onClick={removeCrossReference}
                      >
                        <DisconnectOutlined aria-hidden />
                      </button>
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
