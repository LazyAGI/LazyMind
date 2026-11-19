import { useCallback, useEffect, useRef, useState } from "react";
import { Modal, message } from "antd";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  ChatServiceApi,
  ConversationSettingsApi,
  parseChatEntryDefaults,
  parseConversationRuntimeSettings,
  type ConversationRuntimeSettings,
} from "../../utils/request";

export function useWorkflowSelection({
  conversationId,
  runInBackground,
  settings,
  onChange,
}: {
  conversationId?: string;
  runInBackground: boolean;
  settings?: ConversationRuntimeSettings;
  onChange: (settings: ConversationRuntimeSettings) => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const settingsRef = useRef(settings);
  settingsRef.current = settings;
  const pending = useRef(false);
  const cancelConfirmation = useRef<(() => void) | undefined>();
  const scope = useRef({});
  useEffect(() => {
    scope.current = {};
    pending.current = false;
    setBusy(false);
    return () => {
      scope.current = {};
      cancelConfirmation.current?.();
    };
  }, [conversationId, runInBackground]);

  const persistedId = conversationId && !conversationId.startsWith("temp_")
    ? conversationId : undefined;
  const loadSettings = useCallback(async () => {
    if (persistedId) {
      const response = await ChatServiceApi().conversationServiceGetConversationDetail({ conversation: persistedId });
      if (!response.data.conversation) {
        throw new Error(t("chat.workflowSelectionSettingsUnavailable"));
      }
      // Historical NULL snapshots use the same hard default as Core's loadUserAgentConfig.
      return { enable_workflow: true, ...parseConversationRuntimeSettings(response.data.conversation) };
    }
    if (settingsRef.current?.enable_workflow !== undefined) return settingsRef.current;
    const response = await ConversationSettingsApi().getChatSettings();
    return parseChatEntryDefaults(response.data)[runInBackground ? "new_task" : "quick_question"].conversation_settings;
  }, [persistedId, runInBackground, t]);

  const beforeWorkflowSelect = useCallback(async (): Promise<boolean> => {
    if (pending.current) return false;
    const currentScope = scope.current;
    pending.current = true;
    setBusy(true);
    try {
      const current = await loadSettings();
      if (scope.current !== currentScope) return false;
      if (current.enable_workflow !== false) {
        onChange(current);
        return true;
      }
      return await new Promise<boolean>((resolve) => {
        const finish = (allowed: boolean) => {
          cancelConfirmation.current = undefined;
          resolve(allowed);
        };
        const modal = Modal.confirm({
          title: t("chat.workflowSelectionDisabledTitle"),
          content: t("chat.workflowSelectionDisabledContent"),
          okText: t("chat.workflowSelectionEnable"),
          cancelText: t("chat.workflowSelectionKeepDisabled"),
          keyboard: false,
          onCancel: () => finish(false),
          onOk: async () => {
            const patch = { enable_workflow: true, workflow_mode: "dynamic" as const };
            modal.update({ cancelButtonProps: { disabled: true } });
            try {
              if (persistedId) {
                await ConversationSettingsApi().patchConversationSettings(persistedId, patch);
              }
              if (scope.current !== currentScope) return;
              const next = { ...current, ...patch };
              settingsRef.current = next;
              onChange(next);
              finish(true);
            } catch (error) {
              message.error(getLocalizedErrorMessage(error));
              finish(false);
            }
          },
        });
        cancelConfirmation.current = () => { modal.destroy(); finish(false); };
      });
    } catch (error) {
      if (scope.current === currentScope) message.error(getLocalizedErrorMessage(error));
      return false;
    } finally {
      if (scope.current === currentScope) {
        pending.current = false;
        setBusy(false);
      }
    }
  }, [loadSettings, onChange, persistedId, t]);

  const validateWorkflowSelection = useCallback(async () => {
    const currentScope = scope.current;
    try {
      const current = await loadSettings();
      if (scope.current !== currentScope) return false;
      if (current.enable_workflow === false) {
        onChange(current);
        message.warning(t("chat.workflowSelectionDisabledNotice"));
        return false;
      }
      return true;
    } catch (error) {
      if (scope.current === currentScope) message.error(getLocalizedErrorMessage(error));
      return false;
    }
  }, [loadSettings, onChange, t]);

  return { beforeWorkflowSelect, validateWorkflowSelection, busy };
}
