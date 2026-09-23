import { getLocalizedErrorMessage } from "@/components/request";
import {
  DndContext, PointerSensor, KeyboardSensor, closestCenter, pointerWithin, useSensor, useSensors,
  type DragEndEvent, type DragStartEvent, type DragOverEvent, type CollisionDetection,
} from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, verticalListSortingStrategy } from "@dnd-kit/sortable";
import SortableConversationRow from "./SortableConversationRow";
import ConversationRunningIndicator from "./ConversationRunningIndicator";
import { useConversationRunningStore } from "@/modules/chat/store/conversationRunning";
import { applyConversationOrder, isConversationPinned, sortConversationHistory, type SidebarConversation } from "./conversationHistory";
import {
  CloudDownloadOutlined,
  DeleteOutlined,
  DownOutlined,
  FilterOutlined,
  FolderOutlined,
  InboxOutlined,
  FilePdfOutlined,
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
  type ConversationGroupMember,
} from "@/api/generated/core-client";
import {
  useEffect,
  useMemo,
  useRef,
  useId,
  forwardRef,
  useImperativeHandle,
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
  readChatConversationFilters,
  readKnownConversationSources,
  rememberConversationSources,
  selectChatConversationSources,
  type ChatConversationActivityDetail,
  type ChatConversationFilters,
} from "@/modules/chat/constants/chat";
import "./index.scss";
import { downloadStream } from "@/modules/chat/utils/download";
import ArchiveConversationModal from "../ArchiveConversationModal";
import { unarchiveConversation } from "@/modules/settings/recoveryApi";
import {
  CONVERSATION_GROUPS_CHANGED_EVENT,
  emitConversationGroupsChanged,
  assignConversation,
} from "@/modules/chat/conversationOrganizer/api";
import { CONVERSATION_DRAG, readConversationDrag, startConversationDrag } from "@/modules/chat/conversationOrganizer/drag";
import { removeConversation } from "@/modules/chat/conversationOrganizer/api";
import { conversationGroupSubmenu } from "@/modules/chat/conversationOrganizer/ConversationGroupPicker";
import ConversationTitleEditor from "../ConversationTitleEditor";
import ConversationPreview from "../ConversationPreview";
import { CONVERSATION_TITLE_CHANGED_EVENT, type ConversationTitleChangedDetail } from "../../constants/chat";
import ConversationMembershipModal from "@/modules/chat/conversationOrganizer/ConversationMembershipModal";
import ConversationGroups from "@/modules/chat/conversationOrganizer/ConversationGroups";
import type { GroupBatchSelection } from "@/modules/chat/conversationOrganizer/SidebarGroups";
import { RECOVERY_ARCHIVE_PATH } from "@/modules/settings/recoveryRoute";
import {
  CONVERSATION_RELATION_FORK,
  getConversationRelation,
  isChildConversation,
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
  groupSection?: (batchSelection?: GroupBatchSelection) => React.ReactNode;
}

export interface RecordListImperativeProps {
  refresh: () => void;
}

const { Search } = Input;

type ConversationGroup = "pinned" | "today" | "yesterday" | "recentWeek" | "earlier";

type SidebarConversationNode = {
  conversation: SidebarConversation;
  children: SidebarConversation[];
  isPlaceholderParent?: boolean;
};

function getConversationGroup(updateTime?: string): Exclude<ConversationGroup, "pinned"> {
  const parsedTime = dayjs(updateTime);
  if (!parsedTime.isValid()) {
    return "earlier";
  }
  const todayStart = dayjs().startOf("day");
  if (parsedTime.isSame(todayStart, "day")) {
    return "today";
  }
  if (parsedTime.isSame(todayStart.subtract(1, "day"), "day")) {
    return "yesterday";
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
    const [modal, modalContextHolder] = Modal.useModal();
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
      groupSection,
    } = props;
    const [historyList, setHistoryList] = useState<SidebarConversation[]>([]);
    const [renamingId, setRenamingId] = useState<string | null>(null);
    const [movingConversation, setMovingConversation] = useState<SidebarConversation | null>(null);
    const statusWatcherId = useId();
    useEffect(() => {
      useConversationRunningStore.getState().watch(statusWatcherId, historyList.flatMap((item) => [
        item.conversation_id || "", getConversationRelation(item)?.parentConversationId || "",
      ]));
    }, [historyList, statusWatcherId]);
    useEffect(() => () => useConversationRunningStore.getState().unwatch(statusWatcherId), [statusWatcherId]);
    const [keyword, setKeyword] = useState("");
    const [pageToken, setPageToken] = useState("");
    const [historyRevision, setHistoryRevision] = useState(0);
    const [checkedList, setCheckedList] = useState<string[]>([]);
    const [batchGroupMembers, setBatchGroupMembers] = useState<ConversationGroupMember[]>([]);
    const [showBatchExport, setShowBatchExport] = useState(false);
    const [isHistoryLoading, setIsHistoryLoading] = useState(true);
    const [batchArchiveIds, setBatchArchiveIds] = useState<string[] | null>(null);
    const [archiveItem, setArchiveItem] = useState<Conversation | null>(null);
    const [pinningConversationId, setPinningConversationId] = useState("");
    useEffect(() => {
      const isLocked = (id?: string) => historyList.some((item) => item.conversation_id === id && item.organizing_run_id);
      if (archiveItem && isLocked(archiveItem.conversation_id)) {
        setArchiveItem(null);
        message.warning(t("conversationOrganizer.deleteLocked"));
      }
      if (movingConversation && isLocked(movingConversation.conversation_id)) {
        setMovingConversation(null);
        message.warning(t("conversationOrganizer.deleteLocked"));
      }
    }, [historyList, archiveItem, movingConversation, t]);

    const [expandedParentIds, setExpandedParentIds] = useState<Set<string>>(
      () => new Set(),
    );
    const [conversationFilters, setConversationFilters] = useState(readChatConversationFilters);
    const conversationFiltersRef = useRef(conversationFilters);
    conversationFiltersRef.current = conversationFilters;
    const [externalAgents, setExternalAgents] = useState<ChatExecutorDescriptor[]>([]);
    const [knownSources, setKnownSources] = useState(readKnownConversationSources);
    const visibleSources = [...new Set([
      "lazymind",
      ...knownSources,
      ...(conversationFilters.sources ?? []),
    ])];
    const [filterPopoverOpen, setFilterPopoverOpen] = useState(false);
    const scrollableTargetId = compact
      ? "sidebarConversationScrollableDiv"
      : "scrollableDiv";
    const deleteHistoryInFlightRef = useRef(false);
    const batchDeleteInFlightRef = useRef(false);
    const pinningConversationRef = useRef(false);
    const historyRequestRef = useRef(0);
    const historyRefreshRequiredRef = useRef(false);
    const [reorderingConversationId, setReorderingConversationId] = useState("");
    const reorderingConversationRef = useRef(false);
    const sensors = useSensors(
      useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
      useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
    );
    const sameSectionCollision: CollisionDetection = (args) => {
      const groups = pointerWithin({ ...args, droppableContainers: args.droppableContainers.filter(container => container.data.current?.kind === 'conversation-group') });
      if (groups.length) return groups;
      return closestCenter({
        ...args,
        droppableContainers: args.droppableContainers.filter((container) =>
          container.data.current?.kind !== 'conversation-group' &&
          container.data.current?.pinned === args.active.data.current?.pinned &&
          container.data.current?.sortable?.containerId === args.active.data.current?.sortable?.containerId,
        ),
      });
    };
    const { setThink } = useChatThinkStore();
    const { setNewMessage } = useChatNewMessageStore();

    useEffect(() => {
      let active = true;
      ConversationSettingsApi().listChatExecutors().then((response) => {
        if (!active) return;
        setExternalAgents(
          response.data.data.executors.filter(
            (executor) => executor.kind === "external",
          ),
        );
        setKnownSources(rememberConversationSources([
          ...response.data.data.executors.filter((executor) => executor.connected).map((executor) => executor.id),
          ...(conversationFiltersRef.current.sources ?? []),
        ]));
      }).catch(() => {
        // Persisted and listed sources remain usable if host discovery is unavailable.
      });
      return () => { active = false; };
    }, []);

    useEffect(() => {
      const handleFilterChange = (event: Event) => {
        const next = (event as CustomEvent<ChatConversationFilters>).detail;
        if (next?.filter !== "normal" && next?.filter !== "task") return;
        if (next.filter !== conversationFiltersRef.current.filter) setFilterPopoverOpen(false);
        setKnownSources(rememberConversationSources([
          ...(conversationFiltersRef.current.sources ?? []), ...(next.sources ?? []),
        ]));
        conversationFiltersRef.current = next;
        setConversationFilters(next);
        setHistoryList([]);
        getHistory({ isFirst: true, searchText: keyword });
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

    useEffect(() => {
      const refresh = () => getHistory({ isFirst: true, searchText: keyword });
      window.addEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, refresh);
      return () => window.removeEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, refresh);
    }, [keyword, conversationFilters]);

    useEffect(() => {
      const renamed = (event: Event) => {
        const { conversationId, displayName, titleRevision } = (event as CustomEvent<ConversationTitleChangedDetail>).detail;
        setHistoryList(current => current.map(item => ({
          ...item,
          ...(item.conversation_id === conversationId ? { display_name: displayName, title_revision: titleRevision } : {}),
          ...(item.parent_conversation_id === conversationId ? { parent_display_name: displayName } : {}),
        })));
      };
      window.addEventListener(CONVERSATION_TITLE_CHANGED_EVENT, renamed);
      return () => window.removeEventListener(CONVERSATION_TITLE_CHANGED_EVENT, renamed);
    }, []);

    const conversationTree = useMemo(() => {
      const visibleHistory = historyList.filter(
        (item) => (showBatchExport && !(compact && groupSection)) || isConversationPinned(item) || !item.group_id,
      );
      const conversationsById = new Map(
        visibleHistory.map((item) => [item.conversation_id || "", item]),
      );
      const childrenByParent = new Map<string, SidebarConversation[]>();
      const nestedChildIds = new Set<string>();
      visibleHistory.forEach((item) => {
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
      const nodes: SidebarConversationNode[] = visibleHistory
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
    }, [historyList, showBatchExport, compact, groupSection]);

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
        yesterday: [],
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
          key: "yesterday" as const,
          title: t("chat.conversationGroupYesterday"),
          items: groups.yesterday,
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
        [...new Set([...historyList.flatMap((item) =>
          !isChildConversation(item) && item.conversation_id &&
          (!(compact && groupSection) || !item.group_id || isConversationPinned(item))
            ? [item.conversation_id]
            : [],
        ), ...batchGroupMembers.map(item => item.conversation_id)])],
      [historyList, batchGroupMembers, compact, groupSection],
    );

    function toggleBatchConversation(id: string, checked: boolean) {
      toggleBatchConversations([id], checked);
    }

    function toggleBatchConversations(ids: string[], checked: boolean) {
      const selectedIds = new Set(ids);
      setCheckedList(previous => checked ? [...new Set([...previous, ...ids])] : previous.filter(item => !selectedIds.has(item)));
    }

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
          // Activity does not carry task/source metadata. Only reorder known
          // rows optimistically; the filtered server query owns membership.
          if (!prev.some((item) => item.conversation_id === conversationId)) return prev;
          const next = bumpConversationToTop(prev, conversationId);
          if (prev.find((item) => item.conversation_id === conversationId)?.history_order == null) {
            window.requestAnimationFrame(() => {
              document.getElementById(scrollableTargetId)?.scrollTo({
                top: 0,
                behavior: "smooth",
              });
            });
          }
          return sortConversationHistory(next);
        });
        getHistory({ isFirst: true, searchText: keyword });
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
    }, [keyword, scrollableTargetId]);

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
      const activeFilter = conversationFiltersRef.current;
      const requestId = ++historyRequestRef.current;
      const replaceHistory = isFirst || historyRefreshRequiredRef.current;
      if (replaceHistory) historyRefreshRequiredRef.current = true;
      setIsHistoryLoading(true);

      ChatServiceApi()
        .conversationServiceListConversations(
          {
            keyword: searchText ?? keyword,
            pageToken: replaceHistory ? "" : pageToken,
            pageSize: 50,
          },
          {
            params: {
              is_task_conv: activeFilter.filter === "task" ? "true" : "false",
              ...(activeFilter.sources
                ? { assistants: activeFilter.sources.join(",") }
                : {}),
            },
          },
        )
        .then((res) => {
          const conversations: SidebarConversation[] =
            res?.data?.conversations ?? [];
          if (requestId !== historyRequestRef.current) return;
          setKnownSources(rememberConversationSources(conversations.map((conversation) => conversation.assistant || "lazymind")));
          setHistoryList((previous) => sortConversationHistory(
            [...new Map((isMore && !replaceHistory ? [...previous, ...conversations] : conversations)
              .map((item) => [item.conversation_id, item])).values()],
          ));
          setPageToken(res.data.next_page_token || "");
          historyRefreshRequiredRef.current = false;
          if (replaceHistory) setHistoryRevision((revision) => revision + 1);
        })
        .catch(() => {
          if (requestId !== historyRequestRef.current) return;
          message.error(t("chat.fork.historyLoadFailed"));
          // Reset InfiniteScroll's pending-load latch even when the row count
          // stays the same, so a failed refresh can retry from the first page.
          setHistoryRevision((revision) => revision + 1);
        })
        .finally(() => {
          if (requestId === historyRequestRef.current) setIsHistoryLoading(false);
        });
    }

    function removeHistoryItems(ids: string[]) {
      const removedIds = new Set(ids);
      // Side chats follow their parent; independent fork conversations do not.
      let size = 0;
      while (size !== removedIds.size) {
        size = removedIds.size;
        historyList.forEach((item) => {
          if (item.parent_conversation_id && removedIds.has(item.parent_conversation_id)) {
            removedIds.add(item.conversation_id || "");
          }
        });
      }
      ++historyRequestRef.current;
      setHistoryList((previous) => previous.filter((item) => !removedIds.has(item.conversation_id || "")));
      setBatchGroupMembers((previous) => previous.filter((item) => !removedIds.has(item.conversation_id)));
      setCheckedList((previous) => previous.filter((id) => !removedIds.has(id)));
      if (removedIds.has(currentSessionId)) {
        onRemove([...historyList, ...batchGroupMembers].find((item) => item.conversation_id === currentSessionId)
          || { conversation_id: currentSessionId });
      }
      emitConversationGroupsChanged();
      getHistory({ isFirst: true });
    }

    function deleteHistory(data: Conversation) {
      if (deleteHistoryInFlightRef.current) return;
      deleteHistoryInFlightRef.current = true;
      return ChatServiceApi()
        .conversationServiceDeleteConversation({
          conversation: data.conversation_id || "",
        })
        .then(() => {
          message.success(t("chat.deleteConversationSuccess"));
          removeHistoryItems([data.conversation_id || ""]);
        })
        .catch((error) => {
          message.error(t("settingsPage.recovery.operationFailed"));
          throw error;
        })
        .finally(() => {
          deleteHistoryInFlightRef.current = false;
        });
    }

    function setConversationPinned(
      conversation: SidebarConversation,
      pinned: boolean,
    ) {
      if (pinningConversationRef.current || reorderingConversationRef.current) {
        return;
      }
      const conversationId = conversation.conversation_id || "";
      if (!conversationId) {
        return;
      }
      pinningConversationRef.current = true;
      ++historyRequestRef.current;
      setIsHistoryLoading(false);
      setPinningConversationId(conversationId);
      return ChatServiceApi()
        .conversationServiceSetPinned(conversationId, pinned)
        .then((res) => {
          setHistoryList((previous) => applyConversationOrder(previous, {
            ...res.data,
            conversation_id: conversationId,
            is_pinned: pinned,
            pinned_at: pinned ? res.data?.pinned_at : null,
          }));
          getHistory({ isFirst: true });
          message.success(
            t(
              pinned
                ? "chat.pinConversationSuccess"
                : "chat.unpinConversationSuccess",
            ),
          );
          document.getElementById(scrollableTargetId)?.scrollTo({ top: 0 });
        })
        .catch((error) => {
          message.error(getLocalizedErrorMessage(error));
        })
        .finally(() => {
          pinningConversationRef.current = false;
          setPinningConversationId("");
        });
    }

    async function handleReorder({ active, over }: DragEndEvent) {
      if (!over || showBatchExport || keyword || active.id === over.id || reorderingConversationRef.current || pinningConversationRef.current) return;
      const moved = historyList.find((item) => item.conversation_id === active.id);
      const targetGroupId = over.data?.current?.kind === 'conversation-group' ? over.data.current.groupId as string : '';
      if (moved && targetGroupId) {
        if (moved.group_kind === "project" || moved.group_id === targetGroupId || moved.organizing_run_id || isChildConversation(moved)) return;
        reorderingConversationRef.current = true;
        setReorderingConversationId(String(active.id));
        try {
          await assignConversation(targetGroupId, String(active.id));
          emitConversationGroupsChanged();
        } catch { /* The shared request interceptor displays the API error. */ }
        finally { reorderingConversationRef.current = false; setReorderingConversationId(''); }
        return;
      }
      const target = historyList.find((item) => item.conversation_id === over.id);
      if (!moved || !target || isConversationPinned(moved) !== isConversationPinned(target)) return;
      if (compact && !isConversationPinned(moved) && getConversationGroup(moved.update_time) !== getConversationGroup(target.update_time)) return;
      const sourceIndex = historyList.indexOf(moved);
      const targetIndex = historyList.indexOf(target);
      reorderingConversationRef.current = true;
      ++historyRequestRef.current;
      setIsHistoryLoading(false);
      setReorderingConversationId(String(active.id));
      try {
        const response = await ChatServiceApi().conversationServiceReorder(
          String(active.id), String(over.id), sourceIndex < targetIndex ? "after" : "before",
        );
        setHistoryList((previous) => applyConversationOrder(previous, response.data));
        getHistory({ isFirst: true });
      } catch {
        message.error(t("chat.reorderConversationFailed"));
      } finally {
        reorderingConversationRef.current = false;
        setReorderingConversationId("");
      }
    }

    async function confirmDeleteHistory(data: Conversation) {
      if ((data as SidebarConversation).organizing_run_id) {
        message.warning(t("conversationOrganizer.deleteLocked"));
        return;
      }
      let hasForks = false;
      try {
        const response = await ChatServiceApi().conversationServiceGetConversationDetail({ conversation: data.conversation_id || "" });
        hasForks = Boolean((response.data.conversation as { has_fork_descendants?: boolean })?.has_fork_descendants);
      } catch { message.error(t("chat.fork.historyLoadFailed")); return; }
      await modal.confirm({
        title: t("settingsPage.recovery.moveToTrashTitle"),
        content: t("settingsPage.recovery.moveToTrashDescription") + (hasForks ? " " + t("chat.fork.deleteNotice") : ""),
        okText: t("common.delete"),
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
                  emitConversationGroupsChanged();
                  getHistory({ isFirst: true });
                })
                .catch((error) => message.error(getLocalizedErrorMessage(error)));
            }}>{t("settingsPage.recovery.undo")}</Button>
            <Button type="link" size="small" onClick={() => navigate(RECOVERY_ARCHIVE_PATH)}>{t("settingsPage.recovery.viewArchived")}</Button>
          </span>
        ),
      });
    }

    async function batchDeleteHistory() {
      if (!checkedList.length) {
        message.warning(t("chat.selectConversationToDelete"));
        return;
      }
      if (batchDeleteInFlightRef.current) {
        return;
      }
      await modal.confirm({
        title: t("chat.batchDeleteConversationTitle", {
          count: checkedList.length,
        }),
        content: t("chat.batchDeleteConversationContent") + " " + t("chat.fork.deleteNotice"),
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
              const deletedIds = res.data?.deleted_ids ?? checkedList;
              const remainingIds = checkedList.filter((id) => !deletedIds.includes(id));
              message.success(t("chat.batchDeleteConversationSuccess", { count: res.data?.deleted_count ?? deletedIds.length }));
              removeHistoryItems(deletedIds);
              setCheckedList(remainingIds);
              if (remainingIds.length) message.error(t("settingsPage.recovery.operationFailed"));
              else setShowBatchExport(false);
            })
            .catch((error) => {
              message.error(t("settingsPage.recovery.operationFailed"));
              throw error;
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
      setBatchGroupMembers([]);
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
        key: "archive",
        label: t("chat.batchArchive"),
        icon: <InboxOutlined />,
        disabled: !checkedList.length || historyList.some(
          (item) => checkedList.includes(item.conversation_id || "") && Boolean(item.organizing_run_id),
        ),
        onClick: () => setBatchArchiveIds([...checkedList]),
      },
      {
        key: "delete",
        label: t("common.delete"),
        icon: <DeleteOutlined />,
        danger: true,
        disabled: !checkedList.length || historyList.some(
          (item) => checkedList.includes(item.conversation_id || "") && Boolean(item.organizing_run_id),
        ),
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
              disabled: Boolean(pinningConversationId || reorderingConversationId),
              onClick: () => setConversationPinned(item, !pinned),
            },
            ...(item.group_kind === "project" ? [] : [{
              key: "move-to-group",
              label: t("conversationOrganizer.moveToGroup"),
              disabled: Boolean(item.organizing_run_id),
              children: conversationGroupSubmenu({ conversationId, groupId: item.group_id, title: item.display_name }, () => setMovingConversation(item)),
            }]),
          ];
      const activateConversation = () => {
        if (showBatchExport || selected) return;
        onSelected(item);
        setThink(false);
        setNewMessage(false);
      };
      return (
        <ConversationPreview conversationId={conversationId} title={conversationTitle} summary={item.summary} updateTime={item.update_time} isTask={Boolean(item.is_task_conv)} relation={relationDescription} disabled={showBatchExport || renamingId === conversationId}>
        <div
          aria-busy={item.metadata_pending || undefined}
          data-title-revision={item.title_revision}
          className={classnames("record", {
            selected,
            "record-child": isChild,
            "record-renaming": renamingId === conversationId,
          })}
          key={item.conversation_id}
          role={showBatchExport || renamingId === conversationId ? undefined : "button"}
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
          {renamingId === conversationId ? <ConversationTitleEditor key={conversationId} conversationId={conversationId} initialTitle={conversationTitle} onClose={() => setRenamingId(null)} /> : <span className="title">{conversationTitle}</span>}
          <ConversationRunningIndicator conversationId={conversationId} />
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
              destroyPopupOnHide
              trigger={["click"]}
              menu={{
                onClick: ({ domEvent }) => domEvent.stopPropagation(),
                items: [
                  { key: "rename", label: t("conversationOrganizer.renameConversation"), onClick: () => setRenamingId(conversationId) },
                  ...ownershipActionItems,
                  ...(!isChildConversation(item) ? [{
                    key: "archive",
                    icon: <FolderOutlined />,
                    label: t("settingsPage.recovery.archiveAction"),
                    disabled: Boolean(item.organizing_run_id),
                    onClick: () => setArchiveItem(item),
                  }] : []),
                  {
                    key: "trash",
                    icon: <DeleteOutlined />,
                    danger: true,
                    label: t("common.delete"),
                    disabled: Boolean(item.organizing_run_id),
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
        </ConversationPreview>
      );
    }

    function renderItem(pinnedOnly?: boolean) {
      const renderNode = (node: SidebarConversationNode) => {
        const item = node.conversation;
        const conversationId = item.conversation_id || "";
        const selected = conversationId === currentSessionId;
        const childrenExpanded = expandedParentIds.has(conversationId);
        const record = showBatchExport ? (
          <Checkbox
            className="export-checkbox-item"
            checked={checkedList.includes(conversationId)}
            onChange={event => toggleBatchConversation(conversationId, event.target.checked)}
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
          <SortableConversationRow
            key={conversationId}
            id={conversationId}
            title={item.display_name || conversationId}
            pinned={isConversationPinned(item)}
            hideDragHandle={item.group_kind === "project" || showBatchExport || renamingId === conversationId}
            disabled={item.group_kind === "project" || showBatchExport || Boolean(renamingId) || isHistoryLoading || Boolean(keyword || pinningConversationId || reorderingConversationId || item.organizing_run_id) || Boolean(node.isPlaceholderParent)}
          >
            <Col span={24} draggable={item.group_kind !== "project" && renamingId !== conversationId && !showBatchExport && !keyword && !item.organizing_run_id && !isChildConversation(item) && !node.isPlaceholderParent && !item.is_task_conv} onDragStart={(e: React.DragEvent<HTMLElement>) => startConversationDrag(e, conversationId, item.group_id)}>{record}</Col>
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
          </SortableConversationRow>
        );
      };

      const content = compact ? (
          <div className="record-groups">
            {groupedHistoryList.filter(group => pinnedOnly === undefined || (group.key === "pinned") === pinnedOnly).map((group) => (
              <div className="record-group" key={group.key}>
                <div className="record-group-title">{group.title}</div>
                <SortableContext items={group.items.map((node) => node.conversation.conversation_id || "")} strategy={verticalListSortingStrategy}>
                  <Row>
                    {group.items.map((node) => renderNode(node))}
                  </Row>
                </SortableContext>
              </div>
            ))}
          </div>
      ) : <Row>{conversationTree.map((node) => renderNode(node))}</Row>;
      return compact ? content : (
        <SortableContext items={conversationTree.map((node) => node.conversation.conversation_id || "")} strategy={verticalListSortingStrategy}>
          {content}
        </SortableContext>
      );
    }

    return (
      <DndContext sensors={sensors} collisionDetection={sameSectionCollision} onDragEnd={handleReorder}
        accessibility={{
          screenReaderInstructions: { draggable: t("chat.reorderConversationHint") },
          announcements: {
            onDragStart: ({ active }: DragStartEvent) => t("chat.reorderConversationStarted", { name: historyList.find(item => item.conversation_id === active.id)?.display_name }),
            onDragOver: ({ over }: DragOverEvent) => over ? t("chat.reorderConversationOver", { name: over.data.current?.label || historyList.find(item => item.conversation_id === over.id)?.display_name }) : undefined,
            onDragEnd: () => t("chat.reorderConversationEnded"),
            onDragCancel: () => t("chat.reorderConversationCanceled"),
          },
        }}>
      <div id={compact && groupSection ? scrollableTargetId : undefined} className={classnames("record-container", { compact, "grouped-sidebar": compact && groupSection })} onDragOver={e => { if (!showBatchExport && !keyword && e.dataTransfer.types.includes(CONVERSATION_DRAG)) e.preventDefault(); }} onDrop={async e => { const item = readConversationDrag(e); if (!item || keyword || showBatchExport) return; e.preventDefault(); if (item.groupId) { await removeConversation(item.groupId, item.id); emitConversationGroupsChanged(); } }}>
        {modalContextHolder}
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
            removeHistoryItems([archived.conversation_id || ""]);
            showArchivedFeedback(archived);
          }}
        />
        <ArchiveConversationModal
          open={batchArchiveIds !== null}
          conversationIds={batchArchiveIds || []}
          onCancel={() => setBatchArchiveIds(null)}
          onArchived={(archivedIds, failedIds) => {
            setBatchArchiveIds(null);
            removeHistoryItems(archivedIds);
            setCheckedList(failedIds);
            if (!failedIds.length) setShowBatchExport(false);
            message.open({
              type: "success",
              duration: 8,
              content: <span className="archive-feedback">
                {t("chat.batchArchiveSuccess", { count: archivedIds.length })}
                <Button type="link" size="small" onClick={() => navigate(RECOVERY_ARCHIVE_PATH)}>{t("settingsPage.recovery.viewArchived")}</Button>
              </span>,
            });
          }}
        />
        <ConversationMembershipModal conversation={movingConversation?.conversation_id ? { conversationId: movingConversation.conversation_id, groupId: movingConversation.group_id, title: movingConversation.display_name } : null} onClose={() => setMovingConversation(null)} />
        {compact && groupSection && !showBatchExport && <>{renderItem(true)}{groupSection()}</>}
        {!hideHeader && (
          <div className="record-header">
            {(!compact || showBatchActions) && (
              <div className="record-header-top">
                <div className="list-title">
                  {compact ? t("chat.recentConversations") : title || t("chat.chatHistory")}
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
                                value={conversationFilters.sources ?? visibleSources}
                                onChange={(vals) => {
                                  const next = vals as string[];
                                  if (next.length === 0) {
                                    message.warning(t("chat.selectAtLeastOneConvType"));
                                    return;
                                  }
                                  selectChatConversationSources(next);
                                }}
                                style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
                              >
                                {visibleSources.map((source) => (
                                  <Checkbox key={source} value={source}>
                                    {source === "lazymind" ? t("chat.lazyMindConversation")
                                      : externalAgents.find((agent) => agent.id === source)?.display_name ?? source}
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
                            title={t("chat.filterConversationType")}
                            aria-label={t("chat.filterConversationType")}
                            style={{ padding: '0 4px' }}
                          />
                        </Popover>
                        <Tooltip title={t("settingsPage.recovery.viewArchived")}>
                          <Button size="small" type="text" icon={<InboxOutlined />}
                            aria-label={t("settingsPage.recovery.viewArchived")}
                            onClick={() => navigate(RECOVERY_ARCHIVE_PATH)} />
                        </Tooltip>
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
            {compact && !showBatchExport && <ConversationGroups mode="organizer" onChanged={emitConversationGroupsChanged} />}
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
        <div className="record-list" id={compact && groupSection ? undefined : scrollableTargetId}>
          {!isHistoryLoading && !historyList?.length && !(showBatchExport && compact && groupSection) ? (
            <div className="record-empty" role="status">
              {t("chat.noConversations")}
            </div>
          ) : (
            <InfiniteScroll
              key={historyRevision}
              dataLength={historyList?.length || 0}
              next={() => getHistory({ isMore: true })}
              hasMore={!!pageToken}
              loader={<Spin />}
              scrollableTarget={scrollableTargetId}
            >
              {showBatchExport ? (
                <div className="export-checkbox-group">
                  {compact && groupSection ? <>
                    {renderItem(true)}
                    {groupSection({ checkedIds: checkedList, onToggle: toggleBatchConversation, onToggleMany: toggleBatchConversations, onMembersChange: setBatchGroupMembers })}
                    {renderItem(false)}
                  </> : renderItem()}
                </div>
              ) : (
                renderItem(compact && groupSection ? false : undefined)
              )}
            </InfiniteScroll>
          )}
        </div>
      </div>
      </DndContext>
    );
  },
);

export default RecordList;
