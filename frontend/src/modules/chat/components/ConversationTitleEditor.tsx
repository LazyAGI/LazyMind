import { useCallback, useEffect, useId, useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { Button, Modal } from "antd";
import "./ConversationTitleEditor.scss";
import { useTranslation } from "react-i18next";
import { ChatServiceApi } from "../utils/request";
import { emitConversationGroupsChanged, renameGroupConversation } from "../conversationOrganizer/api";
import { CONVERSATION_TITLE_CHANGED_EVENT, type ConversationTitleChangedDetail } from "../constants/chat";

export default function ConversationTitleEditor({ conversationId, initialTitle = "", onClose }: {
  conversationId: string;
  initialTitle?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [title, setTitle] = useState(initialTitle);
  const [revision, setRevision] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const helpId = useId();
  const returnFocus = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const active = useRef(false);
  const request = useRef(0);
  const pending = useRef(false);
  const keepDraft = useRef(false);
  const originalTitle = useRef(initialTitle);
  const closing = useRef(false);
  const restoreFocus = useRef(false);
  const normalized = title.trim();
  const valid = normalized.length > 0 && Array.from(normalized).length <= 255;

  const load = useCallback(async () => {
    const current = ++request.current;
    setLoading(true);
    setRevision(null);
    try {
      const response = await ChatServiceApi().conversationServiceGetConversationDetail({ conversation: conversationId });
      if (!active.current || current !== request.current) return;
      const item = response.data.conversation as { display_name?: string; title_revision?: number };
      originalTitle.current = item.display_name || "";
      if (!keepDraft.current) setTitle(originalTitle.current);
      setRevision(item.title_revision ?? 0);
      setError(current => current === "conversationRename.loadFailed" ? "" : current);
    } catch {
      if (active.current && current === request.current) setError("conversationRename.loadFailed");
    } finally {
      if (active.current && current === request.current) setLoading(false);
    }
  }, [conversationId]);

  useEffect(() => {
    active.current = true;
    void load();
    return () => {
      active.current = false;
      ++request.current;
      queueMicrotask(() => {
        if (active.current || !restoreFocus.current) return;
        if (returnFocus.current?.isConnected) returnFocus.current.focus();
      });
    };
  }, [load]);

  useEffect(() => {
    if (loading || closing.current) return;
    input.current?.focus();
    if (keepDraft.current) input.current?.setSelectionRange(input.current.value.length, input.current.value.length);
    else input.current?.select();
  }, [loading]);

  const close = (restore: boolean) => {
    closing.current = true;
    restoreFocus.current = restore;
    onClose();
  };

  const save = async () => {
    if (!valid || revision === null || loading || pending.current || closing.current) return;
    restoreFocus.current = true;
    if (normalized === originalTitle.current.trim()) {
      close(true);
      return;
    }
    pending.current = true;
    keepDraft.current = true;
    setSaving(true);
    setError("");
    try {
      const result = await renameGroupConversation(conversationId, normalized, revision);
      window.dispatchEvent(new CustomEvent<ConversationTitleChangedDetail>(CONVERSATION_TITLE_CHANGED_EVENT, {
        detail: { conversationId, displayName: result.display_name ?? normalized, titleRevision: result.title_revision ?? revision + 1 },
      }));
      emitConversationGroupsChanged();
      if (active.current) close(restoreFocus.current);
    } catch (cause) {
      if (!active.current) return;
      if ((cause as { response?: { status?: number } }).response?.status === 409) {
        setError("conversationRename.conflict");
        await load();
      } else {
        setError("conversationRename.saveFailed");
      }
    } finally {
      pending.current = false;
      if (active.current) setSaving(false);
    }
  };

  return <Modal open title={t("conversationOrganizer.renameConversation")} width={420}
    className="conversation-title-modal" maskClosable={false} keyboard={!saving} closable={!saving}
    okText={t("common.save")} cancelText={t("common.cancel")} confirmLoading={saving}
    okButtonProps={{ disabled: loading || !valid || revision === null, "aria-label": t("common.save") }} cancelButtonProps={{ disabled: saving }}
    onOk={() => void save()} onCancel={() => { if (!pending.current) close(true); }}>
    <div className="conversation-title-editor" aria-busy={loading || saving}
    onClick={event => event.stopPropagation()} onPointerDown={event => event.stopPropagation()}
    onKeyDown={event => {
      event.stopPropagation();
      if (event.key === "Escape" && !event.nativeEvent.isComposing && !pending.current) {
        event.preventDefault();
        close(true);
      }
    }}>
    <label htmlFor={`${helpId}-input`}>{t("conversationRename.name")}</label>
    <input autoFocus ref={input} id={`${helpId}-input`} className="conversation-title-editor-input" value={title} readOnly={loading || saving}
      autoComplete="off"
      aria-label={t("conversationRename.name")} aria-describedby={error || (!loading && !valid) ? helpId : undefined}
      aria-invalid={!valid && !loading}
      onChange={(event: ChangeEvent<HTMLInputElement>) => {
        keepDraft.current = true;
        setTitle(event.target.value);
        setError(current => current === "conversationRename.loadFailed" ? current : "");
      }}
      onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
        if (event.key === "Enter" && !event.nativeEvent.isComposing && event.keyCode !== 229) {
          event.preventDefault();
          void save();
        }
      }} />
    {(error || (!loading && !valid)) && <div id={helpId} className="conversation-title-editor-error" role="alert">
      {t(error || "conversationRename.hint")}
      {error === "conversationRename.loadFailed" && <Button type="link" size="small" disabled={loading}
        onClick={() => void load()}>{t("common.retry")}</Button>}
    </div>}
    </div>
  </Modal>;
}
