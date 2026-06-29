import { type ChangeEvent, type KeyboardEvent, type ReactNode, type Ref } from "react";
import { Input, Typography } from "antd";
import { MessageOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  type SelfEvolutionChatMessage,
  type SelfEvolutionCheckpointPrompt,
} from "./types";

const { Paragraph, Text } = Typography;
const legacyPlanningThinkingText = "正在理解你的请求并规划下一步。";
const hiddenStatusMessagePrefixes = ["已解析意图：", "正在处理意图："];

type ChatMessageStreamProps = {
  isAutoInteractionActive: boolean;
  messages: SelfEvolutionChatMessage[];
  streamRef: Ref<HTMLDivElement>;
};

export function ChatMessageStream({
  isAutoInteractionActive,
  messages,
  streamRef,
}: ChatMessageStreamProps) {
  const { t } = useTranslation();
  const visibleMessages = messages
    .map((item) => ({ ...item, content: item.content.replaceAll(legacyPlanningThinkingText, "").trim() }))
    .filter((item) => item.content && !hiddenStatusMessagePrefixes.some((prefix) => item.content.startsWith(prefix)));
  return (
    <div
      ref={streamRef}
      className="self-evolution-chat-stream"
      aria-live="polite"
      aria-label={t("selfEvolutionRun.chatStreamAria")}
    >
      {visibleMessages.length > 0 ? (
        visibleMessages.map((item) => (
          <article
            key={item.id}
            className={`self-evolution-bubble is-${item.role}`}
            data-self-evolution-message-id={item.id}
          >
            {item.agentLabel && (
              <Text className="self-evolution-bubble-agent-label">{item.agentLabel}</Text>
            )}
            <Paragraph>{item.content}</Paragraph>
            <Text>{item.time}</Text>
          </article>
        ))
      ) : (
        <Paragraph className="self-evolution-chat-empty">
          {isAutoInteractionActive
            ? t("selfEvolutionRun.autoMessagesPlaceholder")
            : t("selfEvolutionRun.emptyChatPlaceholder")}
        </Paragraph>
      )}
    </div>
  );
}

export function AutoInteractionStatus() {
  const { t } = useTranslation();
  return (
    <div className="self-evolution-auto-interaction-status" role="status" aria-live="polite">
      <MessageOutlined />
      <Text>{t("selfEvolutionRun.autoInteractionStatus")}</Text>
    </div>
  );
}

type ChatComposerProps = {
  activeStepText: string;
  isAutoMode: boolean;
  isReadOnlyEnded?: boolean;
  isSendingMessage: boolean;
  pendingCheckpointWaitPrompt?: SelfEvolutionCheckpointPrompt;
  prompt: string;
  onPromptChange: (value: string) => void;
  onSend: (command?: string) => void;
  renderKnowledgeAndModeTools: () => ReactNode;
  renderSendButton: () => ReactNode;
};

export function ChatComposer({
  activeStepText,
  isAutoMode,
  isReadOnlyEnded,
  isSendingMessage,
  pendingCheckpointWaitPrompt,
  prompt,
  onPromptChange,
  onSend,
  renderKnowledgeAndModeTools,
  renderSendButton,
}: ChatComposerProps) {
  const { t } = useTranslation();

  if (isAutoMode) {
    if (isReadOnlyEnded) {
      return null;
    }

    return (
      <div className="self-evolution-chat-composer is-auto">
        <AutoInteractionStatus />
      </div>
    );
  }

  const onInputChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    onPromptChange(event.target.value);
  };

  const onInputPressEnter = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.shiftKey) {
      return;
    }
    event.preventDefault();
    if (isSendingMessage) {
      return;
    }
    if (prompt.trim()) {
      onSend();
    }
  };
  const isCheckpointWaiting = Boolean(pendingCheckpointWaitPrompt);

  return (
    <div className="self-evolution-chat-composer">
      <Input.TextArea
        value={prompt}
        onChange={onInputChange}
        autoSize={{ minRows: 2, maxRows: 4 }}
        className="self-evolution-chatlike-input"
        placeholder={
          isCheckpointWaiting
            ? t("selfEvolutionRun.checkpointInputPlaceholder", { command: pendingCheckpointWaitPrompt?.command || t("selfEvolutionRun.continueExecution") })
            : t("selfEvolutionRun.inputPlaceholder")
        }
        aria-label={t("selfEvolutionRun.inputAria")}
        onPressEnter={onInputPressEnter}
      />

      <div className="self-evolution-chat-composer-footer">
        <div className="self-evolution-chat-composer-left">
          {renderKnowledgeAndModeTools()}
        </div>

        <div className="self-evolution-chatlike-actions">
          <Text className="self-evolution-chatlike-helper">
            {isSendingMessage ? t("selfEvolutionRun.sendingMessage") : activeStepText}
          </Text>
          {renderSendButton()}
        </div>
      </div>
    </div>
  );
}
