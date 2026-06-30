import { CloseOutlined, CloudDownloadOutlined, FilterOutlined, PlusCircleOutlined } from "@ant-design/icons";
import classnames from "classnames";
import {
  Button,
  Checkbox,
  Col,
  Input,
  message,
  Popover,
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
import { addTask } from "@/modules/taskCenter/api";

import dayjs from "dayjs";

import { ChatServiceApi } from "@/modules/chat/utils/request";
import {
  bumpConversationToTop,
} from "@/modules/chat/utils/conversationActivity";
import {
  CHAT_CONVERSATION_ACTIVITY_EVENT,
  type ChatConversationActivityDetail,
} from "@/modules/chat/constants/chat";
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
    // convTypeFilter: which conversation types to show. Default = normal only (no task convs).
    // Values: 'normal' = non-task, 'task' = task. Multiple values allowed.
    const [convTypeFilter, setConvTypeFilter] = useState<string[]>(['normal']);
    const [filterPopoverOpen, setFilterPopoverOpen] = useState(false);
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
      const handleConversationActivity = (event: Event) => {
        const detail =
          (event as CustomEvent<ChatConversationActivityDetail>).detail || {};
        const conversationId = detail.conversationId?.trim();
        if (!conversationId) {
          return;
        }

        setHistoryList((prev) => {
          const exists = prev.some(
            (item) => item.conversation_id === conversationId,
          );
          if (
            !exists &&
            !detail.displayName &&
            !convTypeFilter.includes("normal")
          ) {
            return prev;
          }

          const next = bumpConversationToTop(prev, conversationId, {
            displayName: detail.displayName,
          });
          window.requestAnimationFrame(() => {
            document.getElementById(scrollableTargetId)?.scrollTo({
              top: 0,
              behavior: "smooth",
            });
          });
          return next;
        });
      };

      window.addEventListener(
        CHAT_CONVERSATION_ACTIVITY_EVENT,
        handleConversationActivity,
      );
      return () => {
        window.removeEventListener(
          CHAT_CONVERSATION_ACTIVITY_EVENT,
          handleConversationActivity,
        );
      };
    }, [convTypeFilter, scrollableTargetId]);

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
      filterOverride?: string[];
    }) {
      const { isMore = false, isFirst = false, searchText, filterOverride } = params ?? {};
      const activeFilter = filterOverride ?? convTypeFilter;
      setIsHistoryLoading(true);

      // Determine is_task_conv query param based on active filter selection.
      // 'normal' only → is_task_conv=false, 'task' only → is_task_conv=true, both → no filter.
      const hasNormal = activeFilter.includes('normal');
      const hasTask = activeFilter.includes('task');
      let isTaskConvParam: string | undefined;
      if (hasNormal && !hasTask) {
        isTaskConvParam = 'false';
      } else if (hasTask && !hasNormal) {
        isTaskConvParam = 'true';
      }

      ChatServiceApi()
        .conversationServiceListConversations(
          {
            keyword: searchText ?? keyword,
            pageToken: isFirst ? "" : pageToken,
            pageSize: 50,
          },
          isTaskConvParam !== undefined
            ? { params: { is_task_conv: isTaskConvParam } }
            : undefined,
        )
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
          <Tooltip title="加入任务中心">
            <PlusCircleOutlined
              className="add-to-task"
              style={{ marginLeft: 4, fontSize: 12, color: '#888', cursor: 'pointer' }}
              onClick={async (e) => {
                e.preventDefault();
                e.stopPropagation();
                try {
                  await addTask(item.conversation_id ?? '', item.display_name ?? '');
                  message.success('已加入任务中心');
                } catch {
                  message.error('加入任务中心失败');
                }
              }}
            />
          </Tooltip>
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
                      <>
                        <Popover
                          open={filterPopoverOpen}
                          onOpenChange={setFilterPopoverOpen}
                          trigger="click"
                          placement="bottomRight"
                          content={
                            <div style={{ minWidth: 140 }}>
                              <div style={{ marginBottom: 6, fontWeight: 500, fontSize: 12, color: '#666' }}>筛选对话类型</div>
                              <Checkbox.Group
                                value={convTypeFilter}
                                onChange={(vals) => {
                                  const next = vals as string[];
                                  setConvTypeFilter(next);
                                  getHistory({ isFirst: true, filterOverride: next });
                                  setFilterPopoverOpen(false);
                                }}
                                style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
                              >
                                <Checkbox value="normal">普通对话</Checkbox>
                                <Checkbox value="task">Task 对话</Checkbox>
                              </Checkbox.Group>
                            </div>
                          }
                        >
                          <Button
                            size="small"
                            type={convTypeFilter.length !== 1 || !convTypeFilter.includes('normal') ? 'primary' : 'link'}
                            icon={<FilterOutlined />}
                            style={{ padding: '0 4px' }}
                          />
                        </Popover>
                        <Button
                          size="small"
                          type="link"
                          style={{ padding: 0 }}
                          onClick={() => setShowBatchExport(true)}
                        >
                          {t("chat.batch")}
                        </Button>
                      </>
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
          <div className="record-batch-select-row">
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
              {checkedList.length > 0 && (
                <span className="record-selected-count">{checkedList.length}</span>
              )}
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
