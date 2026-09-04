import {
  CloudDownloadOutlined,
  DeleteOutlined,
  DownOutlined,
  FilterOutlined,
  FolderOutlined,
  FilePdfOutlined,
  LinkOutlined,
  MessageOutlined,
  MoreOutlined,
  PushpinFilled,
  PushpinOutlined,
  RightOutlined,
} from "@ant-design/icons";
import classnames from "classnames";
import {
  Button,
  Checkbox,
  Col,
  Dropdown,
  Input,
  message,
  Modal,
  Popover,
  Row,
  Spin,
  Tooltip,
} from "antd";
import type { MenuProps } from "antd";
import { Conversation } from "@/api/generated/chatbot-client";
import {
  Configuration as CoreConfiguration,
  ConversationsApiFactory,
  DefaultApiFactory,
} from "@/api/generated/core-client";
import {
  useEffect,
  useMemo,
  useRef,
  forwardRef,
  useImperativeHandle,
  Fragment,
} from "react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import InfiniteScroll from "react-infinite-scroll-component";
import { axiosInstance, BASE_URL } from "@/components/request";
import { useChatThinkStore } from "@/modules/chat/store/chatThink";
import { useChatNewMessageStore } from "@/modules/chat/store/chatNewMessage";

import dayjs from "dayjs";

import {
  ChatServiceApi,
  ConversationSettingsApi,
  type ChatExecutorDescriptor,
} from "@/modules/chat/utils/request";
import {
  bumpConversationToTop,
} from "@/modules/chat/utils/conversationActivity";
import {
  CHAT_CONVERSATION_ACTIVITY_EVENT,
  CHAT_CONVERSATION_FILTER_EVENT,
  CHAT_CONVERSATION_FILTER_KEY,
  type ChatConversationActivityDetail,
  type ChatConversationFilter,
} from "@/modules/chat/constants/chat";
import "./index.scss";
import { downloadStream } from "@/modules/chat/utils/download";
import ArchiveConversationModal from "../ArchiveConversationModal";
import { unarchiveConversation } from "@/modules/settings/recoveryApi";
import { RECOVERY_ARCHIVE_PATH } from "@/modules/settings/recoveryRoute";
import {
  CONVERSATION_RELATION_FORK,
  getConversationRelation,
  isChildConversation,
  type ConversationWithRelation,
} from "@/modules/chat/utils/conversationRelation";

const EXPORT_FILE_TYPE_XLSX = "EXPORT_FILE_TYPE_XLSX";
const SIDEBAR_SEARCH_DEBOUNCE_MS = 300;
const conversationsClient = ConversationsApiFactory(
  new CoreConfiguration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);
const defaultCoreClient = DefaultApiFactory(
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

type SidebarConversation = ConversationWithRelation & {
  pinned_at?: string | null;
  is_pinned?: boolean;
  source_type?: string;
  source_display_name?: string;
};

type ConversationGroup = "pinned" | "today" | "recentWeek" | "earlier";

type SidebarConversationNode = {
  conversation: SidebarConversation;
  children: SidebarConversation[];
  isPlaceholderParent?: boolean;
};

function isConversationPinned(conversation: SidebarConversation) {
  return conversation.is_pinned === true || Boolean(conversation.pinned_at);
}

function conversationTime(value?: string | null) {
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.valueOf() : 0;
}

function sortConversationHistory(conversations: SidebarConversation[]) {
  return [...conversations].sort((left, right) => {
    const leftPinned = isConversationPinned(left);
    const rightPinned = isConversationPinned(right);
    if (leftPinned !== rightPinned) {
      return leftPinned ? -1 : 1;
    }
    if (leftPinned) {
      return (
        conversationTime(right.pinned_at) - conversationTime(left.pinned_at)
      );
    }
    return (
      conversationTime(right.update_time) - conversationTime(left.update_time)
    );
  });
}

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
    const navigate = useNavigate();
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
    const [historyList, setHistoryList] = useState<SidebarConversation[]>([]);
    const [keyword, setKeyword] = useState("");
    const [pageToken, setPageToken] = useState("");
    const [checkedList, setCheckedList] = useState<string[]>([]);
    const [showBatchExport, setShowBatchExport] = useState(false);
    const [isHistoryLoading, setIsHistoryLoading] = useState(true);
    const [archiveItem, setArchiveItem] = useState<Conversation | null>(null);
    const [pinningConversationId, setPinningConversationId] = useState("");
    const [expandedParentIds, setExpandedParentIds] = useState<Set<string>>(
      () => new Set(),
    );
    // convTypeFilter: which conversation types to show. Default = normal only (no task convs).
    // Values: 'normal' = non-task, 'task' = task. Multiple values allowed.
    const [convTypeFilter, setConvTypeFilter] = useState<string[]>(() => {
      try {
        const stored = sessionStorage.getItem(CHAT_CONVERSATION_FILTER_KEY);
        if (stored?.startsWith("[")) {
          const parsed = JSON.parse(stored);
          if (Array.isArray(parsed) && parsed.length > 0) return parsed;
        }
        return stored === "task" ? ["task"] : ["normal"];
      } catch {
        return ["normal"];
      }
    });
    const [connectedAgents, setConnectedAgents] = useState<ChatExecutorDescriptor[]>([]);
    const [filterPopoverOpen, setFilterPopoverOpen] = useState(false);
    const scrollableTargetId = compact
      ? "sidebarConversationScrollableDiv"
      : "scrollableDiv";
    const deleteHistoryInFlightRef = useRef(false);
    const deleteHistoryLastInvokeRef = useRef(0);
    const batchDeleteInFlightRef = useRef(false);
    const pinningConversationRef = useRef(false);
    const { setThink } = useChatThinkStore();
    const { setNewMessage } = useChatNewMessageStore();

    useEffect(() => {
      let active = true;
      ConversationSettingsApi().listChatExecutors().then((response) => {
        if (!active) return;
        setConnectedAgents(
          response.data.data.executors.filter(
            (executor) => executor.kind === "external" && executor.connected,
          ),
        );
      }).catch(() => {
        // The regular/task filters remain usable if host discovery is unavailable.
      });
      return () => { active = false; };
    }, []);

    useEffect(() => {
      const handleFilterChange = (event: Event) => {
        const filter = (
          event as CustomEvent<{ filter?: ChatConversationFilter }>
        ).detail?.filter;
        if (filter !== "normal" && filter !== "task") return;
        const next = [filter];
        setConvTypeFilter(next);
        setFilterPopoverOpen(false);
        getHistory({ isFirst: true, filterOverride: next, searchText: keyword });
      };
      window.addEventListener(
        CHAT_CONVERSATION_FILTER_EVENT,
        handleFilterChange,
      );
      return () =>
        window.removeEventListener(
          CHAT_CONVERSATION_FILTER_EVENT,
          handleFilterChange,
        );
    }, [keyword]);

    const conversationTree = useMemo(() => {
      const conversationsById = new Map(
        historyList.map((item) => [item.conversation_id || "", item]),
      );
      const childrenByParent = new Map<string, SidebarConversation[]>();
      const nestedChildIds = new Set<string>();
      historyList.forEach((item) => {
        const relation = getConversationRelation(item);
        const itemId = item.conversation_id || "";
        if (
          !relation ||
          !itemId ||
          relation.parentConversationId === itemId
        ) {
          return;
        }
        const children = childrenByParent.get(relation.parentConversationId) || [];
        children.push(item);
        childrenByParent.set(relation.parentConversationId, children);
        nestedChildIds.add(itemId);
      });
      const nodes = historyList
        .filter((item) => !nestedChildIds.has(item.conversation_id || ""))
        .map((conversation) => ({
          conversation,
          children: childrenByParent.get(conversation.conversation_id || "") || [],
        }));
      childrenByParent.forEach((children, parentConversationId) => {
        if (conversationsById.has(parentConversationId) || children.length === 0) {
          return;
        }
        const firstChild = children[0];
        const relation = getConversationRelation(firstChild);
        nodes.push({
          conversation: {
            ...firstChild,
            conversation_id: parentConversationId,
            display_name: relation?.parentDisplayName || parentConversationId,
            parent_conversation_id: undefined,
            parent_display_name: undefined,
            relation_type: undefined,
            is_pinned: false,
            pinned_at: null,
          },
          children,
          isPlaceholderParent: true,
        });
      });
      return nodes;
    }, [historyList]);

    useEffect(() => {
      if (!keyword.trim()) {
        return;
      }
      const placeholderParentIds = conversationTree
        .filter((node) => node.isPlaceholderParent)
        .map((node) => node.conversation.conversation_id || "")
        .filter(Boolean);
      if (placeholderParentIds.length === 0) {
        return;
      }
      setExpandedParentIds((previous) => {
        const next = new Set(previous);
        let changed = false;
        placeholderParentIds.forEach((id) => {
          if (!next.has(id)) {
            next.add(id);
            changed = true;
          }
        });
        return changed ? next : previous;
      });
    }, [conversationTree, keyword]);

    const groupedHistoryList = useMemo(() => {
      const groups: Record<ConversationGroup, SidebarConversationNode[]> = {
        pinned: [],
        today: [],
        recentWeek: [],
        earlier: [],
      };
      conversationTree.forEach((node) => {
        if (isConversationPinned(node.conversation)) {
          groups.pinned.push(node);
          return;
        }
        groups[getConversationGroup(node.conversation.update_time)].push(node);
      });
      return [
        {
          key: "pinned" as const,
          title: t("chat.conversationGroupPinned"),
          items: groups.pinned,
        },
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
    }, [conversationTree, t]);

    const batchSelectableConversationIds = useMemo(
      () =>
        historyList.flatMap((item) =>
          !isChildConversation(item) && item.conversation_id
            ? [item.conversation_id]
            : [],
        ),
      [historyList],
    );

    useEffect(() => {
      const selected = historyList.find(
        (item) => item.conversation_id === currentSessionId,
      );
      const relation = getConversationRelation(selected);
      if (!relation || !historyList.some(
        (item) => item.conversation_id === relation.parentConversationId,
      )) {
        return;
      }
      setExpandedParentIds((previous) => {
        if (previous.has(relation.parentConversationId)) {
          return previous;
        }
        const next = new Set(previous);
        next.add(relation.parentConversationId);
        return next;
      });
    }, [currentSessionId, historyList]);
    useImperativeHandle(ref, () => ({
      refresh: () => {
        getHistory({ isFirst: true, searchText: keyword });
      },
    }));

    useEffect(() => {
      if (
        !historyList?.some(
          (history) => history.conversation_id === currentSessionId,
        )
      ) {
        getHistory({ isFirst: true, searchText: keyword });
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
          }) as SidebarConversation[];
          window.requestAnimationFrame(() => {
            document.getElementById(scrollableTargetId)?.scrollTo({
              top: 0,
              behavior: "smooth",
            });
          });
          return sortConversationHistory(next);
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
      const selectedAgents = activeFilter.filter((value) => value.startsWith('agent:'))
        .map((value) => value.slice('agent:'.length));
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
          { params: {
            ...(isTaskConvParam !== undefined ? { is_task_conv: isTaskConvParam } : {}),
            assistants: [
              ...(hasNormal || hasTask ? ['lazymind'] : []),
              ...selectedAgents,
            ].join(','),
          } },
        )
        .then((res) => {
          const conversations: SidebarConversation[] =
            res?.data?.conversations ?? [];
          setHistoryList(
            sortConversationHistory(
              isMore
                ? [...(historyList || []), ...(conversations || [])]
                : conversations,
            ),
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
      return ChatServiceApi()
        .conversationServiceDeleteConversation({
          conversation: data.conversation_id || "",
        })
        .then(() => {
          message.success(t("chat.deleteConversationSuccess"));
          getHistory({ isFirst: true });
          document.getElementById(scrollableTargetId)?.scrollTo({ top: 0 });
          onRemove(data);
        })
        .finally(() => {
          deleteHistoryInFlightRef.current = false;
        });
    }

    function setConversationPinned(
      conversation: SidebarConversation,
      pinned: boolean,
    ) {
      if (pinningConversationRef.current) {
        return;
      }
      const conversationId = conversation.conversation_id || "";
      if (!conversationId) {
        return;
      }
      pinningConversationRef.current = true;
      setPinningConversationId(conversationId);
      return ChatServiceApi()
        .conversationServiceSetPinned(conversationId, pinned)
        .then((res) => {
          const pinnedAt = pinned
            ? res.data?.pinned_at || new Date().toISOString()
            : null;
          setHistoryList((previous) =>
            sortConversationHistory(
              previous.map((item) =>
                item.conversation_id === conversationId
                  ? { ...item, is_pinned: pinned, pinned_at: pinnedAt }
                  : item,
              ),
            ),
          );
          message.success(
            t(
              pinned
                ? "chat.pinConversationSuccess"
                : "chat.unpinConversationSuccess",
            ),
          );
          document.getElementById(scrollableTargetId)?.scrollTo({ top: 0 });
        })
        .catch(() => {
          message.error(t("chat.pinConversationFailed"));
        })
        .finally(() => {
          pinningConversationRef.current = false;
          setPinningConversationId("");
        });
    }

    function confirmDeleteHistory(data: Conversation) {
      Modal.confirm({
        title: t("settingsPage.recovery.moveToTrashTitle", { name: data.display_name }),
        content: t("settingsPage.recovery.moveToTrashDescription"),
        okText: t("settingsPage.recovery.moveToTrash"),
        cancelText: t("common.cancel"),
        okButtonProps: { danger: true },
        onOk: () => deleteHistory(data),
      });
    }

    function showArchivedFeedback(data: Conversation) {
      const conversationId = data.conversation_id || "";
      const messageKey = `archived:${conversationId}`;
      message.open({
        key: messageKey,
        type: "success",
        duration: 8,
        content: (
          <span className="archive-feedback">
            {t("settingsPage.recovery.archivedSuccess")}
            <Button type="link" size="small" onClick={() => {
              void unarchiveConversation(conversationId)
                .then(() => {
                  message.destroy(messageKey);
                  message.success(t("settingsPage.recovery.unarchived"));
                  getHistory({ isFirst: true });
                })
                .catch(() => message.error(t("settingsPage.recovery.operationFailed")));
            }}>{t("settingsPage.recovery.undo")}</Button>
            <Button type="link" size="small" onClick={() => navigate(RECOVERY_ARCHIVE_PATH)}>{t("settingsPage.recovery.viewArchived")}</Button>
          </span>
        ),
      });
    }

    function batchDeleteHistory() {
      if (!checkedList.length) {
        message.warning(t("chat.selectConversationToDelete"));
        return;
      }
      if (batchDeleteInFlightRef.current) {
        return;
      }
      Modal.confirm({
        title: t("chat.batchDeleteConversationTitle", {
          count: checkedList.length,
        }),
        content: t("chat.batchDeleteConversationContent"),
        okText: t("common.delete"),
        cancelText: t("common.cancel"),
        okButtonProps: { danger: true },
        onOk: () => {
          batchDeleteInFlightRef.current = true;
          return defaultCoreClient
            .apiCoreConversationsBatchDeletePost({
              conversationBatchDeleteRequest: {
                conversation_ids: checkedList,
              },
            })
            .then((res) => {
              const deletedCount = res.data?.deleted_count ?? checkedList.length;
              message.success(
                t("chat.batchDeleteConversationSuccess", { count: deletedCount }),
              );
              if (checkedList.includes(currentSessionId)) {
                const removed = historyList.find(
                  (item) => item.conversation_id === currentSessionId,
                );
                if (removed) {
                  onRemove(removed);
                }
              }
              setCheckedList([]);
              setShowBatchExport(false);
              getHistory({ isFirst: true });
              document.getElementById(scrollableTargetId)?.scrollTo({ top: 0 });
            })
            .finally(() => {
              batchDeleteInFlightRef.current = false;
            });
        },
      });
    }

    function exitBatchMode() {
      setShowBatchExport(false);
      setCheckedList([]);
    }

    const batchActionMenuItems: MenuProps["items"] = [
      {
        key: "export",
        label: t("chat.export"),
        icon: <CloudDownloadOutlined />,
        disabled: !checkedList.length,
        onClick: () => {
          if (checkedList?.length) {
            exportHistoryFn();
          } else {
            message.warning(t("chat.selectConversationToExport"));
          }
        },
      },
      {
        key: "delete",
        label: t("common.delete"),
        icon: <DeleteOutlined />,
        danger: true,
        disabled: !checkedList.length,
        onClick: () => batchDeleteHistory(),
      },
    ];

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

    function renderItemText(params: {
      item: SidebarConversation;
      selected: boolean;
      childCount?: number;
      isChild?: boolean;
      hideActions?: boolean;
    }) {
      const {
        item,
        selected,
        childCount = 0,
        isChild = false,
        hideActions = false,
      } = params;
      const source = item;
      const pinned = isConversationPinned(item);
      const conversationId = item.conversation_id || "";
      const childrenExpanded = expandedParentIds.has(conversationId);
      const relation = getConversationRelation(item);
      const relationIsFork =
        relation?.relationType === CONVERSATION_RELATION_FORK;
      const conversationTitle = item.display_name || conversationId;
      const relationDescription = relation
        ? t(
            relationIsFork
              ? "chat.conversationForkedFrom"
              : "chat.conversationSourceFrom",
            { parent: relation.parentDisplayName },
          )
        : t("chat.conversationMainLabel");
      const ownershipActionItems: MenuProps["items"] = relation
        ? []
        : [
            {
              key: pinned ? "unpin" : "pin",
              icon: pinned ? <PushpinFilled /> : <PushpinOutlined />,
              label: t(
                pinned ? "chat.unpinConversation" : "chat.pinConversation",
              ),
              disabled: Boolean(pinningConversationId),
              onClick: () => setConversationPinned(item, !pinned),
            },
            {
              key: "archive",
              icon: <FolderOutlined />,
              label: t("settingsPage.recovery.archiveAction"),
              onClick: () => setArchiveItem(item),
            },
          ];
      const activateConversation = () => {
        if (showBatchExport || selected) return;
        onSelected(item);
        setThink(false);
        setNewMessage(false);
      };
      return (
        <div
          className={classnames("record", {
            selected,
            "record-child": isChild,
          })}
          key={item.conversation_id}
          role={showBatchExport ? undefined : "button"}
          tabIndex={showBatchExport ? undefined : 0}
          aria-current={selected ? "page" : undefined}
          onClick={(e) => {
            e.preventDefault();
            activateConversation();
          }}
          onKeyDown={(event) => {
            if (
              event.target !== event.currentTarget ||
              (event.key !== "Enter" && event.key !== " ")
            ) {
              return;
            }
            event.preventDefault();
            activateConversation();
          }}
        >
          {childCount > 0 ? (
            <Tooltip
              title={t(
                childrenExpanded
                  ? "chat.collapseChildConversations"
                  : "chat.expandChildConversations",
                { count: childCount },
              )}
            >
              <button
                type="button"
                className="record-children-toggle"
                aria-expanded={childrenExpanded}
                aria-controls={`record-children-${conversationId}`}
                aria-label={t(
                  childrenExpanded
                    ? "chat.collapseChildConversations"
                    : "chat.expandChildConversations",
                  { count: childCount },
                )}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  setExpandedParentIds((previous) => {
                    const next = new Set(previous);
                    if (next.has(conversationId)) {
                      next.delete(conversationId);
                    } else {
                      next.add(conversationId);
                    }
                    return next;
                  });
                }}
              >
                {childrenExpanded ? <DownOutlined /> : <RightOutlined />}
                <span>{childCount}</span>
              </button>
            </Tooltip>
          ) : null}
          <Popover
            placement="rightTop"
            trigger="hover"
            arrow={false}
            mouseEnterDelay={0.2}
            mouseLeaveDelay={0.08}
            destroyOnHidden
            classNames={{ root: "record-preview-popover" }}
            content={
              <div className="record-preview-card">
                <strong className="record-preview-title">
                  {conversationTitle}
                </strong>
                <div className="record-preview-meta">
                  {relation ? (
                    <LinkOutlined aria-hidden="true" />
                  ) : (
                    <MessageOutlined aria-hidden="true" />
                  )}
                  <span>{relationDescription}</span>
                </div>
              </div>
            }
          >
            <span className="title">{conversationTitle}</span>
          </Popover>
          {source.source_type === "pdf_preview" ? (
            <Tooltip title={source.source_display_name || t("knowledge.pdfChatSavedSource")}>
              <FilePdfOutlined className="record-source-icon" aria-label={t("knowledge.pdfChatSavedSource")} />
            </Tooltip>
          ) : null}
          <span className="update-time">
            {dayjs(item.update_time).format("MM/DD")}
          </span>
          {!showBatchExport && !hideActions ? (
            <Dropdown
              trigger={["click"]}
              menu={{
                items: [
                  ...ownershipActionItems,
                  {
                    key: "trash",
                    icon: <DeleteOutlined />,
                    danger: true,
                    label: t("settingsPage.recovery.moveToTrash"),
                    onClick: () => confirmDeleteHistory(item),
                  },
                ],
              }}
            >
              <Button
                type="text"
                size="small"
                className="close"
                icon={<MoreOutlined />}
                aria-label={t("settingsPage.recovery.moreActions")}
                onClick={(event: React.MouseEvent<HTMLElement>) => {
                  event.preventDefault();
                  event.stopPropagation();
                }}
              />
            </Dropdown>
          ) : null}
        </div>
      );
    }

    function renderItem() {
      const renderNode = (node: SidebarConversationNode) => {
        const item = node.conversation;
        const conversationId = item.conversation_id || "";
        const selected = conversationId === currentSessionId;
        const childrenExpanded = expandedParentIds.has(conversationId);
        const record = showBatchExport ? (
          <Checkbox
            className="export-checkbox-item"
            value={item.conversation_id}
            disabled={isChildConversation(item) || node.isPlaceholderParent}
          >
            {renderItemText({
              item,
              selected,
              childCount: node.children.length,
              hideActions: node.isPlaceholderParent,
            })}
          </Checkbox>
        ) : (
          renderItemText({
            item,
            selected,
            childCount: node.children.length,
            hideActions: node.isPlaceholderParent,
          })
        );
        return (
          <Fragment key={conversationId}>
            <Col span={24}>{record}</Col>
            {childrenExpanded && node.children.length > 0 ? (
              <Col span={24}>
                <div
                  id={`record-children-${conversationId}`}
                  className="record-children"
                  role="group"
                  aria-label={t("chat.childConversationsLabel", {
                    parent: item.display_name || conversationId,
                  })}
                >
                  {node.children.map((child) => {
                    const childSelected =
                      child.conversation_id === currentSessionId;
                    return showBatchExport ? (
                      <Checkbox
                        key={child.conversation_id}
                        className="export-checkbox-item record-child-checkbox"
                        value={child.conversation_id}
                        disabled
                      >
                        {renderItemText({
                          item: child,
                          selected: childSelected,
                          isChild: true,
                        })}
                      </Checkbox>
                    ) : (
                      renderItemText({
                        item: child,
                        selected: childSelected,
                        isChild: true,
                      })
                    );
                  })}
                </div>
              </Col>
            ) : null}
          </Fragment>
        );
      };

      if (compact) {
        return (
          <div className="record-groups">
            {groupedHistoryList.map((group) => (
              <div className="record-group" key={group.key}>
                <div className="record-group-title">{group.title}</div>
                <Row>
                  {group.items.map((node) => renderNode(node))}
                </Row>
              </div>
            ))}
          </div>
        );
      }
      return (
        <Row>
          {conversationTree.map((node) => renderNode(node))}
        </Row>
      );
    }

    return (
      <div className={classnames("record-container", { compact })}>
        <ArchiveConversationModal
          open={Boolean(archiveItem)}
          conversationId={archiveItem?.conversation_id}
          title={archiveItem?.display_name}
          itemKind={(archiveItem as (Conversation & { is_task_conv?: boolean }) | null)?.is_task_conv ? "task" : "dialog"}
          onCancel={() => setArchiveItem(null)}
          onArchived={() => {
            const archived = archiveItem;
            setArchiveItem(null);
            if (!archived) return;
            getHistory({ isFirst: true });
            onRemove(archived);
            showArchivedFeedback(archived);
          }}
        />
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
                        <Dropdown
                          menu={{ items: batchActionMenuItems }}
                          trigger={["click"]}
                          placement="bottomRight"
                        >
                          <Button size="small" type="link" className="record-batch-actions-trigger">
                            {t("common.actions")}
                            <DownOutlined className="record-batch-actions-caret" />
                          </Button>
                        </Dropdown>
                        <Button size="small" type="text" onClick={exitBatchMode}>
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
                              <div style={{ marginBottom: 6, fontWeight: 500, fontSize: 12, color: '#666' }}>{t("chat.filterConversationType")}</div>
                              <Checkbox.Group
                                value={convTypeFilter}
                                onChange={(vals) => {
                                  const next = vals as string[];
                                  if (next.length === 0) {
                                    message.warning(t("chat.selectAtLeastOneConvType"));
                                    return;
                                  }
                                  setConvTypeFilter(next);
                                  try {
                                    sessionStorage.setItem(
                                      CHAT_CONVERSATION_FILTER_KEY,
                                      JSON.stringify(next),
                                    );
                                  } catch {
                                    // Ignore storage errors.
                                  }
                                  getHistory({ isFirst: true, filterOverride: next, searchText: keyword });
                                }}
                                style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
                              >
                                <Checkbox value="normal">{t("chat.normalConversation")}</Checkbox>
                                <Checkbox value="task">{t("chat.taskConversation")}</Checkbox>
                                {connectedAgents.map((agent) => (
                                  <Checkbox key={agent.id} value={`agent:${agent.id}`}>
                                    {agent.display_name}
                                  </Checkbox>
                                ))}
                              </Checkbox.Group>
                            </div>
                          }
                        >
                          <Button
                            size="small"
                            type="text"
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
                checkedList.length < batchSelectableConversationIds.length
              }
              checked={
                batchSelectableConversationIds.length === checkedList?.length &&
                !!checkedList?.length
              }
              onChange={(e) =>
                setCheckedList(
                  e.target.checked ? batchSelectableConversationIds : [],
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
                <Checkbox.Group<string>
                  className="export-checkbox-group"
                  onChange={(list: string[]) =>
                    setCheckedList(
                      list.filter((id: string) =>
                        batchSelectableConversationIds.includes(String(id)),
                      ),
                    )
                  }
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
