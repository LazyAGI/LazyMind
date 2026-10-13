import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { message } from "antd";
import {
  AppstoreOutlined,
  BulbOutlined,
  CommentOutlined,
  DatabaseOutlined,
  FileTextOutlined,
} from "@ant-design/icons";
import { axiosInstance, BASE_URL } from "@/components/request";
import { listSkillAssetsPage } from "@/modules/memory/skillApi";
import {
  ChatServiceApi,
  KnowledgeBaseServiceApi,
} from "@/modules/chat/utils/request";

export type MentionType =
  | "knowledge_base"
  | "skill"
  | "workflow"
  | "tool"
  | "conversation"
  | "file";

export interface ChatMention {
  mention_id: string;
  type: MentionType;
  resource_id: string;
  display_name: string;
  start?: number;
  end?: number;
}

type CandidateType = Exclude<MentionType, "tool">;
type Candidate = {
  id: string;
  type: CandidateType;
  name: string;
  description?: string;
  content?: string;
  disabled?: boolean;
  disabledReason?: string;
  disabledReasonKey?: string;
};

type QueryState = {
  trigger: "@" | "/";
  keyword: string;
  type?: CandidateType;
  range: Range;
};

type MenuPlacement = {
  direction: "above" | "below";
  height: number;
};

export interface MentionEditorRef {
  focus: () => void;
  getMentions: () => ChatMention[];
  setPlainText: (value: string) => void;
}

const isImeComposingEvent = (event: React.KeyboardEvent<HTMLElement>) =>
  event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229;

const groups: Array<{
  type: CandidateType;
  trigger: "@" | "/";
  shortcut: string;
  labelKey: string;
  icon: React.ReactNode;
}> = [
  { type: "knowledge_base", trigger: "@", shortcut: "kb", labelKey: "chat.mentionKnowledgeBase", icon: <DatabaseOutlined /> },
  { type: "skill", trigger: "/", shortcut: "skill", labelKey: "chat.mentionSkill", icon: <BulbOutlined /> },
  { type: "workflow", trigger: "/", shortcut: "workflow", labelKey: "chat.mentionWorkflow", icon: <AppstoreOutlined /> },
  { type: "file", trigger: "@", shortcut: "file", labelKey: "chat.mentionFile", icon: <FileTextOutlined /> },
  { type: "conversation", trigger: "@", shortcut: "chat", labelKey: "chat.mentionConversation", icon: <CommentOutlined /> },
];

const shortcutTypes = new Map(groups.map((group) => [group.shortcut, group.type]));
const MENU_MAX_HEIGHT = 420;
const MENU_MIN_UPWARD_HEIGHT = 160;
// Keep the existing 8px anchor gap plus an 8px viewport margin.
const MENU_VIEWPORT_OFFSET = 16;


const bypassCandidateCache = (type: CandidateType) =>
  type === "skill" || type === "workflow" || type === "conversation" || type === "file";

function escapeHtml(value: string) {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttribute(value: string) {
  return escapeHtml(value).replace(/"/g, "&quot;");
}

function mentionHtml(mention: ChatMention, removeLabel: string) {
  return `<span class="chat-mention-chip" contenteditable="false" data-mention-id="${escapeAttribute(mention.mention_id)}" data-mention-type="${mention.type}" data-resource-id="${escapeAttribute(mention.resource_id)}" data-display-name="${escapeAttribute(mention.display_name)}" data-trigger="${mention.type === "skill" || mention.type === "workflow" ? "/" : "@"}">${escapeHtml(mention.display_name)}<button type="button" class="chat-mention-remove" data-remove-mention aria-label="${escapeAttribute(removeLabel + " " + mention.display_name)}"></button></span>&#8203;`;
}

function serializeEditor(editor: HTMLElement) {
  let text = "";
  let afterMention = false;
  const mentions: ChatMention[] = [];
  const appendText = (value: string) => {
    if (!value) return text.length;
    if (afterMention && !/^\s/.test(value)) text += " ";
    const start = text.length;
    text += value;
    afterMention = false;
    return start;
  };
  const visit = (node: Node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      appendText((node.textContent || "").replace(/\u200b/g, ""));
      return;
    }
    if (!(node instanceof HTMLElement)) return;
    if (node.dataset.mentionId) {
      const displayName = node.dataset.displayName || node.textContent || "";
      const start = appendText(displayName);
      const mention: ChatMention = {
        mention_id: node.dataset.mentionId,
        type: node.dataset.mentionType as MentionType,
        resource_id: node.dataset.resourceId || "",
        display_name: displayName,
        start,
        end: start + (node.dataset.displayName || node.textContent || "").length,
      };
      mentions.push(mention);
      afterMention = true;
      return;
    }
    if (node.tagName === "BR") {
      appendText("\n");
      return;
    }
    node.childNodes.forEach(visit);
    if (node !== editor && node.tagName === "DIV") appendText("\n");
  };
  editor.childNodes.forEach(visit);
  return { text: text.replace(/\n+$/, ""), mentions };
}

function queryAtCaret(editor: HTMLElement): QueryState | null {
  const selection = window.getSelection();
  if (!selection || !selection.isCollapsed || selection.rangeCount === 0) return null;
  const caret = selection.getRangeAt(0);
  const node = caret.startContainer;
  if (node.nodeType !== Node.TEXT_NODE || !editor.contains(node)) return null;
  const before = (node.textContent || "").slice(0, caret.startOffset);
  const at = Math.max(before.lastIndexOf("@"), before.lastIndexOf("/"));
  if (at < 0 || (at > 0 && !/[\s，。！？、；：,.!?;:]/.test(before[at - 1]))) return null;
  const trigger = before[at] as "@" | "/";
  const raw = before.slice(at + 1);
  if (/[\n\r/@]/.test(raw) || (trigger === "/" && /[\s\\]/.test(raw))) return null;
  const shortcut = raw.match(/^([a-z]+):( *)(.*)$/i);
  let type: CandidateType | undefined;
  let keyword = raw;
  if (shortcut && groups.some(group => group.trigger === trigger && group.shortcut === shortcut[1].toLowerCase())) {
    if (shortcut[2].length > 1) return null;
    type = shortcutTypes.get(shortcut[1].toLowerCase());
    keyword = shortcut[3];
  }
  const range = document.createRange();
  range.setStart(node, at);
  range.setEnd(node, caret.startOffset);
  return { trigger, keyword, type, range };
}

function unwrap<T>(value: T | { data?: T }): T {
  return value && typeof value === "object" && "data" in value && (value as { data?: T }).data
    ? (value as { data: T }).data
    : (value as T);
}

async function loadCandidates(type: CandidateType, keyword: string, runInBackground: boolean, signal?: AbortSignal): Promise<Candidate[]> {
  if (type === "knowledge_base") {
    const response = await KnowledgeBaseServiceApi().datasetServiceListDatasets({ keyword, pageSize: 100 });
    return (response.data.datasets || []).map((item) => ({
      id: item.dataset_id || "",
      type,
      name: item.display_name || item.dataset_id || "",
    }));
  }
  if (type === "skill") {
    const response = await listSkillAssetsPage({ keyword, page: 1, pageSize: 100 });
    return response.records.map((item) => ({ id: item.id, type, name: item.name, description: item.description }));
  }
  if (type === "workflow") {
    const response = await axiosInstance.get(`${BASE_URL}/api/core/chat/settings/workflows`, { params: { keyword } });
    const payload = unwrap<{ workflows?: Array<Record<string, unknown>> }>(response.data);
    const workflows = payload.workflows || [];
    const names = new Map<string, number>();
    for (const item of workflows) {
      const name = String(item.name || item.workflow_id || "");
      names.set(name, (names.get(name) || 0) + 1);
    }
    return workflows
      .map((item) => {
        const id = String(item.workflow_ref || item.workflow_id || "");
        const name = String(item.name || item.workflow_id || "");
        const workflowId = String(item.workflow_id || id);
        const identity = workflows.some((other) => other !== item && other.workflow_id === item.workflow_id && other.name === item.name)
          ? id : workflowId;
        const disabled = item.enabled === false || item.call_mode === "disabled";
        return {
          id, type, name: (names.get(name) || 0) > 1 ? `${name}（${identity}）` : name,
          description: String(item.description || ""),
          disabled,
          disabledReasonKey: disabled ? "chat.mentionWorkflowDisabled" : undefined,
        };
      })
      .filter((item) => !keyword || `${item.name} ${item.description}`.toLowerCase().includes(keyword.toLowerCase()));
  }
  const response = await ChatServiceApi().conversationServiceListConversations({ keyword, pageSize: 50, pageToken: "" }, { params: { is_task_conv: runInBackground }, signal });
  return (response.data.conversations || []).map((item) => ({ id: item.conversation_id || item.name || "", type, name: item.display_name || item.conversation_id || "" }));
}

function replaceCandidateGroup(current: Candidate[], type: CandidateType, items: Candidate[]) {
  const byType = new Map<CandidateType, Candidate[]>();
  for (const item of current) {
    if (item.type === type) continue;
    const groupItems = byType.get(item.type) || [];
    groupItems.push(item);
    byType.set(item.type, groupItems);
  }
  byType.set(type, items);
  return groups.flatMap((group) => byType.get(group.type) || []);
}

const EMPTY_FILES: Array<{ id: string; name: string; disabled?: boolean }> = [];

const MentionEditor = forwardRef<MentionEditorRef, {
  value: string;
  initialMentions?: ChatMention[];
  disabled?: boolean;
  placeholder: string;
  onChange: (value: string) => void;
  onMentionsChange: (mentions: ChatMention[]) => void;
  onPaste: (event: React.ClipboardEvent<HTMLDivElement>) => void;
  onSend: () => void;
  onCompositionChange: (composing: boolean) => void;
  disabledMentionReasons?: Partial<Record<MentionType, string>>;
  allowKnowledgeBaseSelection?: boolean;
  allowMentions?: boolean;
  runInBackground?: boolean;
  conversationId?: string;
  fileCandidates?: Array<{ id: string; name: string; disabled?: boolean }>;
  onQueryOpen?: () => void;
}>(({
  value,
  initialMentions,
  disabled,
  placeholder,
  onChange,
  onMentionsChange,
  onPaste,
  onSend,
  onCompositionChange,
  disabledMentionReasons,
  allowKnowledgeBaseSelection = true,
  allowMentions = true,
  runInBackground = false,
  conversationId,
  fileCandidates = EMPTY_FILES,
  onQueryOpen,
}, ref) => {
  const { t } = useTranslation();
  const menuId = useId();
  const availableGroups = useMemo(() => allowMentions ? groups.filter((group) =>
    allowKnowledgeBaseSelection || group.type !== 'knowledge_base',
  ) : [], [allowKnowledgeBaseSelection, allowMentions]);
  const canRestore = useCallback((type: string) => allowMentions && (type !== "knowledge_base" || allowKnowledgeBaseSelection) && ["knowledge_base", "skill", "workflow", "tool", "conversation", "file"].includes(type), [allowMentions, allowKnowledgeBaseSelection]);
  const candidateCache = useRef(new Map<string, Candidate[]>());
  const composingRef = useRef(false);
  const [retryCount, setRetryCount] = useState(0);
  const [loadError, setLoadError] = useState(false);
  const editorRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const emittedRef = useRef<string | null>(null);
  const queryRef = useRef<QueryState | null>(null);
  const requestRef = useRef(0);
  const [query, setQuery] = useState<QueryState | null>(null);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [activeKey, setActiveKey] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [expandedTypes, setExpandedTypes] = useState<Set<CandidateType>>(new Set());
  const [menuPlacement, setMenuPlacement] = useState<MenuPlacement>({
    direction: "above",
    height: MENU_MAX_HEIGHT,
  });

  const closeMenu = useCallback(() => {
    requestRef.current += 1;
    queryRef.current = null;
    setQuery(null);
    setActiveKey(undefined);
  }, []);

  useEffect(() => {
    if (!allowMentions || !query) return;
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node)
        || editorRef.current?.contains(target)
        || menuRef.current?.contains(target)) return;
      closeMenu();
    };
    document.addEventListener("pointerdown", handlePointerDown, true);
    return () => document.removeEventListener("pointerdown", handlePointerDown, true);
  }, [allowMentions, query, closeMenu]);

  const updateMenuPlacement = useCallback(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const rect = editor.getBoundingClientRect();
    const aboveSpace = Math.max(0, rect.top - MENU_VIEWPORT_OFFSET);
    const belowSpace = Math.max(
      0,
      window.innerHeight - rect.bottom - MENU_VIEWPORT_OFFSET,
    );
    const direction = aboveSpace >= MENU_MIN_UPWARD_HEIGHT || aboveSpace >= belowSpace
      ? "above"
      : "below";
    const height = Math.min(MENU_MAX_HEIGHT, Math.floor(
      direction === "above" ? aboveSpace : belowSpace,
    ));
    setMenuPlacement((current) => (
      current.direction === direction && current.height === height
        ? current
        : { direction, height }
    ));
  }, []);

  const emit = useCallback(() => {
    if (!editorRef.current) return;
    const serialized = serializeEditor(editorRef.current);
    emittedRef.current = serialized.text;
    onChange(serialized.text);
    onMentionsChange(allowMentions ? serialized.mentions : []);
  }, [allowMentions, onChange, onMentionsChange]);

  useImperativeHandle(ref, () => ({
    focus: () => editorRef.current?.focus(),
    getMentions: () => allowMentions && editorRef.current ? serializeEditor(editorRef.current).mentions : [],
    setPlainText: (text) => {
      if (!editorRef.current) return;
      editorRef.current.textContent = text;
      emit();
    },
  }), [allowMentions, emit]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    if (value === emittedRef.current) {
      let removed = false;
      editor.querySelectorAll<HTMLElement>('.chat-mention-chip').forEach((chip) => {
        if (!canRestore(chip.dataset.mentionType || "")) {
          const separator = chip.nextSibling;
          if (separator?.nodeType === Node.TEXT_NODE) {
            separator.textContent = (separator.textContent || '').replace(/^\u200b/, '');
          }
          chip.replaceWith(document.createTextNode(chip.dataset.displayName || chip.textContent || ''));
          removed = true;
        }
      });
      if (removed) onMentionsChange(serializeEditor(editor).mentions);
      return;
    }
    let cursor = 0;
    let html = '';
    for (const mention of [...(allowMentions ? initialMentions || [] : [])].sort((a, b) => (a.start ?? 0) - (b.start ?? 0))) {
      const { start, end } = mention;
      if (start === undefined || end === undefined || !Number.isInteger(start) || !Number.isInteger(end)
        || start < cursor || end <= start || end > value.length || value.slice(start, end) !== mention.display_name
        || !canRestore(mention.type)) continue;
      html += escapeHtml(value.slice(cursor, start)) + mentionHtml(mention, t("chat.removeMention"));
      cursor = end;
    }
    editor.innerHTML = html + escapeHtml(value.slice(cursor));
    emittedRef.current = value;
    onMentionsChange(serializeEditor(editor).mentions);
  }, [allowMentions, canRestore, initialMentions, onMentionsChange, value]);

  useEffect(() => {
    closeMenu();
    candidateCache.current.clear();
  }, [closeMenu, runInBackground, conversationId]);

  useEffect(() => {
    const controller = new AbortController();
    availableGroups.filter(group => group.type !== 'file').forEach(group => {
      void loadCandidates(group.type, '', runInBackground, controller.signal).then(items => {
        if (!controller.signal.aborted) candidateCache.current.set(`${group.type}:`, items);
      }).catch(() => undefined);
    });
    return () => controller.abort();
  }, [availableGroups, runInBackground, conversationId]);

  useEffect(() => {
    if (!query) { setCandidates([]); return; }
    const requestId = ++requestRef.current;
    const controller = new AbortController();
    const targetGroups = availableGroups.filter(group => group.trigger === query.trigger && (!query.type || group.type === query.type));
    setCandidates([]);
    setLoading(true);
    setLoadError(false);
    const timer = window.setTimeout(() => {
      let remaining = targetGroups.length;
      if (!remaining) setLoading(false);
      targetGroups.forEach(group => {
        const key = `${group.type}:${query.keyword.toLocaleLowerCase()}`;
        const cached = candidateCache.current.get(key);
        const request = group.type === 'file'
          ? Promise.resolve(fileCandidates.map<Candidate>(file => ({ ...file, type: 'file' as const, disabledReasonKey: file.disabled ? 'chat.mentionFileUploading' : undefined })))
          : cached && !bypassCandidateCache(group.type) ? Promise.resolve(cached)
          : loadCandidates(group.type, query.keyword, runInBackground, controller.signal);
        request.then(items => {
          if (requestRef.current !== requestId || controller.signal.aborted) return;
          const filtered = items.filter(item => item.id && item.name && `${item.name} ${item.description || ""}`.toLocaleLowerCase().includes(query.keyword.trim().toLocaleLowerCase()));
          candidateCache.current.set(key, filtered);
          setCandidates(current => replaceCandidateGroup(current, group.type, filtered));
        }).catch(() => {
          if (requestRef.current === requestId && !controller.signal.aborted) setLoadError(true);
        }).finally(() => {
          remaining -= 1;
          if (!remaining && requestRef.current === requestId && !controller.signal.aborted) setLoading(false);
        });
      });
    }, query.keyword ? 180 : 0);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [query?.keyword, query?.type, query?.trigger, availableGroups, runInBackground, fileCandidates, retryCount]);

  useEffect(() => {
    if (!query) return;
    requestAnimationFrame(() => {
      menuRef.current?.scrollTo({ top: 0 });
    });
  }, [query?.keyword, query?.type]);

  useLayoutEffect(() => {
    if (!query) return;
    updateMenuPlacement();
    window.addEventListener("resize", updateMenuPlacement);
    window.addEventListener("scroll", updateMenuPlacement, true);
    return () => {
      window.removeEventListener("resize", updateMenuPlacement);
      window.removeEventListener("scroll", updateMenuPlacement, true);
    };
  }, [query, updateMenuPlacement]);

  const refreshQuery = useCallback(() => {
    if (composingRef.current) return;
    const next = allowMentions && editorRef.current ? queryAtCaret(editorRef.current) : null;
    if (next) onQueryOpen?.();
    queryRef.current = next;
    setQuery(next);
    setActiveKey(undefined);
    setExpandedTypes(new Set());
  }, [allowMentions, onQueryOpen]);

  const getDisabledReason = useCallback((candidate: Candidate) => (
    candidate.disabledReason ||
    (candidate.disabledReasonKey ? t(candidate.disabledReasonKey) : undefined) ||
    disabledMentionReasons?.[candidate.type as MentionType]
  ), [disabledMentionReasons, t]);

  const isCandidateDisabled = useCallback((candidate: Candidate) => (
    candidate.disabled || Boolean(getDisabledReason(candidate))
  ), [getDisabledReason]);

  const insertCandidate = useCallback((candidate: Candidate) => {
    const editor = editorRef.current;
    const currentQuery = queryRef.current;
    if (!editor || !currentQuery) return;
    const candidateDisabledReason = getDisabledReason(candidate);
    if (candidate.disabled || candidateDisabledReason) {
      message.info(candidateDisabledReason || "该能力已停用");
      return;
    }
    if (
      candidate.type === "workflow" &&
      serializeEditor(editor).mentions.some((mention) => mention.type === "workflow")
    ) {
      message.warning(t("chat.mentionSingleWorkflowOnly"));
      return;
    }
    const selection = window.getSelection();
    currentQuery.range.deleteContents();
      const mention: ChatMention = {
        mention_id: crypto.randomUUID(),
        type: candidate.type,
        resource_id: candidate.id,
        display_name: candidate.name,
      };
      const holder = document.createElement("span");
      holder.innerHTML = mentionHtml(mention, t("chat.removeMention"));
      const chip = holder.firstChild!;
      const spacer = holder.lastChild!;
      currentQuery.range.insertNode(spacer);
      currentQuery.range.insertNode(chip);
      const caret = document.createRange();
      caret.setStartAfter(spacer);
      caret.collapse(true);
      selection?.removeAllRanges();
      selection?.addRange(caret);
    closeMenu();
    emit();
    editor.focus();
  }, [closeMenu, emit, getDisabledReason, t]);

  const removeChip = (chip: HTMLElement) => {
    const parent = chip.parentNode;
    if (!parent) return;
    const index = Array.from(parent.childNodes).indexOf(chip);
    const separator = chip.nextSibling;
    chip.remove();
    if (separator?.nodeType === Node.TEXT_NODE) separator.textContent = (separator.textContent || '').replace(/^\u200b/, '');
    editorRef.current?.focus();
    const caret = document.createRange();
    caret.setStart(parent, Math.min(index, parent.childNodes.length));
    caret.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(caret);
    closeMenu();
    emit();
  };

  const visibleCandidates = candidates.filter((candidate) => {
    if (expandedTypes.has(candidate.type)) return true;
    return candidates.filter((item) => item.type === candidate.type).indexOf(candidate) < 9;
  });

  const activeIndex = visibleCandidates.findIndex(item => `${item.type}:${item.id}` === activeKey);

  const moveActiveCandidate = (direction: 1 | -1) => {
    if (!visibleCandidates.some((candidate) => !isCandidateDisabled(candidate))) {
      setActiveKey(undefined);
      return;
    }
    setActiveKey((current) => {
      const currentIndex = visibleCandidates.findIndex(item => `${item.type}:${item.id}` === current);
      let index = currentIndex < 0 ? (direction > 0 ? -1 : 0) : currentIndex;
      for (let step = 0; step < visibleCandidates.length; step += 1) {
        index = (index + direction + visibleCandidates.length) % visibleCandidates.length;
        if (!isCandidateDisabled(visibleCandidates[index])) return `${visibleCandidates[index].type}:${visibleCandidates[index].id}`;
      }
      return undefined;
    });
  };

  return (
    <div className="chat-mention-editor-wrapper">
      <div
        ref={editorRef}
        className="message-input chat-mention-editor"
        contentEditable={!disabled}
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        aria-expanded={Boolean(query)}
        aria-controls={query ? menuId : undefined}
        aria-activedescendant={query && activeIndex >= 0 ? `${menuId}-${activeIndex}` : undefined}
        aria-label={placeholder}
        data-placeholder={placeholder}
        data-empty={value === "" ? "true" : "false"}
        onInput={() => { emit(); refreshQuery(); }}
        onClick={(event) => {
          const button = (event.target as HTMLElement).closest('[data-remove-mention]');
          const chip = button?.closest<HTMLElement>('.chat-mention-chip');
          if (chip) removeChip(chip); else refreshQuery();
        }}
        onKeyUp={(event) => { if (!["ArrowUp", "ArrowDown", "Enter", "Escape"].includes(event.key)) refreshQuery(); }}
        onCompositionStart={() => { composingRef.current = true; closeMenu(); onCompositionChange(true); }}
        onCompositionEnd={() => { composingRef.current = false; onCompositionChange(false); refreshQuery(); }}
        onPaste={onPaste}
        onKeyDown={(event) => {
          if (isImeComposingEvent(event)) {
            return;
          }
          if ((event.key === 'Enter' || event.key === ' ') && (event.target as HTMLElement).matches('[data-remove-mention]')) {
            event.preventDefault();
            const chip = (event.target as HTMLElement).closest<HTMLElement>('.chat-mention-chip');
            if (chip) removeChip(chip);
            return;
          }
          if (allowMentions && query) {
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault();
              moveActiveCandidate(event.key === "ArrowDown" ? 1 : -1);
              return;
            }
            if ((event.key === "Enter" || event.key === "Tab") && activeIndex >= 0 && visibleCandidates[activeIndex]) {
              event.preventDefault();
              insertCandidate(visibleCandidates[activeIndex]);
              return;
            }
            if (event.key === "Escape") {
              event.preventDefault();
              closeMenu();
              return;
            }
          }
          if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            onSend();
          }
        }}
      />
      {allowMentions && query && (
        <div
          ref={menuRef}
          id={menuId}
          className={`chat-mention-menu${menuPlacement.direction === "below" ? " is-below" : ""}`}
          role="listbox"
          style={{ height: menuPlacement.height }}
          onMouseDown={(event) => event.preventDefault()}
        >
          {loading && candidates.length === 0 ? <div className="chat-mention-empty">{t("common.loading")}</div> : null}
          {loadError ? <div className="chat-mention-empty" role="alert">{t("chat.mentionLoadFailed")} <button type="button" onClick={() => setRetryCount(count => count + 1)}>{t("common.retry")}</button></div> : null}
          {!loading && !loadError && candidates.length === 0 ? <div className="chat-mention-empty">{t("chat.mentionNoResults")}</div> : null}
          {availableGroups.filter((group) => group.trigger === query.trigger && (!query.type || group.type === query.type)).map((group) => {
            const allItems = candidates.filter((item) => item.type === group.type);
            const isExpanded = expandedTypes.has(group.type);
            const items = isExpanded ? allItems : allItems.slice(0, 9);
            if (!items.length) return null;
            return <div className={`chat-mention-group${group.type === "conversation" ? " is-conversation" : ""}`} key={group.type}>
              <div className="chat-mention-group-title">
                <span>{group.icon}{t(group.labelKey)}</span>
                <span className="chat-mention-shortcut"><code>{group.trigger}{group.shortcut}:</code>{t("chat.mentionShortcutHint")}</span>
              </div>
              <div className="chat-mention-options">
              {items.map((item) => {
                const index = visibleCandidates.indexOf(item);
                const itemDisabled = isCandidateDisabled(item);
                const itemDisabledReason = getDisabledReason(item);
                const title = itemDisabled
                  ? `${item.name}\n${itemDisabledReason || "该能力已停用"}`
                  : item.description ? `${item.name}\n${item.description}` : item.name;
                return <button type="button" role="option" id={`${menuId}-${index}`} title={title} aria-selected={activeIndex === index} className={`chat-mention-option${activeIndex === index ? " is-active" : ""}`} disabled={itemDisabled} key={`${item.type}-${item.id}`} onClick={() => { if (!itemDisabled) insertCandidate(item); }} onMouseEnter={() => { if (!itemDisabled) setActiveKey(`${item.type}:${item.id}`); }} onMouseDown={(event) => { event.preventDefault(); if (!itemDisabled) insertCandidate(item); }}>
                  <span className="chat-mention-option-name">{item.name}</span>
                  {itemDisabled ? <span className="chat-mention-option-status">{itemDisabledReason || "已停用"}</span> : null}
                  {group.type === "conversation" && item.description ? <span className="chat-mention-option-description">{item.description}</span> : null}
                </button>;
              })}
              </div>
              {allItems.length > 9 || isExpanded ? (
                <button type="button" className="chat-mention-expand" onMouseDown={(event) => { event.preventDefault(); setExpandedTypes((current) => { const next = new Set(current); if (isExpanded) next.delete(group.type); else next.add(group.type); return next; }); setActiveKey(undefined); }}>
                  {isExpanded ? t("chat.mentionCollapse") : t("chat.mentionShowMore", { count: allItems.length })}
                </button>
              ) : null}
            </div>;
          })}
        </div>
      )}
    </div>
  );
});

MentionEditor.displayName = "MentionEditor";
export default MentionEditor;
