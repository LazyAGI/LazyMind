import { useCallback, useRef, useState } from "react";
import { message } from "antd";
import { useTranslation } from "react-i18next";
import type { RefObject } from "react";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { MAX_CITE_MESSAGE_COUNT } from "../utils/citeMessage";

export function useCiteMessagesInput(
  chatInputRef: RefObject<ChatInputImperativeProps>,
) {
  const { t } = useTranslation();
  const [citeMessages, setCiteMessages] = useState<string[]>([]);
  const [citeHistoryIds, setCiteHistoryIds] = useState<(string | undefined)[]>(
    [],
  );
  const citeMessagesRef = useRef(citeMessages);
  citeMessagesRef.current = citeMessages;

  const handleAddCiteMessage = useCallback(
    (text: string, historyId?: string) => {
      const normalizedText = text.trim();
      if (!normalizedText) {
        return;
      }

      // Keep setState pure: warn outside the updater so the tip always surfaces.
      if (citeMessagesRef.current.length >= MAX_CITE_MESSAGE_COUNT) {
        message.warning(
          t("chat.maxCitationsWarning", {
            count: MAX_CITE_MESSAGE_COUNT,
          }),
        );
        return;
      }

      setCiteHistoryIds((prev) =>
        prev.length >= MAX_CITE_MESSAGE_COUNT
          ? prev
          : [...prev, historyId?.trim() || undefined],
      );
      setCiteMessages((prev) =>
        prev.length >= MAX_CITE_MESSAGE_COUNT ? prev : [...prev, normalizedText],
      );
      requestAnimationFrame(() => {
        chatInputRef.current?.focus();
      });
    },
    [chatInputRef, t],
  );

  const handleRemoveCiteMessage = useCallback((index: number) => {
    setCiteMessages((prev) => prev.filter((_, itemIndex) => itemIndex !== index));
    setCiteHistoryIds((prev) => prev.filter((_, itemIndex) => itemIndex !== index));
  }, []);

  const clearCiteMessages = useCallback(() => {
    setCiteMessages([]);
    setCiteHistoryIds([]);
  }, []);

  return {
    citeMessages,
    citeHistoryIds,
    setCiteMessages,
    handleAddCiteMessage,
    handleRemoveCiteMessage,
    clearCiteMessages,
  };
}
