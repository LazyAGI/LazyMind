import { useEffect, useId, useRef, useState } from "react";
import { CloseOutlined } from "@ant-design/icons";
import { Button } from "antd";
import { useTranslation } from "react-i18next";
import SideChatPanel, { type SideChatPanelProps } from "../SideChatPanel";
import { ChatSourcePanel } from "../AssistantMessage";
import { getSourceDedupKey, type ChatSource } from "@/modules/chat/utils/sourceAdapter";
import "./index.scss";

export interface SourceRequest {
  sources: ChatSource[];
  origin: "main" | "side";
  summary?: string;
}

export default function ChatContextPanel({ sideChat, sourceRequest, visible, resumeRequest, onStateChange }: {
  sideChat?: SideChatPanelProps;
  sourceRequest?: SourceRequest;
  visible: boolean;
  resumeRequest?: number;
  onStateChange?: (state: { collapsed: boolean; unread: boolean }) => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const [tab, setTab] = useState<"side" | "sources">(sourceRequest ? "sources" : "side");
  const [collapsed, setCollapsed] = useState(false);
  const [references, setReferences] = useState(sourceRequest);
  const [unread, setUnread] = useState(false);
  const streaming = useRef(false);
  const returnFocus = useRef<HTMLElement | null>(null);
  const sideTab = useRef<HTMLButtonElement>(null);
  const sourcesTab = useRef<HTMLButtonElement>(null);

  const selectTab = (next: "side" | "sources") => {
    setTab(next);
    if (next === "side") setUnread(false);
  };
  const captureFocus = () => {
    if (document.activeElement instanceof HTMLElement) returnFocus.current = document.activeElement;
  };
  useEffect(() => {
    if (!sideChat) return;
    captureFocus();
    setCollapsed(false);
    setTab("side");
    setUnread(false);
  }, [sideChat?.source]);
  useEffect(() => {
    if (!sourceRequest) return;
    captureFocus();
    setReferences(sourceRequest);
    setTab("sources");
    setCollapsed(false);
    requestAnimationFrame(() => sourcesTab.current?.focus());
  }, [sourceRequest]);
  useEffect(() => {
    if (!sideChat && references) setTab("sources");
  }, [sideChat, references]);

  useEffect(() => {
    if (visible && !collapsed && tab === "side") setUnread(false);
  }, [visible, collapsed, tab]);

  useEffect(() => { onStateChange?.({ collapsed, unread }); }, [collapsed, unread, onStateChange]);
  useEffect(() => {
    if (!resumeRequest) return;
    setCollapsed(false);
    setTab("side");
    setUnread(false);
    requestAnimationFrame(() => sideTab.current?.focus());
  }, [resumeRequest]);

  const collapse = () => {
    setCollapsed(true);
    returnFocus.current?.focus();
  };

  return (
    <aside className={`chat-context-panel${collapsed ? " is-collapsed" : ""}`} hidden={!visible}
      aria-label={t("chat.contextPanel.title")}
      onKeyDown={event => {
        if (event.key === "Escape") { event.stopPropagation(); collapse(); }
        if (event.key === "Tab" && !collapsed && window.innerWidth <= 640) {
          const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>("button:not(:disabled), a[href], input:not(:disabled), [tabindex='0'], [contenteditable='true']"))
            .filter(element => element.getClientRects().length > 0 && element.tabIndex >= 0);
          const first = controls[0];
          const last = controls[controls.length - 1];
          if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
          else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
        }
      }}>
      <div className="chat-context-surface" hidden={collapsed}>
        <header className="chat-context-header">
          <div role="tablist" aria-label={t("chat.contextPanel.title")} onKeyDown={event => {
            if (!sideChat || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
            event.preventDefault();
            const next = event.key === "Home" ? "side" : event.key === "End" ? "sources" : tab === "side" ? "sources" : "side";
            selectTab(next);
            (next === "side" ? sideTab : sourcesTab).current?.focus();
          }}>
            {sideChat && <button ref={sideTab} role="tab" id={`${id}-side-tab`} aria-controls={`${id}-side`}
              aria-selected={tab === "side"} tabIndex={tab === "side" ? 0 : -1} onClick={() => selectTab("side")}>
              {t("chat.sideChat.title")}{unread && <span className="chat-context-unread" aria-label={t("chat.contextPanel.newReply")} />}
            </button>}
            <button ref={sourcesTab} role="tab" id={`${id}-sources-tab`} aria-controls={`${id}-sources`}
              aria-selected={tab === "sources"} tabIndex={tab === "sources" ? 0 : -1} onClick={() => selectTab("sources")}>
              {t("chat.references")} {!!references?.sources.length && <span className="chat-context-count">{references.sources.length}</span>}
            </button>
          </div>
          <Button type="text" icon={<CloseOutlined />} aria-label={t("chat.contextPanel.collapse")} onClick={collapse} />
        </header>
        <div role="tabpanel" id={`${id}-side`} aria-labelledby={`${id}-side-tab`} hidden={tab !== "side"} className="chat-context-content">
          {sideChat && <SideChatPanel {...sideChat} embedded visible={visible && sideChat.visible !== false && !collapsed && tab === "side"}
            onOpenSources={(sources, summary) => { setReferences({ sources, summary, origin: "side" }); setTab("sources"); }}
            onStreamingChange={next => {
              if (streaming.current && !next && (tab !== "side" || collapsed || !visible)) setUnread(true);
              streaming.current = next;
            }} />}
        </div>
        <div role="tabpanel" id={`${id}-sources`} aria-labelledby={`${id}-sources-tab`} hidden={tab !== "sources"} className="chat-context-content">
          {references?.sources.length ? <>
            <div className="chat-context-origin">
              <strong>{t(references.origin === "side" ? "chat.contextPanel.sideSources" : "chat.contextPanel.mainSources")}</strong>
              {references.summary && <p>{references.summary}</p>}
            </div>
            <ChatSourcePanel key={references.sources.map(getSourceDedupKey).join("|")} sources={references.sources} embedded onClose={collapse} />
          </> : <p className="chat-context-empty">{t("chat.contextPanel.noSources")}</p>}
        </div>
      </div>
    </aside>
  );
}
