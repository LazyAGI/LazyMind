import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { message } from "antd";
import {
  AppstoreOutlined,
  BookOutlined,
  BulbOutlined,
  CommentOutlined,
  DatabaseOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { axiosInstance, BASE_URL } from "@/components/request";
import { listSkillAssetsPage } from "@/modules/memory/skillApi";
import { listToolAssetsPage } from "@/modules/memory/toolApi";
import {
  ChatServiceApi,
  KnowledgeBaseServiceApi,
  PromptServiceApi,
} from "@/modules/chat/utils/request";

export type MentionType =
  | "knowledge_base"
  | "skill"
  | "plugin"
  | "tool"
  | "conversation";

export interface ChatMention {
  mention_id: string;
  type: MentionType;
  resource_id: string;
  display_name: string;
  start?: number;
  end?: number;
}

type CandidateType = MentionType | "prompt";
type Candidate = {
  id: string;
  type: CandidateType;
  name: string;
  description?: string;
  content?: string;
};

type QueryState = {
  keyword: string;
  type?: CandidateType;
  range: Range;
};

export interface MentionEditorRef {
  focus: () => void;
  getMentions: () => ChatMention[];
}

const groups: Array<{
  type: CandidateType;
  shortcut: string;
  labelKey: string;
  icon: React.ReactNode;
}> = [
  { type: "knowledge_base", shortcut: "kb", labelKey: "chat.mentionKnowledgeBase", icon: <DatabaseOutlined /> },
  { type: "skill", shortcut: "skill", labelKey: "chat.mentionSkill", icon: <BulbOutlined /> },
  { type: "plugin", shortcut: "plugin", labelKey: "chat.mentionPlugin", icon: <AppstoreOutlined /> },
  { type: "tool", shortcut: "tool", labelKey: "chat.mentionTool", icon: <ThunderboltOutlined /> },
  { type: "prompt", shortcut: "prompt", labelKey: "chat.mentionPrompt", icon: <BookOutlined /> },
  { type: "conversation", shortcut: "chat", labelKey: "chat.mentionConversation", icon: <CommentOutlined /> },
];

const shortcutTypes = new Map(groups.map((group) => [group.shortcut, group.type]));
const candidateCache = new Map<string, Candidate[]>();
const candidateRequests = new Map<string, Promise<Candidate[]>>();

const cacheKey = (type: CandidateType, keyword: string) =>
  `${type}:${keyword.trim().toLocaleLowerCase()}`;

function escapeHtml(value: string) {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttribute(value: string) {
  return escapeHtml(value).replace(/"/g, "&quot;");
}

function mentionHtml(mention: ChatMention) {
  return `<span class="chat-mention-chip" contenteditable="false" data-mention-id="${escapeAttribute(mention.mention_id)}" data-mention-type="${mention.type}" data-resource-id="${escapeAttribute(mention.resource_id)}" data-display-name="${escapeAttribute(mention.display_name)}">${escapeHtml(mention.display_name)}</span>&#8203;`;
}

function serializeEditor(editor: HTMLElement) {
  let text = "";
  const mentions: ChatMention[] = [];
  const visit = (node: Node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      text += (node.textContent || "").replace(/\u200b/g, "");
      return;
    }
    if (!(node instanceof HTMLElement)) return;
    if (node.dataset.mentionId) {
      const start = text.length;
      const mention: ChatMention = {
        mention_id: node.dataset.mentionId,
        type: node.dataset.mentionType as MentionType,
        resource_id: node.dataset.resourceId || "",
        display_name: node.dataset.displayName || node.textContent || "",
        start,
        end: start + (node.dataset.displayName || node.textContent || "").length,
      };
      mentions.push(mention);
      text += mention.display_name;
      return;
    }
    if (node.tagName === "BR") {
      text += "\n";
      return;
    }
    node.childNodes.forEach(visit);
    if (node !== editor && node.tagName === "DIV") text += "\n";
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
  const at = before.lastIndexOf("@");
  if (at < 0 || (at > 0 && !/[\s，。！？、；：,.!?;:]/.test(before[at - 1]))) return null;
  const raw = before.slice(at + 1);
  const shortcut = raw.match(/^([a-z]+):( *)(.*)$/i);
  let type: CandidateType | undefined;
  let keyword = raw;
  if (shortcut && shortcutTypes.has(shortcut[1].toLowerCase())) {
    if (shortcut[2].length > 1) return null;
    type = shortcutTypes.get(shortcut[1].toLowerCase());
    keyword = shortcut[3];
  }
  const range = document.createRange();
  range.setStart(node, at);
  range.setEnd(node, caret.startOffset);
  return { keyword, type, range };
}

function unwrap<T>(value: T | { data?: T }): T {
  return value && typeof value === "object" && "data" in value && (value as { data?: T }).data
    ? (value as { data: T }).data
    : (value as T);
}

async function loadCandidates(type: CandidateType, keyword: string): Promise<Candidate[]> {
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
  if (type === "tool") {
    const response = await listToolAssetsPage({ keyword, silentError: true });
    return response.records.map((item) => ({ id: item.id, type, name: item.name, description: item.description }));
  }
  if (type === "plugin") {
    const response = await axiosInstance.get(`${BASE_URL}/api/core/chat/settings/plugins`, { params: { keyword } });
    const payload = unwrap<{ plugins?: Array<Record<string, unknown>> }>(response.data);
    return (payload.plugins || [])
      .filter((item) => !keyword || `${item.name || ""} ${item.description || ""}`.toLowerCase().includes(keyword.toLowerCase()))
      .map((item) => ({ id: String(item.plugin_ref || item.plugin_id || ""), type, name: String(item.name || item.plugin_id || ""), description: String(item.description || "") }));
  }
  if (type === "prompt") {
    const response = await PromptServiceApi().listPrompts({ keyword, pageSize: 100 });
    return (response.data.prompts || []).map((item) => ({ id: item.id || "", type, name: item.display_name || item.name || "", content: item.content || "" }));
  }
  const response = await ChatServiceApi().conversationServiceListConversations({ keyword, pageSize: 50, pageToken: "" });
  return (response.data.conversations || []).map((item) => ({ id: item.conversation_id || item.name || "", type, name: item.display_name || item.conversation_id || "" }));
}

function loadAndCacheCandidates(type: CandidateType, keyword: string) {
  const key = cacheKey(type, keyword);
  const cached = candidateCache.get(key);
  if (cached) return Promise.resolve(cached);
  const pending = candidateRequests.get(key);
  if (pending) return pending;
  const request = loadCandidates(type, keyword)
    .then((items) => {
      const normalizedKeyword = keyword.trim().toLocaleLowerCase();
      const seen = new Set<string>();
      const filtered = items.filter((item) => {
        if (normalizedKeyword && !item.name.toLocaleLowerCase().includes(normalizedKeyword)) {
          return false;
        }
        const identity = `${item.type}:${item.id}`;
        if (seen.has(identity)) return false;
        seen.add(identity);
        return true;
      });
      candidateCache.set(key, filtered);
      return filtered;
    })
    .finally(() => candidateRequests.delete(key));
  candidateRequests.set(key, request);
  return request;
}

function cachedCandidates(type: CandidateType, keyword: string) {
  const exact = candidateCache.get(cacheKey(type, keyword));
  if (exact) return exact;
  const base = candidateCache.get(cacheKey(type, ""));
  const normalized = keyword.trim().toLocaleLowerCase();
  if (!base || !normalized) return [];
  return base.filter((item) => item.name.toLocaleLowerCase().includes(normalized));
}

const MentionEditor = forwardRef<MentionEditorRef, {
  value: string;
  disabled?: boolean;
  placeholder: string;
  onChange: (value: string) => void;
  onMentionsChange: (mentions: ChatMention[]) => void;
  onPaste: (event: React.ClipboardEvent<HTMLDivElement>) => void;
  onSend: () => void;
  onCompositionChange: (composing: boolean) => void;
}>(({ value, disabled, placeholder, onChange, onMentionsChange, onPaste, onSend, onCompositionChange }, ref) => {
  const { t } = useTranslation();
  const editorRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const emittedRef = useRef<string | null>(null);
  const queryRef = useRef<QueryState | null>(null);
  const menuWasOpenRef = useRef(false);
  const requestRef = useRef(0);
  const [query, setQuery] = useState<QueryState | null>(null);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [activeIndex, setActiveIndex] = useState(-1);
  const [loading, setLoading] = useState(false);
  const [expandedTypes, setExpandedTypes] = useState<Set<CandidateType>>(new Set());

  const emit = useCallback(() => {
    if (!editorRef.current) return;
    const serialized = serializeEditor(editorRef.current);
    emittedRef.current = serialized.text;
    onChange(serialized.text);
    onMentionsChange(serialized.mentions);
  }, [onChange, onMentionsChange]);

  useImperativeHandle(ref, () => ({
    focus: () => editorRef.current?.focus(),
    getMentions: () => editorRef.current ? serializeEditor(editorRef.current).mentions : [],
  }), []);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor || value === emittedRef.current) return;
    editor.textContent = value;
    emittedRef.current = value;
    onMentionsChange([]);
  }, [onMentionsChange, value]);

  useEffect(() => {
    // Warm the session cache as soon as the composer mounts. Opening `@` can
    // then paint immediately while a background refresh keeps data current.
    groups.forEach((group) => {
      void loadAndCacheCandidates(group.type, "");
    });
  }, []);

  useEffect(() => {
    if (!query) {
      menuWasOpenRef.current = false;
      setCandidates([]);
      return;
    }
    const requestId = ++requestRef.current;
    const targetGroups = query.type ? groups.filter((item) => item.type === query.type) : groups;
    const warmCandidates = targetGroups.flatMap((item) =>
      cachedCandidates(item.type, query.keyword),
    );
    if (warmCandidates.length > 0) {
      setCandidates(warmCandidates);
      setLoading(false);
    }
    const hasExactCache = targetGroups.every((item) =>
      candidateCache.has(cacheKey(item.type, query.keyword)),
    );
    if (hasExactCache) {
      setCandidates(targetGroups.flatMap((item) => cachedCandidates(item.type, query.keyword)));
      setLoading(false);
      return;
    }
    const timer = window.setTimeout(async () => {
      if (warmCandidates.length === 0) setLoading(true);
      try {
        const results = await Promise.allSettled(targetGroups.map((item) => loadAndCacheCandidates(item.type, query.keyword)));
        if (requestRef.current !== requestId) return;
        setCandidates(results.flatMap((result) => result.status === "fulfilled" ? result.value : []));
      } finally {
        if (requestRef.current === requestId) setLoading(false);
      }
    }, query.keyword ? 180 : 0);
    return () => window.clearTimeout(timer);
  }, [query?.keyword, query?.type]);

  useEffect(() => {
    if (!query || menuWasOpenRef.current) return;
    menuWasOpenRef.current = true;
    requestAnimationFrame(() => {
      menuRef.current?.scrollTo({ top: 0 });
    });
  }, [query]);

  const refreshQuery = useCallback(() => {
    const next = editorRef.current ? queryAtCaret(editorRef.current) : null;
    queryRef.current = next;
    setQuery(next);
    setActiveIndex(-1);
    setExpandedTypes(new Set());
  }, []);

  const insertCandidate = useCallback((candidate: Candidate) => {
    const editor = editorRef.current;
    const currentQuery = queryRef.current;
    if (!editor || !currentQuery) return;
    if (
      candidate.type === "plugin" &&
      serializeEditor(editor).mentions.some((mention) => mention.type === "plugin")
    ) {
      message.warning(t("chat.mentionSinglePluginOnly"));
      return;
    }
    const selection = window.getSelection();
    currentQuery.range.deleteContents();
    if (candidate.type === "prompt") {
      const node = document.createTextNode(candidate.content || candidate.name);
      currentQuery.range.insertNode(node);
      const selected = document.createRange();
      selected.selectNodeContents(node);
      selection?.removeAllRanges();
      selection?.addRange(selected);
    } else {
      const mention: ChatMention = {
        mention_id: crypto.randomUUID(),
        type: candidate.type,
        resource_id: candidate.id,
        display_name: candidate.name,
      };
      const holder = document.createElement("span");
      holder.innerHTML = mentionHtml(mention);
      const chip = holder.firstChild!;
      const spacer = holder.lastChild!;
      currentQuery.range.insertNode(spacer);
      currentQuery.range.insertNode(chip);
      const caret = document.createRange();
      caret.setStartAfter(spacer);
      caret.collapse(true);
      selection?.removeAllRanges();
      selection?.addRange(caret);
    }
    queryRef.current = null;
    setQuery(null);
    setActiveIndex(-1);
    emit();
    editor.focus();
  }, [emit, t]);

  const visibleCandidates = candidates.filter((candidate) => {
    if (expandedTypes.has(candidate.type)) return true;
    return candidates.filter((item) => item.type === candidate.type).indexOf(candidate) < 9;
  });

  return (
    <div className="chat-mention-editor-wrapper">
      <div
        ref={editorRef}
        className="message-input chat-mention-editor"
        contentEditable={!disabled}
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        data-placeholder={placeholder}
        onInput={() => { emit(); refreshQuery(); }}
        onClick={refreshQuery}
        onKeyUp={(event) => { if (!["ArrowUp", "ArrowDown", "Enter"].includes(event.key)) refreshQuery(); }}
        onCompositionStart={() => onCompositionChange(true)}
        onCompositionEnd={() => { onCompositionChange(false); refreshQuery(); }}
        onPaste={onPaste}
        onKeyDown={(event) => {
          if (query) {
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault();
              if (visibleCandidates.length) setActiveIndex((current) => event.key === "ArrowDown" ? (current + 1) % visibleCandidates.length : (current <= 0 ? visibleCandidates.length - 1 : current - 1));
              return;
            }
            if ((event.key === "Enter" || event.key === "Tab") && activeIndex >= 0 && visibleCandidates[activeIndex]) {
              event.preventDefault();
              insertCandidate(visibleCandidates[activeIndex]);
              return;
            }
            if (event.key === "Escape") {
              event.preventDefault();
              queryRef.current = null;
              setQuery(null);
              return;
            }
          }
          if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            onSend();
          }
        }}
      />
      {query && (
        <div ref={menuRef} className="chat-mention-menu" role="listbox" onMouseDown={(event) => event.preventDefault()}>
          {loading && candidates.length === 0 ? <div className="chat-mention-empty">{t("common.loading")}</div> : null}
          {!loading && candidates.length === 0 ? <div className="chat-mention-empty">{t("chat.mentionNoResults")}</div> : null}
          {groups.filter((group) => !query.type || group.type === query.type).map((group) => {
            const allItems = candidates.filter((item) => item.type === group.type);
            const isExpanded = expandedTypes.has(group.type);
            const items = isExpanded ? allItems : allItems.slice(0, 9);
            if (!items.length) return null;
            return <div className={`chat-mention-group${group.type === "conversation" ? " is-conversation" : ""}`} key={group.type}>
              <div className="chat-mention-group-title">
                <span>{group.icon}{t(group.labelKey)}</span>
                <span className="chat-mention-shortcut"><code>@{group.shortcut}:</code>{t("chat.mentionShortcutHint")}</span>
              </div>
              <div className="chat-mention-options">
              {items.map((item) => {
                const index = visibleCandidates.indexOf(item);
                return <button type="button" role="option" title={item.description ? `${item.name}\n${item.description}` : item.name} aria-selected={activeIndex === index} className={`chat-mention-option${activeIndex === index ? " is-active" : ""}`} key={`${item.type}-${item.id}`} onMouseEnter={() => setActiveIndex(index)} onMouseDown={(event) => { event.preventDefault(); insertCandidate(item); }}>
                  <span className="chat-mention-option-name">{item.name}</span>
                  {group.type === "conversation" && item.description ? <span className="chat-mention-option-description">{item.description}</span> : null}
                </button>;
              })}
              </div>
              {allItems.length > 9 || isExpanded ? (
                <button type="button" className="chat-mention-expand" onMouseDown={(event) => { event.preventDefault(); setExpandedTypes((current) => { const next = new Set(current); if (isExpanded) next.delete(group.type); else next.add(group.type); return next; }); setActiveIndex(-1); }}>
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
