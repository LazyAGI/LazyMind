import { Flex, Spin, Tooltip } from "antd";
import {
  CommentOutlined,
  DownOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { ChatConversationsResponseFinishReasonEnum } from "@/api/generated/chatbot-client";
import MarkdownViewer from "@/modules/chat/components/MarkdownViewer";
import { RoleTypes } from "@/modules/chat/constants/common";
import {
  formatThinkingForDisplay,
} from "@/modules/chat/utils/thinking";
import { useTranslation } from "react-i18next";
import ChatImages from "../../ChatImages";
import ChatFiles from "../../ChatFiles";
import { getCiteMessages } from "../utils/citeMessage";

const ThinkIcon = new URL("../../../assets/images/think.png", import.meta.url)
  .href;

interface ChatMessageContentProps {
  item: any;
  uniqueKey?: string;
  isThinkingCollapsed: (key: string) => boolean;
  onToggleThinkingCollapse: (key: string) => void;
}

export default function ChatMessageContent({
  item,
  uniqueKey,
  isThinkingCollapsed,
  onToggleThinkingCollapse,
}: ChatMessageContentProps) {
  const { t } = useTranslation();
  const thinkingKey = uniqueKey || item.history_id || item.id || "default";
  const isCollapsed = isThinkingCollapsed(thinkingKey);
  const citeMessageList =
    item.role === RoleTypes.USER ? getCiteMessages(item) : [];
  const isStreaming =
    item.finish_reason !==
    ChatConversationsResponseFinishReasonEnum.FinishReasonStop;

  return (
    <Flex vertical>
      {item.images && <ChatImages images={item.images} />}
      {item.files && <ChatFiles files={item.files} />}
      {citeMessageList.length > 0 ? (
        <Tooltip
          placement="topRight"
          overlayClassName="chat-user-citation-tooltip"
          title={
            <div className="chat-user-citation-tooltip-content">
              {citeMessageList.map((citeMessage, index) => (
                <div
                  className="chat-user-citation-tooltip-item"
                  key={`${index}-${citeMessage}`}
                >
                  {citeMessage}
                </div>
              ))}
            </div>
          }
        >
          <span className="chat-user-citation-icon" aria-label={t("chat.cite")}>
            <CommentOutlined />
          </span>
        </Tooltip>
      ) : null}
      {item.reasoning_content && (
        <>
          <div
            className="chat-think-status"
            onClick={() => onToggleThinkingCollapse(thinkingKey)}
          >
            <img src={ThinkIcon} className="chat-think-icon" alt="" />
            <span className="chat-think-title">
              {item.delta ? "已深度思考" : "思考中"}
              {(item.thinking_duration_s || item.thinking_time_s) &&
                item.thinking_duration_s !== "0" &&
                item.thinking_time_s !== "0" &&
                ` (${item.thinking_duration_s || item.thinking_time_s}s)`}
            </span>
            {isCollapsed ? (
              <UpOutlined className="chat-arrow-icon" />
            ) : (
              <DownOutlined className="chat-arrow-icon" />
            )}
          </div>
          <div className={isCollapsed ? "chat-collapse" : "chat-expand"}>
            <div className="chat-think-text">
              <MarkdownViewer sources={item.sources} IS_STREAMING={isStreaming}>
                {formatThinkingForDisplay(item.reasoning_content)}
              </MarkdownViewer>
            </div>
            {!item.delta && isStreaming && <Spin />}
          </div>
        </>
      )}
      <div className="chat-text">
        <MarkdownViewer sources={item.sources} IS_STREAMING={isStreaming}>
          {item.display_delta || item.delta}
        </MarkdownViewer>
      </div>
    </Flex>
  );
}
