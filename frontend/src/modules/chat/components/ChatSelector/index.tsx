import { Button, Input, Popover, Tooltip, message } from "antd";
import {
  SearchOutlined,
  CheckOutlined,
  PushpinFilled,
  PushpinOutlined,
} from "@ant-design/icons";
import {
  useEffect,
  useState,
  forwardRef,
  useImperativeHandle,
  useMemo,
  useRef,
  MouseEvent,
  ReactNode,
} from "react";
import {
  KnowledgeBaseServiceApi,
} from "@/modules/chat/utils/request";
import { Dataset } from "@/api/generated/knowledge-client";
import KnowledgeIcon from "../../assets/icons/knowledge.svg?react";
import "./index.scss";
import { debounce } from "lodash";
import { ChatConfig } from "../ChatConfigs";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { AgentAppsAuth } from "@/components/auth";

export interface ChatSelectorProps {
  chatConfig: ChatConfig;
  refreshKey?: number | string;
  embeddingReady?: boolean | null;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  onChange?: (
    knowledgeIds: string[],
    creators: string[],
    tags: string[],
  ) => void;
}

export interface ChatSelectorImperativeProps {
  open: (triggerElement: HTMLElement) => void;
  close: () => void;
}

const ChatSelector = forwardRef<ChatSelectorImperativeProps, ChatSelectorProps>(
  (props, ref) => {
  const { chatConfig, refreshKey, onChange, embeddingReady, multimodalEmbeddingReady, rerankReady } = props;
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === 'system-admin';
  const isEmbeddingDisabled = embeddingReady === false || multimodalEmbeddingReady === false || rerankReady === false;

  const buildKnowledgeDisabledReason = (): ReactNode => {
    const goConfig = (
      <a
        href="/model-providers"
        style={{ marginLeft: 6, color: '#fff', textDecoration: 'underline' }}
        onClick={(e: MouseEvent<HTMLAnchorElement>) => { e.preventDefault(); navigate('/model-providers'); }}
      >
        {t("knowledge.goToConfig")}
      </a>
    );
    if (embeddingReady === false) {
      return isAdmin
        ? <span>{t("chat.embeddingNotReadyKnowledgeAdmin")}{goConfig}</span>
        : t("chat.embeddingNotReadyKnowledge");
    }
    if (multimodalEmbeddingReady === false) {
      return isAdmin
        ? <span>{t("chat.multimodalEmbeddingNotReadyKnowledgeAdmin")}{goConfig}</span>
        : t("chat.multimodalEmbeddingNotReadyKnowledge");
    }
    if (rerankReady === false) {
      return <span>{t("chat.rerankNotReadyKnowledge")}{goConfig}</span>;
    }
    return undefined;
  };
  const knowledgeDisabledReason = buildKnowledgeDisabledReason();

    const [knowledgeBaseList, setKnowledgeBaseList] = useState<Dataset[]>([]);
    const [filteredList, setFilteredList] = useState<Dataset[]>([]);
    const [selectedIds, setSelectedIds] = useState<string[]>([]);
    const [open, setOpen] = useState(false);
    const [knowledgeLoading, setKnowledgeLoading] = useState(false);
    const [defaultKnowledgeId, setDefaultKnowledgeId] = useState<string[]>([]);
    const [searchValue, setSearchValue] = useState<string>("");
    const [defaultUpdatingId, setDefaultUpdatingId] = useState("");
    const isResettingSelectionRef = useRef(false);
    const previousRefreshKeyRef = useRef(refreshKey);

    function getDefaultDatasetIds(datasets: Dataset[]) {
      return (datasets
        ?.filter((it) => it?.default_dataset)
        ?.map((k) => k.dataset_id)
        .filter(Boolean) as string[]) || [];
    }

    function mergeSelectedIds(...groups: Array<Array<string | undefined>>) {
      return [
        ...new Set(groups.flat().filter((id): id is string => Boolean(id))),
      ];
    }

    useEffect(() => {
      if (isResettingSelectionRef.current) {
        return;
      }
      const setData = new Set([
        ...defaultKnowledgeId,
        ...(chatConfig?.knowledgeBaseId || []),
      ]);
      setSelectedIds([...setData]);
    }, [chatConfig, defaultKnowledgeId]);

    useEffect(() => {
      const hasDocumentFilters =
        (chatConfig?.creators?.length ?? 0) > 0 ||
        (chatConfig?.tags?.length ?? 0) > 0;

      if (!hasDocumentFilters) {
        return;
      }

      onChange?.(
        mergeSelectedIds(defaultKnowledgeId, chatConfig?.knowledgeBaseId ?? []),
        [],
        [],
      );
    }, [chatConfig, defaultKnowledgeId, onChange]);

    useImperativeHandle(ref, () => ({
      open: () => {
        setOpen(true);
      },
      close: () => setOpen(false),
    }));

    useEffect(() => {
      getKnowledgeBaseList();
    }, []);

    useEffect(() => {
      if (
        refreshKey === undefined ||
        previousRefreshKeyRef.current === refreshKey
      ) {
        return;
      }

      previousRefreshKeyRef.current = refreshKey;
      getKnowledgeBaseList();
    }, [refreshKey]);

    function getKnowledgeBaseList() {
      setKnowledgeLoading(true);
      KnowledgeBaseServiceApi()
        .datasetServiceListDatasets({ pageSize: 1000 })
        .then((res) => {
          const datasets = res.data.datasets || [];
          setKnowledgeBaseList(datasets);
          setFilteredList(datasets);
          const defaultIds = getDefaultDatasetIds(datasets);
          setDefaultKnowledgeId(defaultIds);
          const mergedIds = mergeSelectedIds(
            defaultIds,
            chatConfig?.knowledgeBaseId ?? [],
          );
          setSelectedIds(mergedIds);
          if (
            defaultIds.length > 0 &&
            (!chatConfig?.knowledgeBaseId ||
              chatConfig.knowledgeBaseId.length === 0)
          ) {
            onChange?.(
              mergedIds,
              [],
              [],
            );
          }
        })
        .finally(() => setKnowledgeLoading(false));
    }

    const filterKnowledgeBaseListFn = debounce((search: string) => {
      setSearchValue(search);
    }, 300);

    const sortedAndFilteredList = useMemo(() => {
      let list = [...knowledgeBaseList];
      const originalIndexMap = new Map(
        knowledgeBaseList.map((item, index) => [item.dataset_id || `idx-${index}`, index]),
      );

      if (searchValue.trim()) {
        list = list.filter((item) =>
          item.display_name?.toLowerCase().includes(searchValue.toLowerCase()),
        );
      }

      list.sort((a, b) => {
        const aDefault = !!a.default_dataset;
        const bDefault = !!b.default_dataset;
        const aSelected = selectedIds.includes(a.dataset_id || "");
        const bSelected = selectedIds.includes(b.dataset_id || "");
        const aIndex = originalIndexMap.get(a.dataset_id || "") ?? 0;
        const bIndex = originalIndexMap.get(b.dataset_id || "") ?? 0;

        if (aDefault && !bDefault) {
          return -1;
        }
        if (!aDefault && bDefault) {
          return 1;
        }

        if (aSelected && !bSelected) {
          return -1;
        }
        if (!aSelected && bSelected) {
          return 1;
        }

        return aIndex - bIndex;
      });

      return list;
    }, [knowledgeBaseList, selectedIds, searchValue]);

    useEffect(() => {
      setFilteredList(sortedAndFilteredList);
    }, [sortedAndFilteredList]);

    function handleItemClick(item: Dataset) {
      const datasetId = item.dataset_id;
      if (!datasetId) {
        return;
      }

      const newSelectedIds = selectedIds.includes(datasetId)
        ? selectedIds.filter((id) => id !== datasetId)
        : [...selectedIds, datasetId];

      setSelectedIds(newSelectedIds);
      onChange?.(
        newSelectedIds,
        [],
        [],
      );
    }

    async function setDefaultDatasetFn(item: Dataset) {
      const datasetId = item.dataset_id;
      const datasetName = item.display_name || "";
      if (!datasetId || defaultUpdatingId) {
        return;
      }

      const nextDefault = !item.default_dataset;
      setDefaultUpdatingId(datasetId);
      try {
        if (nextDefault) {
          await KnowledgeBaseServiceApi().datasetServiceSetDefaultDataset(
            datasetId,
            datasetName,
          );
        } else {
          await KnowledgeBaseServiceApi().datasetServiceUnsetDefaultDataset(
            datasetId,
            datasetName,
          );
        }

        setKnowledgeBaseList((previous) =>
          previous.map((knowledgeBase) =>
            knowledgeBase.dataset_id === datasetId
              ? { ...knowledgeBase, default_dataset: nextDefault }
              : knowledgeBase,
          ),
        );
        setDefaultKnowledgeId((previous) =>
          nextDefault
            ? mergeSelectedIds(previous, [datasetId])
            : previous.filter((id) => id !== datasetId),
        );

        if (nextDefault && !selectedIds.includes(datasetId)) {
          const nextSelectedIds = mergeSelectedIds(selectedIds, [datasetId]);
          setSelectedIds(nextSelectedIds);
          onChange?.(
            nextSelectedIds,
            [],
            [],
          );
        }
      } catch (error) {
        console.error("Set default dataset failed:", error);
        message.error(t("chat.pinKnowledgeBaseFailed"));
      } finally {
        setDefaultUpdatingId("");
      }
    }

    function renderDefaultItem(item: Dataset, isSelected: boolean) {
      if (!isSelected) {
        return null;
      }

      const isDefault = Boolean(item.default_dataset);
      const Icon = isDefault ? PushpinFilled : PushpinOutlined;
      return (
        <Tooltip
          title={isDefault ? t("chat.unpinKnowledgeBase") : t("chat.pinKnowledgeBase")}
        >
          <Icon
            className={`chat-selector-pin-icon ${isDefault ? "is-pinned" : ""}`}
            onClick={(e) => {
              e.stopPropagation();
              void setDefaultDatasetFn(item);
            }}
          />
        </Tooltip>
      );
    }

    function renderContent() {
      return (
        <div className="chat-selector-container">
          <div className="chat-selector-search-box">
            <Input
              suffix={<SearchOutlined style={{ color: "#999" }} />}
              placeholder={t("chat.searchKnowledge")}
              onChange={(e) => filterKnowledgeBaseListFn(e.target.value)}
              className="chat-selector-search-input"
              autoFocus
              disabled={knowledgeLoading}
            />
            <Button
              type="link"
              className="chat-selector-action-button"
              disabled={knowledgeLoading}
              onClick={() => {
                isResettingSelectionRef.current = true;
                setSelectedIds(defaultKnowledgeId);
                onChange?.(
                  defaultKnowledgeId,
                  [],
                  [],
                );
                window.setTimeout(() => {
                  isResettingSelectionRef.current = false;
                }, 0);
              }}
            >
              {t("chat.reset")}
            </Button>
            {selectedIds.length !== knowledgeBaseList.length ? (
              <Button
                type="link"
                className="chat-selector-action-button"
                disabled={knowledgeLoading}
                onClick={() => {
                  const allIds = knowledgeBaseList.map(
                    (item) => item.dataset_id || "",
                  );
                  setSelectedIds(allIds);
                  onChange?.(
                    allIds,
                    [],
                    [],
                  );
                }}
              >
                {t("chat.selectAll")}
              </Button>
            ) : (
              <Button
                type="link"
                className="chat-selector-action-button"
                onClick={() => {
                  setSelectedIds(defaultKnowledgeId);
                  onChange?.(
                    defaultKnowledgeId,
                    [],
                    [],
                  );
                }}
              >
                {t("chat.cancelSelectAll")}
              </Button>
            )}
          </div>
          <div className="chat-selector-list-container">
            {filteredList.map((item) => {
              const isSelected = selectedIds.includes(item.dataset_id || "");
              return (
                <div
                  key={item.dataset_id}
                  className={`chat-selector-list-item ${isSelected ? "selected" : ""} ${
                    item.default_dataset ? "defaultDataset" : ""
                  }`}
                  onClick={() => handleItemClick(item)}
                >
                  <span className="chat-selector-item-label">
                    {item.display_name}
                  </span>
                  <span className="chat-selector-item-actions">
                    {renderDefaultItem(item, isSelected)}
                    {isSelected && (
                      <CheckOutlined className="chat-selector-check-icon" />
                    )}
                  </span>
                </div>
              );
            })}
            {knowledgeLoading ? (
              <div className="chat-selector-empty-text">{t("chat.loadingWait")}</div>
            ) : !filteredList?.length ? (
              <div className="chat-selector-empty-text">{t("chat.noData")}</div>
            ) : null}
          </div>
        </div>
      );
    }

    return (
      <div className="chat-selector-wrapper">
        <Popover
          content={renderContent()}
          classNames={{ root: "knowledgePopover" }}
          trigger="click"
          open={open}
          onOpenChange={(bool) => {
            if (isEmbeddingDisabled) return;
            setOpen(bool);
          }}
        >
          <Tooltip title={isEmbeddingDisabled ? knowledgeDisabledReason : undefined}>
            <div
              className={`input-bottom-actions-left-item ${open || selectedIds.length > 0 ? "selected" : ""}${isEmbeddingDisabled ? " is-disabled" : ""}`}
              aria-disabled={isEmbeddingDisabled}
              onClick={(e) => {
                if (isEmbeddingDisabled) {
                  e.stopPropagation();
                }
              }}
            >
              <KnowledgeIcon />
              {t("chat.knowledgeBase")}
            </div>
          </Tooltip>
        </Popover>
      </div>
    );
  },
);

export default ChatSelector;
