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
  CommentOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  MoreOutlined,
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
  type MouseEvent as ReactMouseEvent,
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
import type {
  RewriteSelectionPreview,
  WriterHeadingNumberingMode,
  WriterNumberingState,
  WriterNumberingUpdate,
} from '@/modules/chat/utils/request';
import { resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';
import { WriterHeadingNumberingMenu } from './WriterHeadingNumberingMenu';
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
  type WriterMarkdownOutlineItem,
} from './writerMarkdownAnchors';
import './MarkdownArtifactEditor.scss';

/** Idle debounce after the latest edit before a silent draft save. */
const MARKDOWN_AUTOSAVE_IDLE_MS = 1_000;
const CHAT_PARAGRAPH_HOVER_MS = 1_000;

function WriterAnchorEditor(props: JsxEditorProps) {
  const id = props.mdastNode.attributes.find(
    (attribute) => attribute.type === 'mdxJsxAttribute' && attribute.name === 'id',
  )?.value;
  if (
    typeof id === 'string'
    && (id.startsWith('block-') || id.startsWith('writer-page-marker-'))
  ) {
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

function attachOutlineInstructionControl(
  heading: HTMLElement,
  item: WriterMarkdownOutlineItem,
  expanded: boolean,
  onToggle: () => void,
  labels: {
    instructions: string;
    targetChars: string;
    contextRelations: string;
    writingSubtasks: string;
    subtaskType: (type: string) => string;
  },
): void {
  const instructions = item.instructions;
  if (!instructions || heading.querySelector('[data-writer-outline-control]')) return;

  const panelId = `writer-outline-instructions-${item.anchorId}`;
  const button = globalThis.document.createElement('button');
  button.type = 'button';
  button.className = 'writer-markdown-editor__heading-instruction-toggle';
  button.dataset.writerOutlineControl = item.anchorId;
  button.setAttribute('contenteditable', 'false');
  button.setAttribute('aria-expanded', String(expanded));
  button.setAttribute('aria-controls', panelId);
  button.textContent = labels.instructions;

  const panel = globalThis.document.createElement('div');
  panel.id = panelId;
  panel.className = 'writer-markdown-editor__heading-instructions';
  panel.dataset.writerOutlinePanel = item.anchorId;
  panel.setAttribute('contenteditable', 'false');
  panel.hidden = !expanded;

  const addRow = (label: string, values: string[]) => {
    if (values.length === 0) return;
    const row = globalThis.document.createElement('div');
    row.className = 'writer-markdown-editor__heading-instruction-row';
    const strong = globalThis.document.createElement('strong');
    strong.textContent = label;
    row.append(strong);
    if (values.length === 1) {
      const value = globalThis.document.createElement('span');
      value.textContent = values[0];
      row.append(value);
    } else {
      const list = globalThis.document.createElement('ul');
      values.forEach((text) => {
        const entry = globalThis.document.createElement('li');
        entry.textContent = text;
        list.append(entry);
      });
      row.append(list);
    }
    panel.append(row);
  };

  if (instructions.target_chars) {
    addRow(labels.targetChars, [String(instructions.target_chars)]);
  }
  addRow(
    labels.contextRelations,
    instructions.context_relations.map((relation) => (
      relation.guidance
      || [relation.relation, relation.target_node_id].filter(Boolean).join(' / ')
      || '-'
    )),
  );
  addRow(
    labels.writingSubtasks,
    instructions.subtasks.map(
      (subtask) => `${labels.subtaskType(subtask.subtask_type)} ${subtask.question}`,
    ),
  );

  button.addEventListener('click', () => {
    const expanded = button.getAttribute('aria-expanded') !== 'true';
    button.setAttribute('aria-expanded', String(expanded));
    panel.hidden = !expanded;
    onToggle();
  });
  heading.append(button);
  heading.insertAdjacentElement('afterend', panel);
}

function setOutlineInstructionControlsExpanded(root: HTMLElement, expanded: boolean): void {
  root.querySelectorAll<HTMLButtonElement>('[data-writer-outline-control]')
    .forEach((button) => button.setAttribute('aria-expanded', String(expanded)));
  root.querySelectorAll<HTMLElement>('[data-writer-outline-panel]')
    .forEach((panel) => { panel.hidden = !expanded; });
}

function internalWriterReferenceLink(target: EventTarget | null): HTMLAnchorElement | null {
  return target instanceof Element
    ? target.closest<HTMLAnchorElement>('a[href^="#block-"]')
    : null;
}

function sourceReferenceLink(target: EventTarget | null): HTMLAnchorElement | null {
  return target instanceof Element
    ? target.closest<HTMLAnchorElement>(
      'a[href^="#source-"], a[href^="#user-content-source-"]',
    )
    : null;
}

function sourceReferenceId(link: HTMLAnchorElement): string {
  const href = link.getAttribute('href') ?? '';
  const match = /^#(?:user-content-)?source-(.+)$/.exec(href);
  return match ? decodeURIComponent(match[1]) : '';
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

function mdxMarkupLength(line: string, start: number): number {
  const markup = line.slice(start).match(
    /^(?:<!--.*?-->|<\/?[A-Za-z][A-Za-z0-9:._-]*(?=[\s/>])[^<>]*>|<(?:https?:\/\/|mailto:)[^<>\s]+>|<[^<>\s@]+@[^<>\s@]+>)/i,
  );
  return markup?.[0].length ?? 0;
}

function escapeMdxPlainTextInLine(line: string): string {
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

    if (inlineCodeFence === 0 && line[index] === '<' && !isEscaped(line, index)) {
      const markupLength = mdxMarkupLength(line, index);
      if (markupLength > 0) {
        result += line.slice(index, index + markupLength);
        index += markupLength;
        continue;
      }
      result += '\\';
    }
    if (
      (line[index] === '{' || line[index] === '}')
      && inlineCodeFence === 0
      && !isEscaped(line, index)
    ) {
      // Workflow artifacts can contain inline JSON such as
      // `配图：{"reference_image_index": 0}`. MDX otherwise parses the braces
      // as a JavaScript expression and replaces the whole document with an
      // empty editor when that expression is invalid.
      result += '\\';
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
    return fenceCharacter ? line : escapeMdxPlainTextInLine(line);
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
  applyPreview?: () => Promise<number | undefined>;
}

export interface MarkdownSourceReferencePresentation {
  citationId: string;
  label: string;
  title: string;
  href: string;
  faviconUrl?: string;
}

interface MarkdownSourceReferencePopover {
  source: MarkdownSourceReferencePresentation;
  left: number;
  top: number;
  placement: 'top' | 'bottom';
}

export type MarkdownSaveMode = 'draft' | 'checkpoint';

interface MarkdownArtifactEditorProps {
  markdown: string;
  numbering?: WriterNumberingState;
  sourceRevision: number;
  maxHeight?: number;
  /** Compact chat presentation hides Workflow-only document chrome. */
  presentation?: 'workflow' | 'chat';
  readOnly?: boolean;
  /** Stable key used to register flush-before-retry/continue with WorkflowPanel. */
  editingKey?: string;
  onSave: (
    markdown: string,
    baseRevision: number,
    mode?: MarkdownSaveMode,
    numberingUpdate?: WriterNumberingUpdate,
  ) => Promise<number | { markdown: string; revision?: number } | undefined>;
  onRefresh?: () => void;
  onDownload?: () => void;
  /** Reports the current draft so the write-back action can compare it with its Feishu baseline. */
  onContentChange?: (markdown: string) => void;
  /** Chat-only action that sends the current selection to the composer as a citation. */
  onCiteSelection?: (text: string) => void;
  /** Chat-only action that opens the source represented by an inline citation. */
  onOpenSourceReference?: (citationId: string) => void;
  /** Chat-only display metadata for inline source citations. */
  sourceReferences?: MarkdownSourceReferencePresentation[];
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

function markdownNumberingMarkerClicked(heading: HTMLElement, clientX: number): boolean {
  if (!heading.dataset.writerNumberingLabel) return false;
  const start = markdownTextBoundary(heading, 0);
  const range = globalThis.document.createRange();
  range.setStart(start.node, start.offset);
  range.collapse(true);
  const textStart = range.getBoundingClientRect().left;
  return clientX >= heading.getBoundingClientRect().left && clientX < textStart;
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
  numbering,
  sourceRevision,
  maxHeight,
  presentation = 'workflow',
  readOnly = false,
  editingKey,
  onSave,
  onRefresh,
  onDownload,
  onContentChange,
  onCiteSelection,
  onOpenSourceReference,
  sourceReferences = [],
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
  const chatPresentation = presentation === 'chat';
  const [baseMarkdown, setBaseMarkdown] = useState(() => normalizeMarkdownForMdxEditor(markdown));
  const [draftMarkdown, setDraftMarkdown] = useState(() => normalizeMarkdownForMdxEditor(markdown));
  const [anchorSourceMarkdown, setAnchorSourceMarkdown] = useState(markdown);
  const [baseRevision, setBaseRevision] = useState(sourceRevision);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [renderErrorSource, setRenderErrorSource] = useState<string>();
  const [conflict, setConflict] = useState(false);
  const [outlineOpen, setOutlineOpen] = useState(false);
  const [outlineInstructionsExpanded, setOutlineInstructionsExpanded] = useState(false);
  const [pageWidth, setPageWidth] = useState<'default' | 'wide'>('default');
  const [selection, setSelection] = useState<MarkdownSelection | null>(null);
  const [selectionToolbar, setSelectionToolbar] = useState<FloatingToolbarAnchor | null>(null);
  const [numberingMenu, setNumberingMenu] = useState<{
    anchorId: string;
    x: number;
    y: number;
  } | null>(null);
  const [referenceDropdownOpen, setReferenceDropdownOpen] = useState(false);
  const [compactActionsOpen, setCompactActionsOpen] = useState(false);
  const [rewriteLayer, setRewriteLayer] = useState<HTMLDivElement | null>(null);
  const [rewriteSelectionPinned, setRewriteSelectionPinned] = useState(false);
  const [sourceReferencePopover, setSourceReferencePopover] = useState<
    MarkdownSourceReferencePopover | null
  >(null);
  const rootRef = useRef<HTMLElement>(null);
  const editorRef = useRef<MDXEditorMethods>(null);
  const referenceSelectionRef = useRef<MarkdownSelection | null>(null);
  const capturedSelectionRangeRef = useRef<Range | null>(null);
  const toolbarInteractionRef = useRef(false);
  const paragraphHoverTimerRef = useRef<number | undefined>(undefined);
  const paragraphHoverTargetRef = useRef<HTMLElement | null>(null);
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
  const saveChangesRef = useRef<(mode?: MarkdownSaveMode) => Promise<boolean>>(async () => true);
  const outlineId = useId();
  const sourceReferenceMap = useMemo(
    () => new Map(sourceReferences.map((source) => [source.citationId, source])),
    [sourceReferences],
  );
  const sourceReferencePopoverId = useId();

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
  const hasOutline = Boolean(markdownOutline.title);
  const referenceTargets = useMemo(
    () => collectWriterMarkdownReferenceTargets(materializedDraftMarkdown),
    [materializedDraftMarkdown],
  );
  const outlineBaseLevel = Math.min(
    ...markdownOutline.items.map((item) => item.level),
    6,
  );
  const hasOutlineInstructions = markdownOutline.items.some((item) => Boolean(item.instructions));
  const syncOutlineInstructionsExpanded = useCallback(() => {
    const controls = Array.from(
      rootRef.current?.querySelectorAll<HTMLButtonElement>('[data-writer-outline-control]') ?? [],
    );
    setOutlineInstructionsExpanded(
      controls.length > 0 && controls.every(
        (button) => button.getAttribute('aria-expanded') === 'true',
      ),
    );
  }, []);
  const expandAllOutlineInstructions = useCallback(() => {
    if (rootRef.current) setOutlineInstructionControlsExpanded(rootRef.current, true);
    setOutlineInstructionsExpanded(true);
  }, []);
  const collapseAllOutlineInstructions = useCallback(() => {
    if (rootRef.current) setOutlineInstructionControlsExpanded(rootRef.current, false);
    setOutlineInstructionsExpanded(false);
  }, []);
  dirtyRef.current = dirty;
  draftMarkdownRef.current = draftMarkdown;
  savingRef.current = saving;
  conflictRef.current = conflict;

  useEffect(() => {
    onContentChange?.(
      dirty
        ? writerMarkdownForSave(materializedDraftMarkdown)
        : anchorSourceMarkdown,
    );
  }, [anchorSourceMarkdown, dirty, materializedDraftMarkdown, onContentChange]);

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
          delete element.dataset.writerHeadingMode;
          delete element.dataset.writerNumberingLabel;
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
        if (anchor.type === 'heading') {
          const nodeId = anchor.anchorId.slice('block-'.length);
          target.dataset.writerHeadingMode = numbering?.entries[nodeId]?.mode ?? 'ordered';
          const label = numbering?.entries[nodeId]?.label;
          if (label) target.dataset.writerNumberingLabel = label;
        }
      });
      markdownOutline.items.forEach((item) => {
        const heading = Array.from(headings).find(
          (candidate) => candidate.id === item.anchorId,
        );
        if (!heading) return;
        attachOutlineInstructionControl(heading, item, false, syncOutlineInstructionsExpanded, {
          instructions: t('chat.writerIR.outlineInstructions'),
          targetChars: t('chat.writerIR.targetChars'),
          contextRelations: t('chat.writerIR.contextRelations'),
          writingSubtasks: t('chat.writerIR.writingSubtasks'),
          subtaskType: (type) => t(`chat.writerIR.subtaskTypes.${type}`, {
            defaultValue: type,
          }),
        });
      });
      if (chatPresentation) {
        editable.querySelectorAll<HTMLAnchorElement>(
          'a[href^="#source-"], a[href^="#user-content-source-"]',
        ).forEach((link) => {
          const presentation = sourceReferenceMap.get(sourceReferenceId(link));
          const fallbackLabel = link.textContent?.trim() ?? '';
          const label = presentation?.label || fallbackLabel;
          link.dataset.writerSourceCitation = 'true';
          link.dataset.writerSourceLabel = label;
          link.dataset.writerSourceInitial = label.slice(0, 1).toUpperCase();
          link.setAttribute('contenteditable', 'false');
          link.setAttribute('role', 'button');
          link.tabIndex = 0;
          if (presentation?.faviconUrl) {
            link.dataset.writerSourceHasIcon = 'true';
            link.style.setProperty(
              '--writer-source-icon',
              `url("${presentation.faviconUrl}")`,
            );
          } else {
            delete link.dataset.writerSourceHasIcon;
            link.style.removeProperty('--writer-source-icon');
          }
          link.removeAttribute('title');
          link.setAttribute(
            'aria-label',
            `${t('chat.references')} ${label}`.trim(),
          );
        });
      }
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
  }, [
    chatPresentation,
    markdownOutline,
    materializedDraftMarkdown,
    numbering,
    sourceReferenceMap,
    syncOutlineInstructionsExpanded,
    t,
  ]);

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
    setCompactActionsOpen(false);
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
      !root
      || !surface
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

    const toolbarRect = toolbar.getBoundingClientRect();
    const viewportAnchor = floatingToolbarAnchor({
      selectionRect,
      containerRect: surfaceRect,
      // The page may be zoomed or scaled. Rect dimensions match the fixed
      // toolbar's actual viewport size; offsetHeight can underestimate it and
      // let the toolbar overlap the selected line.
      toolbarWidth: toolbarRect.width || toolbar.offsetWidth,
      toolbarHeight: toolbarRect.height || toolbar.offsetHeight,
    });
    // Keep the toolbar in the scrolling surface's coordinate system. Desktop
    // Chromium otherwise resolves fixed descendants differently around named
    // containers and can shift most actions outside the clipped editor.
    const nextAnchor = {
      ...viewportAnchor,
      top: viewportAnchor.top - surfaceRect.top - surface.clientTop + surface.scrollTop,
      left: viewportAnchor.left - surfaceRect.left - surface.clientLeft + surface.scrollLeft,
    };
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
    if (
      !nextSelection
      && (
        toolbarInteractionRef.current
        ||
        isMarkdownToolbarInteractionTarget(document.activeElement)
        || isMarkdownToolbarDropdownOpen()
      )
    ) {
      return;
    }
    if (nextSelection?.supported || nextSelection?.internalReference) {
      referenceSelectionRef.current = nextSelection;
      const browserSelection = globalThis.getSelection();
      if (browserSelection?.rangeCount) {
        capturedSelectionRangeRef.current = browserSelection.getRangeAt(0).cloneRange();
      }
    } else {
      referenceSelectionRef.current = null;
      capturedSelectionRangeRef.current = null;
    }
    setSelection(nextSelection);
    if (!showToolbar) return;
    selectionToolbarDismissedRef.current = false;
    updateSelectionToolbar();
  }, [updateSelectionToolbar]);

  const cancelParagraphHover = useCallback(() => {
    if (paragraphHoverTimerRef.current !== undefined) {
      window.clearTimeout(paragraphHoverTimerRef.current);
      paragraphHoverTimerRef.current = undefined;
    }
    paragraphHoverTargetRef.current = null;
  }, []);

  const handleChatParagraphHover = useCallback((event: ReactMouseEvent<HTMLElement>) => {
    if (!chatPresentation || readOnly || toolbarInteractionRef.current) return;
    const root = rootRef.current;
    const editable = root?.querySelector<HTMLElement>(
      '.mdxeditor-root-contenteditable [contenteditable="true"]',
    );
    if (!editable) return;
    const editableRect = editable.getBoundingClientRect();
    const paragraph = Array.from(editable.querySelectorAll<HTMLElement>('p')).find((candidate) => {
      const rect = candidate.getBoundingClientRect();
      const inVerticalBand = event.clientY >= rect.top && event.clientY <= rect.bottom;
      const inLeadingGutter = event.clientX >= editableRect.left
        && event.clientX <= rect.left + 32;
      return inVerticalBand && inLeadingGutter;
    }) ?? null;
    if (!paragraph) {
      cancelParagraphHover();
      return;
    }
    if (paragraphHoverTargetRef.current === paragraph) return;
    cancelParagraphHover();
    paragraphHoverTargetRef.current = paragraph;
    paragraphHoverTimerRef.current = window.setTimeout(() => {
      paragraphHoverTimerRef.current = undefined;
      if (!paragraph.isConnected || paragraphHoverTargetRef.current !== paragraph) return;
      const range = document.createRange();
      range.selectNodeContents(paragraph);
      const browserSelection = globalThis.getSelection();
      browserSelection?.removeAllRanges();
      browserSelection?.addRange(range);
      recordSelection();
    }, CHAT_PARAGRAPH_HOVER_MS);
  }, [cancelParagraphHover, chatPresentation, readOnly, recordSelection]);

  useEffect(() => cancelParagraphHover, [cancelParagraphHover]);

  const editorTranslation = useCallback((
    key: string,
    defaultValue: string,
    interpolations?: Record<string, unknown>,
  ) => {
    switch (key) {
      case 'toolbar.blockTypes.paragraph':
        return t('chat.writerMarkdown.blockTypes.paragraph');
      case 'toolbar.blockTypes.quote':
        return t('chat.writerMarkdown.blockTypes.quote');
      case 'toolbar.blockTypes.heading':
        return t('chat.writerMarkdown.blockTypes.heading', {
          level: interpolations?.level,
        });
      case 'toolbar.blockTypeSelect.selectBlockTypeTooltip':
        return t('chat.writerMarkdown.blockTypes.selectTooltip');
      case 'toolbar.blockTypeSelect.placeholder':
        return t('chat.writerMarkdown.blockTypes.placeholder');
      default:
        return defaultValue;
    }
  }, [t]);

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
    setRenderErrorSource(undefined);
    setConflict(false);
    pendingSourceRef.current = undefined;
  }, [dirty, markdown, replaceMarkdownSilently, sourceRevision]);

  const persistMarkdown = useCallback(async (
    nextDraft: string,
    revisionBeforeSave: number,
    mode: MarkdownSaveMode = 'draft',
    numberingUpdate?: WriterNumberingUpdate,
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
      const protectedDraft = numberingUpdate && !dirtyRef.current
        ? sourceBeforeSave.markdown
        : protectWriterMarkdownAnchors(sourceBeforeSave.markdown, nextDraft);
      const savedMarkdown = writerMarkdownForSave(protectedDraft);
      const result = await onSave(savedMarkdown, revisionBeforeSave, mode, numberingUpdate);
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
      setRenderErrorSource(undefined);
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

  const saveChanges = useCallback(async (mode: MarkdownSaveMode = 'draft'): Promise<boolean> => {
    if (!dirty || savingRef.current || readOnly) return false;
    return persistMarkdown(draftMarkdown, baseRevision, mode);
  }, [baseRevision, dirty, draftMarkdown, persistMarkdown, readOnly]);

  saveChangesRef.current = saveChanges;

  useEffect(() => {
    if (!chatPresentation || readOnly) return undefined;
    const flush = () => {
      if (
        dirtyRef.current
        && !savingRef.current
        && !conflictRef.current
      ) {
        void saveChangesRef.current();
      }
    };
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'hidden') flush();
    };
    window.addEventListener('pagehide', flush);
    window.addEventListener('beforeunload', flush);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.removeEventListener('pagehide', flush);
      window.removeEventListener('beforeunload', flush);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      flush();
    };
  }, [chatPresentation, readOnly]);

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
      return saveChangesRef.current('checkpoint');
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
    || (dirty && !chatPresentation)
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
  const requestPolish = useCallback(async () => {
    if (polishDisabled || !selection || !onRewriteSelection) return;
    if (dirtyRef.current && chatPresentation) {
      const saved = await saveChangesRef.current();
      if (!saved) return;
    }
    const browserSelection = globalThis.getSelection();
    if (browserSelection?.rangeCount && !browserSelection.isCollapsed) {
      pinnedRewriteRangeRef.current = browserSelection.getRangeAt(0).cloneRange();
    } else {
      const capturedRange = capturedSelectionRangeRef.current;
      pinnedRewriteRangeRef.current = capturedRange ? capturedRange.cloneRange() : null;
    }
    setRewriteSelectionPinned(Boolean(pinnedRewriteRangeRef.current));
    onRewriteSelection(selection);
    dismissSelectionToolbar();
  }, [chatPresentation, dismissSelectionToolbar, onRewriteSelection, polishDisabled, selection]);
  const citeSelection = useCallback(() => {
    const text = selection?.text.trim();
    if (!text || !onCiteSelection) return;
    onCiteSelection(text);
    globalThis.getSelection()?.removeAllRanges();
    dismissSelectionToolbar();
  }, [dismissSelectionToolbar, onCiteSelection, selection]);
  const numberingNodeId = numberingMenu?.anchorId.slice('block-'.length);
  const currentMarkdownNumbering = numberingNodeId
    ? numbering?.entries[numberingNodeId]
    : undefined;
  const markdownNumberingMode: WriterHeadingNumberingMode =
    currentMarkdownNumbering?.mode ?? 'ordered';
  const orderedMarkdownNumberingStyle = numbering?.ordered_style ?? 'hierarchical';
  const applyMarkdownNumbering = (update: WriterNumberingUpdate) => {
    if (!numberingMenu || readOnly || savingRef.current || conflictRef.current) return;
    void persistMarkdown(draftMarkdownRef.current, baseRevision, 'draft', update);
  };
  const removableReferenceMarkdown = useMemo(() => {
    if (!selection) return null;
    const nextMarkdown = removeWriterMarkdownInternalReference(
      draftMarkdown,
      selection.paragraph?.textContent ?? '',
      selection.startOffset ?? -1,
      selection.text,
      selection.internalReference,
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
      || !referenceSelection
      || (!referenceSelection.supported && !referenceSelection.internalReference)
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
      referenceSelection.internalReference,
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

  const showSourceReferencePopover = useCallback((link: HTMLAnchorElement) => {
    const source = sourceReferenceMap.get(sourceReferenceId(link));
    if (!source) return;
    const rect = link.getBoundingClientRect();
    const placement = rect.bottom + 120 <= window.innerHeight ? 'bottom' : 'top';
    const popoverHalfWidth = Math.min(180, Math.max(0, (window.innerWidth - 32) / 2));
    setSourceReferencePopover({
      source,
      left: Math.min(
        Math.max(rect.left + rect.width / 2, 16 + popoverHalfWidth),
        window.innerWidth - 16 - popoverHalfWidth,
      ),
      top: placement === 'bottom' ? rect.bottom + 8 : rect.top - 8,
      placement,
    });
  }, [sourceReferenceMap]);

  const selectionToolbarStyle = selectionToolbar
    ? {
      '--writer-markdown-selection-toolbar-top': `${selectionToolbar.top}px`,
      '--writer-markdown-selection-toolbar-left': `${selectionToolbar.left}px`,
      '--writer-markdown-selection-toolbar-max-width': `${selectionToolbar.maxWidth}px`,
    } as CSSProperties
    : undefined;
  const editorStyle: CSSProperties | undefined = selectionToolbarStyle || maxHeight !== undefined
    ? { ...selectionToolbarStyle, ...(maxHeight !== undefined ? { maxHeight } : {}) }
    : undefined;

  return (
    <section
      className={`writer-markdown-editor writer-markdown-editor--width-${pageWidth}${
        outlineOpen && hasOutline ? ' writer-markdown-editor--outline-open' : ''
      }${
        !chatPresentation && !hasOutline ? ' writer-markdown-editor--no-outline' : ''
      }${
        selectionToolbar ? ' writer-markdown-editor--selection-toolbar-visible' : ''
      }${chatPresentation ? ' writer-markdown-editor--chat' : ''}`}
      aria-label={t('chat.writerMarkdown.documentRegion')}
      ref={rootRef}
      style={editorStyle}
      onBlurCapture={() => {
        if (!chatPresentation || readOnly) return;
        window.setTimeout(() => {
          const root = rootRef.current;
          if (!root?.contains(document.activeElement)) {
            void saveChangesRef.current();
          }
        }, 0);
      }}
      onMouseDownCapture={(event) => {
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest('.mdxeditor-toolbar')) {
          // Browsers dispatch selectionchange before toolbar focus/click. Keep
          // the editor selection alive until MDXEditor has applied its command.
          toolbarInteractionRef.current = true;
        }
        if (chatPresentation && onOpenSourceReference && sourceReferenceLink(event.target)) {
          event.preventDefault();
          event.stopPropagation();
          return;
        }
        if (!internalWriterReferenceLink(event.target)) return;
        event.preventDefault();
        event.stopPropagation();
      }}
      onMouseOverCapture={(event) => {
        if (!chatPresentation) return;
        const sourceLink = sourceReferenceLink(event.target);
        if (sourceLink) showSourceReferencePopover(sourceLink);
      }}
      onMouseOutCapture={(event) => {
        if (!sourceReferenceLink(event.target)) return;
        const nextTarget = event.relatedTarget;
        if (nextTarget instanceof Node && sourceReferenceLink(nextTarget)) return;
        setSourceReferencePopover(null);
      }}
      onClickCapture={(event) => {
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest('.mdxeditor-toolbar')) {
          window.setTimeout(() => {
            toolbarInteractionRef.current = false;
          }, 0);
        }
        const sourceLink = chatPresentation ? sourceReferenceLink(event.target) : null;
        if (sourceLink && onOpenSourceReference) {
          event.preventDefault();
          event.stopPropagation();
          const citationId = sourceReferenceId(sourceLink);
          if (citationId) onOpenSourceReference(citationId);
          return;
        }
        const link = internalWriterReferenceLink(event.target);
        const heading = target?.closest<HTMLElement>('h1, h2, h3, h4, h5, h6');
        if (heading && /^h[2-6]$/i.test(heading.tagName)) {
          const rawAnchorId = heading.dataset.writerSystemAnchor === 'true'
            ? heading.id
            : heading.id || heading.dataset.writerSystemAnchor || '';
          const anchorId = rawAnchorId.startsWith('block-')
            ? rawAnchorId
            : rawAnchorId ? `block-${rawAnchorId}` : '';
          const numberingMarker = markdownNumberingMarkerClicked(heading, event.clientX);
          const unorderedControl = heading.dataset.writerHeadingMode === 'unordered'
            && event.clientX < heading.getBoundingClientRect().left;
          if (anchorId && (numberingMarker || unorderedControl)) {
            event.preventDefault();
            event.stopPropagation();
            setNumberingMenu({
              anchorId,
              x: event.clientX,
              y: event.clientY,
            });
            return;
          }
        }
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
      onMouseMove={handleChatParagraphHover}
      onMouseLeave={(event) => {
        setSourceReferencePopover(null);
        const nextTarget = event.relatedTarget;
        if (!(nextTarget instanceof Node) || !event.currentTarget.contains(nextTarget)) {
          cancelParagraphHover();
        }
      }}
      onKeyUp={(event) => {
        if (event.key !== 'Escape') recordSelection();
      }}
      onKeyDownCapture={(event) => {
        if (!chatPresentation || !onOpenSourceReference) return;
        if (event.key !== 'Enter' && event.key !== ' ') return;
        const sourceLink = sourceReferenceLink(event.target);
        if (!sourceLink) return;
        event.preventDefault();
        event.stopPropagation();
        const citationId = sourceReferenceId(sourceLink);
        if (citationId) onOpenSourceReference(citationId);
      }}
    >
      {numberingMenu && (
        <WriterHeadingNumberingMenu
          x={numberingMenu.x}
          y={numberingMenu.y}
          targetId={numberingNodeId ?? ''}
          mode={markdownNumberingMode}
          orderedStyle={orderedMarkdownNumberingStyle}
          restart={Boolean(currentMarkdownNumbering?.restart)}
          disabled={readOnly || saving || conflict}
          onApply={applyMarkdownNumbering}
          onClose={() => setNumberingMenu(null)}
        />
      )}
      {sourceReferencePopover && (
        <div
          id={sourceReferencePopoverId}
          className='writer-markdown-editor__source-popover'
          data-placement={sourceReferencePopover.placement}
          role='tooltip'
          style={{
            left: sourceReferencePopover.left,
            top: sourceReferencePopover.top,
          }}
        >
          <div className='writer-markdown-editor__source-popover-heading'>
            <span
              className='writer-markdown-editor__source-popover-icon'
              data-has-icon={Boolean(sourceReferencePopover.source.faviconUrl)}
              style={sourceReferencePopover.source.faviconUrl
                ? {
                  '--writer-source-popover-icon':
                    `url("${sourceReferencePopover.source.faviconUrl}")`,
                } as CSSProperties
                : undefined}
              aria-hidden='true'
            >
              {sourceReferencePopover.source.label.slice(0, 1).toUpperCase()}
            </span>
            <strong>{sourceReferencePopover.source.title}</strong>
          </div>
          <span className='writer-markdown-editor__source-popover-link'>
            {sourceReferencePopover.source.href}
          </span>
        </div>
      )}
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
              onClick={() => void saveChanges()}
              disabled={saving || !dirty}
            >
              {t('common.retry')}
            </button>
          )}
        </div>
      )}

      <div className='writer-markdown-editor__document-layout'>
        {!chatPresentation && hasOutline && <aside
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
                  {markdownOutline.items.map((item) => {
                    const nodeId = item.anchorId.slice('block-'.length);
                    const numberingLabel = numbering?.entries[nodeId]?.label;
                    const displayLabel = numberingLabel
                      ? `${numberingLabel} ${item.label}`
                      : item.label;
                    return (
                      <li key={item.anchorId}>
                        <button
                          type='button'
                          className={
                            `writer-markdown-editor__outline-link `
                            + `writer-markdown-editor__outline-link--level-${
                              Math.max(1, item.level - outlineBaseLevel + 1)
                            }`
                          }
                          title={displayLabel}
                          aria-label={t('chat.writerIR.jumpToHeading', { title: displayLabel })}
                          onClick={() => navigateToOutlineItem(item.anchorId)}
                        >
                          {numberingLabel && (
                            <span className='writer-markdown-editor__outline-number'>
                              {numberingLabel}
                            </span>
                          )}
                          <span>{item.label}</span>
                        </button>
                      </li>
                    );
                  })}
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
        </aside>}
        <div className='writer-markdown-editor__main'>
          {!chatPresentation && <div
            className='writer-markdown-editor__display-toolbar'
            role='toolbar'
            aria-label={t('chat.writerIR.displaySettings')}
            onClick={(event) => event.stopPropagation()}
          >
            {hasOutlineInstructions && (
              <button
                type='button'
                className='writer-markdown-editor__outline-instructions-all'
                aria-pressed={outlineInstructionsExpanded}
                onClick={outlineInstructionsExpanded
                  ? collapseAllOutlineInstructions
                  : expandAllOutlineInstructions}
              >
                {t(outlineInstructionsExpanded
                  ? 'chat.writerIR.collapseAllOutlineInstructions'
                  : 'chat.writerIR.expandAllOutlineInstructions')}
              </button>
            )}
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
          </div>}
          {renderErrorSource !== undefined ? (
            <div
              className='writer-markdown-editor__parse-fallback'
              role='alert'
            >
              <span className='writer-markdown-editor__parse-fallback-message'>
                {t('chat.writerMarkdown.renderFallback')}
              </span>
              <pre>{renderErrorSource}</pre>
            </div>
          ) : <MDXEditor
            ref={editorRef}
            className='writer-markdown-editor__surface'
            markdown={baseMarkdown}
            translation={editorTranslation}
            readOnly={readOnly}
            onChange={handleMarkdownChange}
            onError={({ source }: { source: string }) => setRenderErrorSource(source)}
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
                      {chatPresentation && onCiteSelection && (
                        <button
                          type='button'
                          className='writer-markdown-editor__polish-action'
                          disabled={!selection?.text.trim()}
                          title={t('chat.cite')}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={citeSelection}
                        >
                          <CommentOutlined aria-hidden />
                          <span>{t('chat.cite')}</span>
                        </button>
                      )}
                      {showPolishAction && (
                        <button
                          type='button'
                          className='writer-markdown-editor__polish-action'
                          disabled={polishDisabled}
                          title={polishTitle}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => void requestPolish()}
                        >
                          <HighlightOutlined aria-hidden />
                          <span>{t('chat.artifactRewrite.action')}</span>
                        </button>
                      )}
                      {!chatPresentation && <Dropdown
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
                            if (selection?.supported || selection?.internalReference) {
                              referenceSelectionRef.current = selection;
                            }
                          }}
                        >
                          <LinkOutlined aria-hidden />
                          <span>{t('chat.writerIR.crossReference')}</span>
                          <DownOutlined
                            className='writer-markdown-editor__reference-caret'
                            aria-hidden
                          />
                        </button>
                      </Dropdown>}
                      {!chatPresentation && <button
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
                          if (selection?.supported || selection?.internalReference) {
                            referenceSelectionRef.current = selection;
                          }
                        }}
                        onClick={removeCrossReference}
                      >
                        <DisconnectOutlined aria-hidden />
                      </button>}
                    </div>
                    <Dropdown
                      trigger={['click']}
                      placement='bottomLeft'
                      overlayClassName='writer-markdown-editor__reference-dropdown'
                      open={compactActionsOpen}
                      onOpenChange={(open: boolean) => setCompactActionsOpen(open)}
                      menu={{
                        items: [
                          ...(chatPresentation && onCiteSelection ? [{
                            key: 'cite',
                            icon: <CommentOutlined />,
                            label: t('chat.cite'),
                            disabled: !selection?.text.trim(),
                          }] : []),
                          ...(showPolishAction ? [{
                            key: 'polish',
                            icon: <HighlightOutlined />,
                            label: t('chat.artifactRewrite.action'),
                            disabled: polishDisabled,
                          }] : []),
                          ...(!chatPresentation ? [{
                            key: 'cross-reference',
                            icon: <LinkOutlined />,
                            label: t('chat.writerIR.crossReference'),
                            disabled: referenceDisabled,
                            children: referenceTargets.map((target) => ({
                              key: `reference:${target.anchorId}`,
                              label: target.label,
                            })),
                          }, {
                            key: 'remove-reference',
                            icon: <DisconnectOutlined />,
                            label: t('chat.writerIR.removeCrossReference'),
                            disabled: removeReferenceDisabled,
                          }] : []),
                        ],
                        onClick: ({ key }: { key: string | number }) => {
                          const action = String(key);
                          setCompactActionsOpen(false);
                          if (action === 'cite') citeSelection();
                          if (action === 'polish') void requestPolish();
                          if (action === 'remove-reference') removeCrossReference();
                          if (action.startsWith('reference:')) {
                            applyCrossReference(action.slice('reference:'.length));
                          }
                        },
                      }}
                    >
                      <button
                        type='button'
                        className={
                          'writer-markdown-editor__reference-select '
                          + 'writer-markdown-editor__toolbar-more'
                        }
                        aria-label={t('chat.writerMarkdown.moreActions')}
                        aria-haspopup='menu'
                        aria-expanded={compactActionsOpen}
                        title={t('chat.writerMarkdown.moreActions')}
                        onMouseDown={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          if (selection?.supported || selection?.internalReference) {
                            referenceSelectionRef.current = selection;
                          }
                        }}
                      >
                        <MoreOutlined aria-hidden />
                      </button>
                    </Dropdown>
                  </>
                ),
              }),
            ]}
          />}
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
          applyPreview={rewritePreview.applyPreview}
          onApplied={onRewritePreviewApplied}
          onReject={onRewritePreviewRejected}
        />
      )}
    </section>
  );
}
