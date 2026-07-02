import { useEffect, useRef, useState } from "react";
import { message } from "antd";
import { useTranslation } from "react-i18next";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import { userMessageEditDraftStore } from "@/modules/chat/store/userMessageEditDraft";
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import { streamManager } from "@/modules/chat/utils/StreamManager";
import {
  buildCitedMessageText,
  findLastUserMessageIndex,
  getCiteMessages,
} from "../utils/citeMessage";

interface UseUserMessageEditOptions {
  canChat: boolean;
  disabledReason?: string;
  loading: boolean;
  activeStreamRef: React.MutableRefObject<boolean>;
  messageList: any[];
  messageListRef: React.MutableRefObject<any[]>;
  setMessageList: React.Dispatch<React.SetStateAction<any[]>>;
  currentConversationIdRef: React.MutableRefObject<string>;
  conversationMessagesCache: React.MutableRefObject<Map<string, any[]>>;
  openSSE: (
    input: any[],
    action: ChatConversationsRequestActionEnum,
  ) => void;
  scrollToEnd: () => void;
}

export function useUserMessageEdit({
  canChat,
  disabledReason,
  loading,
  activeStreamRef,
  messageList,
  messageListRef,
  setMessageList,
  currentConversationIdRef,
  conversationMessagesCache,
  openSSE,
  scrollToEnd,
}: UseUserMessageEditOptions) {
  const { t } = useTranslation();
  const [editingUserMessageIndex, setEditingUserMessageIndex] = useState<
    number | null
  >(null);
  const [editingUserMessageText, setEditingUserMessageText] = useState("");
  const [editingUserMessageCites, setEditingUserMessageCites] = useState<
    string[]
  >([]);
  const editingUserMessageIndexRef = useRef<number | null>(null);
  const editingUserMessageTextRef = useRef("");
  const editingUserMessageCitesRef = useRef<string[]>([]);

  useEffect(() => {
    editingUserMessageIndexRef.current = editingUserMessageIndex;
    editingUserMessageTextRef.current = editingUserMessageText;
    editingUserMessageCitesRef.current = editingUserMessageCites;
  }, [editingUserMessageIndex, editingUserMessageText, editingUserMessageCites]);

  useEffect(() => {
    const conversationId = currentConversationIdRef.current;
    if (!conversationId || editingUserMessageIndex === null) {
      return;
    }
    userMessageEditDraftStore.setDraft(conversationId, {
      text: editingUserMessageText,
      cites: editingUserMessageCites,
    });
  }, [
    currentConversationIdRef,
    editingUserMessageIndex,
    editingUserMessageText,
    editingUserMessageCites,
  ]);

  useEffect(() => {
    return () => {
      const conversationId = currentConversationIdRef.current;
      if (conversationId && editingUserMessageIndexRef.current !== null) {
        userMessageEditDraftStore.setDraft(conversationId, {
          text: editingUserMessageTextRef.current,
          cites: editingUserMessageCitesRef.current,
        });
      }
    };
  }, [currentConversationIdRef]);

  useEffect(() => {
    if (editingUserMessageIndex === null) {
      return;
    }
    if (
      editingUserMessageIndex < 0 ||
      editingUserMessageIndex >= messageList.length ||
      messageList[editingUserMessageIndex]?.role !== RoleTypes.USER
    ) {
      resetEditState();
    }
  }, [editingUserMessageIndex, messageList]);

  function resetEditState() {
    setEditingUserMessageIndex(null);
    setEditingUserMessageText("");
    setEditingUserMessageCites([]);
  }

  function persistCurrentUserMessageEditDraft(conversationId?: string) {
    const targetConversationId =
      conversationId || currentConversationIdRef.current;
    if (!targetConversationId || editingUserMessageIndexRef.current === null) {
      return;
    }
    userMessageEditDraftStore.setDraft(targetConversationId, {
      text: editingUserMessageTextRef.current,
      cites: editingUserMessageCitesRef.current,
    });
  }

  function clearCurrentUserMessageEditDraft(conversationId?: string) {
    const targetConversationId =
      conversationId || currentConversationIdRef.current;
    if (!targetConversationId) {
      return;
    }
    userMessageEditDraftStore.clearDraft(targetConversationId);
  }

  function restoreUserMessageEditDraft(conversationId: string, list: any[]) {
    const draft = userMessageEditDraftStore.getDraft(conversationId);
    if (!draft) {
      return;
    }
    const lastUserIndex = findLastUserMessageIndex(list);
    if (lastUserIndex < 0) {
      return;
    }
    setEditingUserMessageIndex(lastUserIndex);
    setEditingUserMessageText(draft.text);
    setEditingUserMessageCites(draft.cites);
  }

  function handleStartEditUserMessage(item: any, index: number) {
    if (!canChat) {
      if (disabledReason) {
        message.warning(disabledReason);
      }
      return;
    }
    if (loading || activeStreamRef.current) {
      return;
    }
    const conversationId = currentConversationIdRef.current;
    const draft = conversationId
      ? userMessageEditDraftStore.getDraft(conversationId)
      : null;
    setEditingUserMessageIndex(index);
    if (draft) {
      setEditingUserMessageText(draft.text);
      setEditingUserMessageCites(draft.cites);
      return;
    }
    setEditingUserMessageText(item?.delta || "");
    setEditingUserMessageCites(getCiteMessages(item));
  }

  function handleCancelEditUserMessage() {
    clearCurrentUserMessageEditDraft();
    resetEditState();
  }

  function handleRemoveEditingUserMessageCite(index: number) {
    setEditingUserMessageCites((prev) =>
      prev.filter((_, itemIndex) => itemIndex !== index),
    );
  }

  function handleResendEditedUserMessage(index: number, value: string) {
    if (!canChat) {
      if (disabledReason) {
        message.warning(disabledReason);
      }
      return;
    }
    if (loading || activeStreamRef.current) {
      return;
    }
    const normalizedText = value.trim();
    if (!normalizedText) {
      return;
    }

    const oldUserMessage = messageListRef.current[index];
    if (!oldUserMessage || oldUserMessage.role !== RoleTypes.USER) {
      return;
    }

    const oldInputs = Array.isArray(oldUserMessage.inputs)
      ? oldUserMessage.inputs
      : [];
    const textWithCitation = buildCitedMessageText(
      normalizedText,
      editingUserMessageCites,
    );
    const rebuiltInputs = oldInputs
      .filter((input: any) => (input?.input_type || "text") !== "text")
      .map((input: any) => ({ ...input }));
    rebuiltInputs.unshift({ input_type: "text", text: textWithCitation });

    const newUserMessage = {
      ...oldUserMessage,
      delta: normalizedText,
      display_delta: normalizedText,
      cite_message: editingUserMessageCites.join("\n\n"),
      cite_messages: editingUserMessageCites,
      inputs: rebuiltInputs,
    };

    const assistantMessage = {
      role: RoleTypes.ASSISTANT,
      delta: "",
      reasoning_content: "",
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      answers: [],
      sources: [],
      model_mode: "value_engineering",
    };

    const truncated = messageListRef.current.slice(0, index);
    const newList = [...truncated, newUserMessage, assistantMessage];
    messageListRef.current = newList;
    setMessageList(newList);
    clearCurrentUserMessageEditDraft();
    resetEditState();

    const currentId = currentConversationIdRef.current;
    if (currentId) {
      conversationMessagesCache.current.set(currentId, newList);
      streamManager.saveMessageList(currentId, newList);
    }

    scrollToEnd();
    openSSE(rebuiltInputs, ChatConversationsRequestActionEnum.ChatActionRegeneration);

    if (currentId && !currentId.startsWith("temp_")) {
      emitConversationActivity({ conversationId: currentId });
    }
  }

  async function handleCopyUserMessage(item: any) {
    const text = (item?.delta || "").trim();
    if (!text) {
      return;
    }
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        message.success(t("chat.copySuccess"));
        return;
      }
    } catch {
      // fall through to legacy copy
    }

    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.top = "0";
    textarea.style.left = "0";
    textarea.style.opacity = "0";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    try {
      const copied = document.execCommand("copy");
      if (copied) {
        message.success(t("chat.copySuccess"));
      } else {
        message.error(t("chat.copyFailedManual"));
      }
    } finally {
      document.body.removeChild(textarea);
    }
  }

  return {
    editingUserMessageIndex,
    editingUserMessageText,
    editingUserMessageCites,
    setEditingUserMessageText,
    editingUserMessageIndexRef,
    editingUserMessageTextRef,
    editingUserMessageCitesRef,
    persistCurrentUserMessageEditDraft,
    clearCurrentUserMessageEditDraft,
    restoreUserMessageEditDraft,
    resetEditState,
    handleStartEditUserMessage,
    handleCancelEditUserMessage,
    handleRemoveEditingUserMessageCite,
    handleResendEditedUserMessage,
    handleCopyUserMessage,
  };
}
