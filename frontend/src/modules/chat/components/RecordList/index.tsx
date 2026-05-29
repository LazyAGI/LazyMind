import { CloseOutlined, CloudDownloadOutlined } from "@ant-design/icons";
import classnames from "classnames";
import {
  Button,
  Checkbox,
  Col,
  Input,
  message,
  Row,
  Spin,
  Tooltip,
} from "antd";
import { Conversation } from "@/api/generated/chatbot-client";
import {
  Configuration as CoreConfiguration,
  ConversationsApiFactory,
} from "@/api/generated/core-client";
import {
  useEffect,
  useMemo,
  useRef,
  forwardRef,
  useImperativeHandle,
} from "react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import InfiniteScroll from "react-infinite-scroll-component";
import { axiosInstance, BASE_URL } from "@/components/request";
import { useChatThinkStore } from "@/modules/chat/store/chatThink";
import { useChatNewMessageStore } from "@/modules/chat/store/chatNewMessage";

import dayjs from "dayjs";

import { ChatServiceApi } from "@/modules/chat/utils/request";
import "./index.scss";
import { downloadStream } from "@/modules/chat/utils/download";

const EXPORT_FILE_TYPE_XLSX = "EXPORT_FILE_TYPE_XLSX";
const SIDEBAR_SEARCH_DEBOUNCE_MS = 300;
const conversationsClient = ConversationsApiFactory(
  new CoreConfiguration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

function getExportFileId(uri?: string) {
  if (!uri) return "";
  const matched = uri.match(/\/conversation:export\/files\/([^/?#]+)/);
  return matched?.[1] ?? "";
}

function getDownloadFileName(contentDisposition?: string) {
  if (!contentDisposition) return "conversations-export";
  const utf8Matched = contentDisposition.match(/filename\*=UTF-8''([^;]+)/i);
  if (utf8Matched?.[1]) {
    return decodeURIComponent(utf8Matched[1]);
  }
  const matched = contentDisposition.match(/filename="?([^"]+)"?/i);
  return matched?.[1] ?? "conversations-export";
}

interface IRecordList {
  currentSessionId: string;
  onSelected: (props: Conversation) => void;
  onRemove: (props: Conversation) => void;
  compact?: boolean;
  hideHeader?: boolean;
  hideSearch?: boolean;
  showBatchActions?: boolean;
  searchText?: string;
  title?: string;
}

export interface RecordListImperativeProps {
  refresh: () => void;
}

const { Search } = Input;

type ConversationGroup = "today" | "recentWeek" | "earlier";

function getConversationGroup(updateTime?: string): ConversationGroup {
  const parsedTime = dayjs(updateTime);
  if (!parsedTime.isValid()) {
    return "earlier";
  }
  const todayStart = dayjs().startOf("day");
  if (parsedTime.isSame(todayStart, "day")) {
    return "today";
  }
  if (parsedTime.isAfter(todayStart.subtract(7, "day"))) {
    return "recentWeek";
  }
  return "earlier";
}

const RecordList = forwardRef<RecordListImperativeProps, IRecordList>(
  (props, ref) => {
    const { t } = useTranslation();
    const {
      currentSessionId,
      onSelected,
      onRemove,
      compact = false,
      hideHeader = false,
      hideSearch = false,
      showBatchActions = !compact,
      searchText,
      title,
    } = props;
    const [historyList, setHistoryList] = useState<Conversation[]>([]);
    const [keyword, setKeyword] = useState("");
    const [pageToken, setPageToken] = useState("");
    const [checkedList, setCheckedList] = useState<string[]>([]);
    const [showBatchExport, setShowBatchExport] = useState(false);
    const [isHistoryLoading, setIsHistoryLoading] = useState(true);
    const scrollableTargetId = compact
      ? "sidebarConversationScrollableDiv"
      : "scrollableDiv";
    const deleteHistoryInFlightRef = useRef(false);
    const deleteHistoryLastInvokeRef = useRef(0);
    const { setThink } = useChatThinkStore();
    const { setNewMessage } = useChatNewMessageStore();
    const groupedHistoryList = useMemo(() => {
      const groups: Record<ConversationGroup, Conversation[]> = {
        today: [],
        recentWeek: [],
        earlier: [],
      };
      historyList.forEach((item) => {
        groups[getConversationGroup(item.update_time)].push(item);
      });
      return [
        {
          key: "today" as const,
          title: t("chat.conversationGroupToday"),
          items: groups.today,
        },
        {
          key: "recentWeek" as const,
          title: t("chat.conversationGroupRecentWeek"),
          items: groups.recentWeek,
        },
        {
          key: "earlier" as const,
          title: t("chat.conversationGroupEarlier"),
          items: groups.earlier,
        },
      ].filter((group) => group.items.length > 0);
    }, [historyList, t]);
    useImperativeHandle(ref, () => ({
      refresh: () => {
        getHistory({ isFirst: true });
      },
    }));

    useEffect(() => {
      if (
        !historyList?.some(
          (history) => history.conversation_id === currentSessionId,
        )
      ) {
        getHistory({ isFirst: true });
      }
    }, [currentSessionId]);

    useEffect(() => {
      if (searchText === undefined) {
        return;
      }
      const timer = window.setTimeout(() => {
        setKeyword(searchText);
        getHistory({ searchText, isFirst: true });
      }, SIDEBAR_SEARCH_DEBOUNCE_MS);
      return () => window.clearTimeout(timer);
    }, [searchText]);

    function getHistory(params?: {
      isMore?: boolean;
      isFirst?: boolean;
      searchText?: string;
    }) {
      const { isMore = false, isFirst = false, searchText } = params ?? {};
      setIsHistoryLoading(true);
      ChatServiceApi()
        .conversationServiceListConversations({
          keyword: searchText ?? keyword,
          pageToken: isFirst ? "" : pageToken,
          pageSize: 50,
        })
        .then((res) => {
          const conversations: Conversation[] = res?.data?.conversations ?? [];
          setHistoryList(
            isMore
              ? [...(historyList || []), ...(conversations || [])]
              : conversations,
          );
          setPageToken(res.data.next_page_token || "");
        })
        .finally(() => {
          setIsHistoryLoading(false);
        });
    }

    function deleteHistory(data: Conversation) {
      const now = Date.now();
      if (
        deleteHistoryInFlightRef.current ||
        now - deleteHistoryLastInvokeRef.current < 1000
      ) {
        return;
      }
      deleteHistoryInFlightRef.current = true;
      deleteHistoryLastInvokeRef.current = now;
      ChatServiceApi()
        .conversationServiceDeleteConversation({
          conversation: data.conversation_id || "",
        })
        .then(() => {
          message.success(t("chat.deleteConversationSuccess"));
          getHistory({ isFirst: true });
          document.getElementById(scrollableTargetId)?.scrollTo({ top: 0 });
        })
        .finally(() => {
          deleteHistoryInFlightRef.current = false;
        });
      onRemove(data);
    }

    function exportHistoryFn() {
      conversationsClient
        .apiCoreConversationExportPost({
          exportConversationsRequest: {
            conversation_ids: checkedList,
            file_types: [EXPORT_FILE_TYPE_XLSX],
          },
        })
        .then(async (res) => {
          const { uris = [] } = res.data;
          if (uris?.length) {
            const fileId = getExportFileId(uris[0]);
            if (!fileId) {
              message.error(t("chat.exportFileUrlInvalid"));
              return;
            }
            const downloadRes =
              await conversationsClient.apiCoreConversationExportFilesFileIdGet(
                { fileId },
                { responseType: "blob" },
              );
            downloadStream(
              downloadRes.data as Blob,
              getDownloadFileName(downloadRes.headers["content-disposition"]),
            );
          } else {
            message.warning(t("chat.noConversationToExport"));
          }
        })
        .finally(() => {
          setCheckedList([]);
        });
    }

    function renderItemText(params: { item: Conversation; selected: boolean }) {
      const { item, selected } = params;
      return (
        <div
          className={classnames("record", { selected })}
          key={item.conversation_id}
          onClick={(e) => {
            e.preventDefault();
            if (selected) {
              return;
            }
            onSelected(item);
            setThink(false);
            setNewMessage(false);
          }}
        >
          <Tooltip title={item.display_name}>
            <span className="title">{item.display_name}</span>
          </Tooltip>
          <span className="update-time">
            {dayjs(item.update_time).format("MM/DD")}
          </span>
          <CloseOutlined
            className="close"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              deleteHistory(item);
            }}
          />
        </div>
      );
    }

    function renderItem() {
      if (compact) {
        return (
          <div className="record-groups">
            {groupedHistoryList.map((group) => (
              <div className="record-group" key={group.key}>
                <div className="record-group-title">{group.title}</div>
                <Row>
                  {group.items.map((item) => {
                    const selected = item.conversation_id === currentSessionId;
                    return (
                      <Col span={24} key={item.conversation_id}>
                        {showBatchExport ? (
                          <Checkbox
                            className="export-checkbox-item"
                            value={item.conversation_id}
                          >
                            {renderItemText({ item, selected })}
                          </Checkbox>
                        ) : (
                          renderItemText({ item, selected })
                        )}
                      </Col>
                    );
                  })}
                </Row>
              </div>
            ))}
          </div>
        );
      }
      return (
        <Row>
          {historyList?.map((item) => {
            const selected = item.conversation_id === currentSessionId;
            return (
              <Col span={24} key={item.conversation_id}>
                {showBatchExport ? (
                  <Checkbox
                    className="export-checkbox-item"
                    value={item.conversation_id}
                  >
                    {renderItemText({ item, selected })}
                  </Checkbox>
                ) : (
                  renderItemText({ item, selected })
                )}
              </Col>
            );
          })}
        </Row>
      );
    }

    return (
      <div className={classnames("record-container", { compact })}>
        {!hideHeader && (
          <div className="record-header">
            {(!compact || showBatchActions) && (
              <div className="record-header-top">
                <div className="list-title">
                  {compact ? t("chat.chatHistory") : title || t("chat.chatHistory")}
                </div>
                {showBatchActions && (
                  <div className="record-toolbar-actions">
                    {showBatchExport ? (
                      <>
                        <Button
                          size="small"
                          type="link"
                          icon={<CloudDownloadOutlined />}
                          onClick={() => {
                            if (checkedList?.length) {
                              exportHistoryFn();
                            } else {
                              message.warning(t("chat.selectConversationToExport"));
                            }
                          }}
                        >
                          {t("chat.export")}
                        </Button>
                        <Button
                          size="small"
                          type="text"
                          onClick={() => setShowBatchExport(false)}
                        >
                          {t("common.cancel")}
                        </Button>
                      </>
                    ) : (
                      <Button
                        size="small"
                        type="link"
                        style={{ padding: 0 }}
                        onClick={() => setShowBatchExport(true)}
                      >
                        {t("chat.batch")}
                      </Button>
                    )}
                  </div>
                )}
              </div>
            )}
            {!hideSearch && (
              <div className="record-toolbar">
                <Search
                  className="record-toolbar-search"
                  placeholder={t("chat.searchConversation")}
                  allowClear
                  onSearch={(value: string) => {
                    getHistory({ searchText: value, isFirst: true });
                    setKeyword(value);
                  }}
                />
              </div>
            )}
          </div>
        )}
        {showBatchExport && (
          <div style={{ padding: "8px 0" }}>
            <Checkbox
              indeterminate={
                checkedList?.length > 0 &&
                checkedList.length < historyList?.length
              }
              checked={
                historyList?.length === checkedList?.length &&
                !!checkedList?.length
              }
              onChange={(e) =>
                setCheckedList(
                  e.target.checked
                    ? historyList?.map((it) => it?.conversation_id ?? "")
                    : [],
                )
              }
            >
              {t("chat.selectAll")}
            </Checkbox>
          </div>
        )}
        <div className="record-list" id={scrollableTargetId}>
          {!isHistoryLoading && !historyList?.length ? (
            <div className="record-empty" role="status">
              {t("chat.noConversations")}
            </div>
          ) : (
            <InfiniteScroll
              dataLength={historyList?.length || 0}
              next={() => getHistory({ isMore: true })}
              hasMore={!!pageToken}
              loader={<Spin />}
              scrollableTarget={scrollableTargetId}
            >
              {showBatchExport ? (
                <Checkbox.Group
                  className="export-checkbox-group"
                  onChange={(list) => setCheckedList(list)}
                  value={checkedList}
                >
                  {renderItem()}
                </Checkbox.Group>
              ) : (
                renderItem()
              )}
            </InfiniteScroll>
          )}
        </div>
      </div>
    );
  },
);

export default RecordList;
