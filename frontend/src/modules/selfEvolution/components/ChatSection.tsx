import { type ChangeEvent, type KeyboardEvent, type ReactNode, type Ref } from "react";
import { Input, Typography } from "antd";
import { MessageOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  type SelfEvolutionChatMessage,
  type SelfEvolutionCheckpointPrompt,
} from "./types";

const { Paragraph, Text } = Typography;

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
  return (
    <div
      ref={streamRef}
      className="self-evolution-chat-stream"
      aria-live="polite"
      aria-label={t("selfEvolutionRun.chatStreamAria")}
    >
      {messages.length > 0 ? (
        messages.map((item) => (
          <article key={item.id} className={`self-evolution-bubble is-${item.role}`}>
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
  isSendingMessage,
  pendingCheckpointWaitPrompt,
  prompt,
  onPromptChange,
  onSend,
  renderKnowledgeAndModeTools,
  renderSendButton,
}: ChatComposerProps) {
  const { t } = useTranslation();
  const onInputChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    onPromptChange(event.target.value);
  };

  const onInputPressEnter = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.shiftKey) {
      return;
    }
    event.preventDefault();
    if (prompt.trim()) {
      onSend();
    }
  };
  const isCheckpointWaiting = Boolean(pendingCheckpointWaitPrompt);

  return (
    <div className={`self-evolution-chat-composer${isAutoMode ? " is-auto" : ""}`}>
      {isAutoMode && <AutoInteractionStatus />}

      <Input.TextArea
        value={prompt}
        onChange={onInputChange}
        autoSize={{ minRows: 2, maxRows: 4 }}
        className="self-evolution-chatlike-input"
        placeholder={
          isCheckpointWaiting
            ? t("selfEvolutionRun.checkpointInputPlaceholder")
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
