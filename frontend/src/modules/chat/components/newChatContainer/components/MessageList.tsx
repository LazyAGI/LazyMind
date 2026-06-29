import React, { useEffect, useMemo, useRef, useState } from "react";
import { Button, Input, Space, Tooltip } from "antd";
import {
  CloseOutlined,
  CommentOutlined,
  CopyOutlined,
  EditOutlined,
} from "@ant-design/icons";
import { RoleTypes } from "@/modules/chat/constants/common";
import AssistantMessage from "../../AssistantMessage";
import type { PreferenceType } from "../../MultiAnswerDisplay";
import "../index.scss";
import dayjs from "dayjs";

interface MessageListProps {
  messageList: any[];
  initialCard?: React.ReactNode;
  sendMessage: (text: string, clearInput?: boolean) => void;
  regenerate: () => void;
  stopGeneration: () => void;
  renderText: (item: any) => React.ReactNode;
  updateAssistantMessage: (data: any, id?: string, index?: number) => void;
  onScroll?: () => void;
  chatContentRef?: React.RefObject<HTMLDivElement>;
  sessionId?: string;
  onPreferenceSelect?: (preference: PreferenceType, sessionId?: string) => void;
  editingUserMessageIndex?: number | null;
  editingUserMessageText?: string;
  editingUserMessageCites?: string[];
  onUserMessageEditTextChange?: (value: string) => void;
  onRemoveEditingUserMessageCite?: (index: number) => void;
  onStartEditUserMessage?: (item: any, index: number) => void;
  onCancelEditUserMessage?: () => void;
  onResendEditedUserMessage?: (index: number, value: string) => void;
  onCopyUserMessage?: (item: any) => void;
  onCiteMessage?: (text: string) => void;
  footer?: React.ReactNode;
}

function splitCiteMessages(citeMessage?: string) {
  return (citeMessage || "")
    .split(/\n{2,}/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function getCiteMessages(message?: { cite_message?: string; cite_messages?: string[] }) {
  if (Array.isArray(message?.cite_messages)) {
    return message.cite_messages.map((item) => item.trim()).filter(Boolean);
  }
  const textInput = (message as any)?.inputs?.find((input: any) => {
    const inputType = input?.input_type || "text";
    return inputType === "text" && typeof input?.text === "string";
  });
  const inputCites = Array.from(
    `${textInput?.text || ""}`.matchAll(/<cite_message>([\s\S]*?)<\/cite_message>/gi),
  )
    .map((match) => match[1]?.trim())
    .filter(Boolean);
  if (inputCites.length > 0) {
    return inputCites;
  }
  return splitCiteMessages(message?.cite_message);
}

function UserCitationPreview({ citeMessages }: { citeMessages: string[] }) {
  const [expanded, setExpanded] = useState(false);
  const [isHiding, setIsHiding] = useState(false);
  const hideTimerRef = useRef<number | null>(null);

  const clearHideTimer = () => {
    if (hideTimerRef.current) {
      window.clearTimeout(hideTimerRef.current);
      hideTimerRef.current = null;
    }
  };

  const showCitation = () => {
    clearHideTimer();
    setIsHiding(false);
    setExpanded(true);
  };

  const handleClick = (event: React.MouseEvent<HTMLButtonElement>) => {
    if (event.detail >= 2) {
      showCitation();
    }
  };

  const handleMouseDown = (event: React.MouseEvent<HTMLButtonElement>) => {
    if (event.detail >= 2) {
      showCitation();
    }
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      showCitation();
    }
  };

  const handleMouseEnter = () => {
    if (!expanded) {
      return;
    }
    clearHideTimer();
    setIsHiding(false);
  };

  const handleMouseLeave = () => {
    if (!expanded) {
      return;
    }
    clearHideTimer();
    setIsHiding(true);
    hideTimerRef.current = window.setTimeout(() => {
      setExpanded(false);
      setIsHiding(false);
      hideTimerRef.current = null;
    }, 500);
  };

  const primaryCiteMessage = citeMessages[0] || "";

  useEffect(() => clearHideTimer, []);

  return (
    <button
      type="button"
      className={`chat-user-citation-preview${expanded ? " is-expanded" : ""}${
        isHiding ? " is-hiding" : ""
      }`}
      onClick={handleClick}
      onDoubleClick={showCitation}
      onMouseDown={handleMouseDown}
      onKeyDown={handleKeyDown}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
      onPointerEnter={handleMouseEnter}
      onPointerLeave={handleMouseLeave}
      title={expanded ? "" : primaryCiteMessage}
      aria-label={primaryCiteMessage}
    >
      {!expanded ? (
        <CommentOutlined className="chat-user-citation-preview-icon" />
      ) : (
        <div className="chat-user-citation-preview-content">
          {citeMessages.map((citeMessage, citeIndex) => (
            <div
              className="chat-user-citation-preview-item"
              key={`${citeIndex}-${citeMessage}`}
            >
              {citeMessage}
            </div>
          ))}
        </div>
      )}
    </button>
  );
}

const MessageList: React.FC<MessageListProps> = ({
  messageList,
  initialCard,
  sendMessage,
  regenerate,
  stopGeneration,
  renderText,
  updateAssistantMessage,
  onScroll,
  chatContentRef,
  sessionId = "",
  onPreferenceSelect,
  editingUserMessageIndex = null,
  editingUserMessageText = "",
  editingUserMessageCites = [],
  onUserMessageEditTextChange,
  onRemoveEditingUserMessageCite,
  onStartEditUserMessage,
  onCancelEditUserMessage,
  onResendEditedUserMessage,
  onCopyUserMessage,
  onCiteMessage,
  footer,
}) => {
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const editComposeRef = useRef(false);

  const contentRef = chatContentRef || scrollContainerRef;
  const lastUserIndex = useMemo(
    () =>
      messageList.reduce(
        (lastIndex, msg, idx) => (msg.role === RoleTypes.USER ? idx : lastIndex),
        -1,
      ),
    [messageList],
  );

  const handleEditMessageKeyDown = (
    event: React.KeyboardEvent<HTMLTextAreaElement>,
    index: number,
  ) => {
    if (event.key !== "Enter" || event.shiftKey) {
      return;
    }

    if (
      editComposeRef.current ||
      event.nativeEvent.isComposing ||
      event.nativeEvent.keyCode === 229
    ) {
      return;
    }

    event.preventDefault();
    onResendEditedUserMessage?.(index, editingUserMessageText);
  };

  const renderUser = (item: any, index: number) => {
    const isLastUserMessage = index === lastUserIndex;
    const isEditing = editingUserMessageIndex === index;
    const citeMessageList = isEditing
      ? editingUserMessageCites
      : getCiteMessages(item);

    return (
      <div className="user-message-row">
        {item.create_time && (
          <div className="chat-time">
            {dayjs(item.create_time).format("MM/DD HH:mm")}
          </div>
        )}
        <div className={`user-wrap ${isEditing ? "editing" : ""}`}>
          {!isEditing && citeMessageList.length > 0 ? (
            <UserCitationPreview citeMessages={citeMessageList} />
          ) : null}
          <div className="chat-user">
            {isEditing ? (
              <div className="chat-user-edit-wrap">
                {citeMessageList.length > 0 ? (
                  <div className="chat-user-edit-citation-list">
                    {citeMessageList.map((citeMessage, citeIndex) => (
                      <div
                        className="chat-user-edit-citation"
                        key={`${citeIndex}-${citeMessage}`}
                      >
                        <CommentOutlined className="chat-user-edit-citation-icon" />
                        <Tooltip
                          title={citeMessage}
                          placement="topLeft"
                          overlayClassName="chat-user-citation-tooltip"
                        >
                          <span className="chat-user-edit-citation-text">
                            {citeMessage}
                          </span>
                        </Tooltip>
                        <Button
                          type="text"
                          size="small"
                          className="chat-user-edit-citation-close"
                          icon={<CloseOutlined />}
                          onClick={() => onRemoveEditingUserMessageCite?.(citeIndex)}
                        />
                      </div>
                    ))}
                  </div>
                ) : null}
                <Input.TextArea
                  value={editingUserMessageText}
                  autoSize={{ minRows: 2, maxRows: 6 }}
                  onChange={(event) =>
                    onUserMessageEditTextChange?.(event.target.value)
                  }
                  onCompositionStart={() => {
                    editComposeRef.current = true;
                  }}
                  onCompositionEnd={() => {
                    editComposeRef.current = false;
                  }}
                  onKeyDown={(event) => handleEditMessageKeyDown(event, index)}
                />
                <Space size={6} className="chat-user-edit-actions">
                  <Button
                    className="chat-user-edit-btn cancel-btn"
                    onClick={onCancelEditUserMessage}
                  >
                    取消
                  </Button>
                  <Button
                    className="chat-user-edit-btn send-btn"
                    onClick={() =>
                      onResendEditedUserMessage?.(
                        index,
                        editingUserMessageText,
                      )
                    }
                  >
                    发送
                  </Button>
                </Space>
              </div>
            ) : (
              renderText(item)
            )}
          </div>
          {!isEditing ? (
            <div className="chat-user-toolbar">
              <Tooltip title="复制">
                <Button
                  type="text"
                  size="small"
                  icon={<CopyOutlined />}
                  onClick={() => onCopyUserMessage?.(item)}
                />
              </Tooltip>
              {isLastUserMessage ? (
                <Tooltip title="编辑并重发">
                  <Button
                    type="text"
                    size="small"
                    icon={<EditOutlined />}
                    onClick={() => onStartEditUserMessage?.(item, index)}
                  />
                </Tooltip>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    );
  };

  return (
    <div
      className="message-container chat-content"
      style={{ flex: messageList.length > 0 ? 1 : undefined }}
      ref={contentRef}
      onScroll={onScroll}
    >
      {messageList.length > 0 &&
        messageList.map((item, index) => {
          return (
            <div className="chat-item" key={`chat-${index}`}>
              {item.role === RoleTypes.USER && renderUser(item, index)}
              {item.role === RoleTypes.ASSISTANT && (
                <AssistantMessage
                  item={item}
                  index={index}
                  length={messageList.length}
                  sendMessage={sendMessage}
                  regenerate={regenerate}
                  stopGeneration={stopGeneration}
                  renderText={renderText}
                  updateMessage={(msg: any) =>
                    updateAssistantMessage(msg, msg.id || msg.history_id, index)
                  }
                  sessionId={sessionId}
                  onPreferenceSelect={onPreferenceSelect}
                  onCiteMessage={onCiteMessage}
                  isLatestDualAnswer={
                    index === messageList.length - 1 &&
                    !!(
                      item.answers &&
                      Array.isArray(item.answers) &&
                      item.answers.length >= 2
                    )
                  }
                />
              )}
            </div>
          );
        })}

      {messageList.length === 0 && initialCard}
      {footer}
    </div>
  );
};

export default MessageList;
