import {
  FC,
  useState,
  useEffect,
  useRef,
  useCallback,
  useMemo,
  MouseEvent,
  ChangeEvent,
} from "react";
import {
  Alert,
  Button,
  Form,
  Tooltip,
  Flex,
  message,
  Input,
  TablePaginationConfig,
  Select,
  Tag,
  Space,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useNavigate } from "react-router-dom";
import moment from "moment";
import {
  AppstoreOutlined,
  DatabaseOutlined,
  DownOutlined,
  HistoryOutlined,
  PlusOutlined,
  SearchOutlined,
} from "@ant-design/icons";

import SyncKnowledgeBaseCreationFlow, {
  useSyncKnowledgeBaseCreation,
} from "@/modules/knowledge/components/SyncKnowledgeBaseCreationFlow";
import TypedConfirmModal, {
  type TypedConfirmModalRef,
} from "@/components/ui/TypedConfirmModal";
import CreateUpdateModal, {
  UpdateImperativeProps,
} from "@/modules/knowledge/components/UpdateModal";
import CreateKnowledgeBaseModal, {
  CreateKnowledgeBaseModalRef,
} from "@/modules/knowledge/components/CreateKnowledgeBaseModal";
import UIUtils from "@/modules/knowledge/utils/ui";
import { runtimeFeatures } from "@/runtime/features";
import {
  KnowledgeBaseServiceApi,
} from "@/modules/knowledge/utils/request";
import { ALL_TAGS } from "@/modules/knowledge/constants/common";
import {
  Dataset,
  DatasetAclEnum,
} from "@/api/generated/knowledge-client";
import KnowledgeTag from "@/modules/knowledge/components/KnowledgeTag";
import FileUtils from "@/modules/knowledge/utils/file";

import { ListPageTable } from "@/components/ui";
import { useTranslation } from "react-i18next";
import { axiosInstance, BASE_URL } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import {
  fetchModelFeatures,
  isImageEmbedRequired,
  MODEL_FEATURES_CHANGED_EVENT,
} from "@/hooks/useModelFeatures";
import { dataSourceScanApi } from "@/modules/dataSource/api/clients";
import type { DataSourceItem, SourceType } from "@/modules/dataSource/constants/types";
import { sourceTypeOptions } from "@/modules/dataSource/constants/sourceTypeOptions";
import { mapScanSourceToDataSource } from "@/modules/dataSource/mappers/scanSourceToDataSource";
import {
  getFirstScanBinding,
  getScanSourceId,
  type ScanV2Binding,
  type ScanV2Source,
} from "@/modules/dataSource/utils/scanAccessors";
import {
  getConnectionMeta,
  getSourceTypeTitle,
  getStatusMeta,
  getSyncModeLabel,
  normalizeDataSourceStatus,
} from "@/modules/dataSource/utils/status";
import KnowledgeSquare from "./KnowledgeSquare";
import {
  createInitialKnowledgeSquareStatus,
  OFFICIAL_KNOWLEDGE_BASES,
  type KnowledgeSquareStatusMap,
  type OfficialKnowledgeBase,
} from "./knowledgeSquareData";

import "./index.scss";
import "@/modules/dataSource/index.scss";

const { Text } = Typography;

type SourceCategory = "local" | "cloudArchive" | "official";
type KnowledgePageView = "mine" | "square";

interface KnowledgePageProps {
  modelSettingsPath?: string;
  taskCenterPath?: string;
}

const KnowledgePage: FC<KnowledgePageProps> = ({
  modelSettingsPath = "/model-providers",
  taskCenterPath = "/task-center",
}) => {
  const [form] = Form.useForm();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const confirmRef = useRef<TypedConfirmModalRef>(null);
  const createUpdateRef = useRef<UpdateImperativeProps>(null);
  const createKnowledgeRef = useRef<CreateKnowledgeBaseModalRef>(null);

  const [loading, setLoading] = useState(false);
  const [pagination, setPagination] = useState<TablePaginationConfig>({
    current: 1,
    pageSize: 10,
    total: 0,
  });
  const [dataSource, setDataSource] = useState<Dataset[] | undefined>([]);
  // Keep a local default option to avoid label flicker while tags are loading.
  const [tags, setTags] = useState<string[]>([ALL_TAGS]);
  const [sourceCategory, setSourceCategory] = useState<SourceCategory>("local");
  const [activeView, setActiveView] = useState<KnowledgePageView>("mine");
  const [officialStatus, setOfficialStatus] = useState<KnowledgeSquareStatusMap>(
    createInitialKnowledgeSquareStatus,
  );
  const [mineSort, setMineSort] = useState("updated");
  const [officialSearch, setOfficialSearch] = useState("");
  const [cloudSources, setCloudSources] = useState<DataSourceItem[]>([]);
  const [embeddingReady, setEmbeddingReady] = useState<boolean | null>(null);
  const [multimodalEmbeddingReady, setMultimodalEmbeddingReady] = useState<
    boolean | null
  >(null);
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === "system-admin";
  const syncCreateVm = useSyncKnowledgeBaseCreation({
    onSuccess: () => {
      getTableData();
      createKnowledgeRef.current?.onClose();
    },
  });
  const cloudSourceRequestSeqRef = useRef(0);
  const isCloudArchiveView = sourceCategory === "cloudArchive";
  const isOfficialView = sourceCategory === "official";
  const createActionDisabled =
    embeddingReady === false || multimodalEmbeddingReady === false;
  const createActionDisabledTooltip = isAdmin ? (
    <span>
      {embeddingReady === false
        ? t("knowledge.embeddingNotReadyBannerAdmin")
        : t("knowledge.multimodalEmbeddingNotReadyBannerAdmin")}
      <a
        href="/model-providers"
        style={{ marginLeft: 8, color: "#fff", textDecoration: "underline" }}
        onClick={(e: MouseEvent<HTMLAnchorElement>) => {
          e.preventDefault();
          navigate(modelSettingsPath);
        }}
      >
        {t("knowledge.goToConfig")}
      </a>
    </span>
  ) : embeddingReady === false ? (
    t("knowledge.embeddingNotReadyBanner")
  ) : (
    t("knowledge.multimodalEmbeddingNotReadyBanner")
  );

  useEffect(() => {
    getTags();
    void checkEmbeddingReady();

    const onFeaturesChanged = () => {
      void checkEmbeddingReady();
    };
    window.addEventListener(MODEL_FEATURES_CHANGED_EVENT, onFeaturesChanged);
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        void checkEmbeddingReady();
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.removeEventListener(
        MODEL_FEATURES_CHANGED_EVENT,
        onFeaturesChanged,
      );
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, []);

  async function checkEmbeddingReady() {
    try {
      const features = await fetchModelFeatures(true);
      const imageEmbedRequired = isImageEmbedRequired(features);

      const [embResp, multiResp] = await Promise.all([
        axiosInstance
          .get<
            { data?: { ready: boolean } } | { ready: boolean }
          >(`${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_main`)
          .catch(() => null),
        imageEmbedRequired
          ? axiosInstance
              .get<
                { data?: { ready: boolean } } | { ready: boolean }
              >(`${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_image`)
              .catch(() => null)
          : Promise.resolve(null),
      ]);
      const unwrap = (resp: typeof embResp): boolean | null => {
        if (!resp) return null;
        const d =
          resp.data && typeof resp.data === "object" && "data" in resp.data
            ? (resp.data as { data?: { ready: boolean } }).data
            : (resp.data as { ready: boolean });
        return d?.ready ?? null;
      };
      setEmbeddingReady(unwrap(embResp));
      // null means "not applicable" — does not trigger disabled state.
      setMultimodalEmbeddingReady(
        imageEmbedRequired ? unwrap(multiResp) : null,
      );
    } catch {
      setEmbeddingReady(null);
      setMultimodalEmbeddingReady(null);
    }
  }

  useEffect(() => {
    if (activeView === "mine" && sourceCategory !== "official") {
      getTableData(1, pagination.pageSize);
    }
  }, [activeView, sourceCategory]);

  const loadCloudSources = useCallback(
    async (page = 1, pageSize = 10, keyword = "") => {
      const requestSeq = cloudSourceRequestSeqRef.current + 1;
      cloudSourceRequestSeqRef.current = requestSeq;
      setLoading(true);

      try {
        const sourcesResponse = await dataSourceScanApi.listSources({
          page,
          pageSize,
          keyword: keyword.trim() || undefined,
        });
        const sourceList = (sourcesResponse.data.items || []) as ScanV2Source[];
        const visibleSourceList = sourceList.filter(
          (source) => normalizeDataSourceStatus(source.status) !== "deleted",
        );
        const nextSources = await Promise.all(
          visibleSourceList.map(async (source) => {
            const sourceId = getScanSourceId(source);
            if (!sourceId) {
              return mapScanSourceToDataSource(source, t);
            }
            try {
              const detailResponse = await dataSourceScanApi.getSource({ sourceId });
              const detailSource = {
                ...source,
                ...detailResponse.data.source,
                summary: source.summary,
              } as ScanV2Source;
              const bindings = (detailResponse.data.bindings || []) as ScanV2Binding[];
              return mapScanSourceToDataSource(
                detailSource,
                t,
                undefined,
                getFirstScanBinding(bindings),
                bindings,
              );
            } catch {
              return mapScanSourceToDataSource(source, t);
            }
          }),
        );

        if (cloudSourceRequestSeqRef.current !== requestSeq) {
          return;
        }

        setCloudSources(nextSources);
        setPagination({
          current: page,
          pageSize,
          total: Number(sourcesResponse.data.total || 0),
        });
      } catch (error) {
        if (cloudSourceRequestSeqRef.current !== requestSeq) {
          return;
        }
        setCloudSources([]);
        setPagination({
          current: page,
          pageSize,
          total: 0,
        });
      } finally {
        if (cloudSourceRequestSeqRef.current === requestSeq) {
          setLoading(false);
        }
      }
    },
    [t],
  );

  const resolveCloudArchiveDatasetId = useCallback((record: DataSourceItem) => {
    return `${record.datasetId || ""}`.trim();
  }, []);

  const navigateToKnowledgeDetail = useCallback(
    (record: DataSourceItem) => {
      const datasetId = resolveCloudArchiveDatasetId(record);
      if (!datasetId) {
        message.warning(t("knowledge.noData"));
        return;
      }
      navigate({
        pathname: `/lib/knowledge/detail/${datasetId}`,
      });
    },
    [navigate, resolveCloudArchiveDatasetId, t],
  );

  const handleCloudArchiveEdit = useCallback(
    async (record: DataSourceItem) => {
      const sourceId = `${record.id || ""}`.trim();
      if (!sourceId) {
        message.warning(t("admin.dataSourceDetailNotFound"));
        return;
      }

      try {
        const [detailResponse, summaryResponse] = await Promise.all([
          dataSourceScanApi.getSource({ sourceId }),
          dataSourceScanApi.getSourceSummary({ sourceId }).catch(() => null),
        ]);
        const detailSource = {
          ...record,
          ...detailResponse.data.source,
          summary: summaryResponse?.data,
        } as ScanV2Source;
        const bindings = (detailResponse.data.bindings || []) as ScanV2Binding[];
        const mappedRecord = mapScanSourceToDataSource(
          detailSource,
          t,
          record,
          getFirstScanBinding(bindings),
          bindings,
        );
        syncCreateVm.openEditWizard(mappedRecord);
      } catch {
        // API errors are reported by the shared request interceptor.
      }
    },
    [syncCreateVm, t],
  );

  const handleCloudArchiveDelete = useCallback(
    (record: DataSourceItem) => {
      const datasetId = resolveCloudArchiveDatasetId(record);
      if (!datasetId) {
        message.warning(t("knowledge.noData"));
        return;
      }

      const knowledgeName = record.knowledgeBase || record.name || datasetId;
      confirmRef.current?.onOpen({
        id: datasetId,
        title: t("knowledge.deleteTitle", { name: knowledgeName }),
        content: t("knowledge.deleteContent"),
        confirmText: t("knowledge.deleteConfirmText", {
          name: knowledgeName,
        }),
      });
    },
    [resolveCloudArchiveDatasetId, t],
  );

  const cloudSourceColumns = useMemo<ColumnsType<DataSourceItem>>(
    () => [
      {
        title: t("knowledge.knowledgeBaseName"),
        dataIndex: "name",
        key: "name",
        width: 260,
        render: (_value, record) => (
          <div className="data-source-table-name">
            <span className={`data-source-icon data-source-icon-${record.type}`}>
              {sourceTypeOptions.find((item) => item.type === record.type)?.icon}
            </span>
            <div className="data-source-table-copy">
              <Button
                type="link"
                className="data-source-link-button"
                onClick={() => navigateToKnowledgeDetail(record)}
              >
                {record.name}
              </Button>
              <Tooltip title={record.description} placement="topLeft">
                <Text type="secondary" className="data-source-ellipsis" tabIndex={0}>
                  {record.description}
                </Text>
              </Tooltip>
            </div>
          </div>
        ),
      },
      {
        title: t("admin.dataSourceTableType"),
        dataIndex: "type",
        key: "type",
        width: 150,
        render: (type: SourceType) => (
          <Tag className="data-source-type-tag">{getSourceTypeTitle(type, t)}</Tag>
        ),
      },
      {
        title: t("admin.dataSourceTableSyncStrategy"),
        key: "syncMode",
        width: 190,
        render: (_value, record) => (
          <div className="data-source-sync-cell">
            <Text strong>{getSyncModeLabel(record.syncMode, t)}</Text>
            <Text type="secondary">{record.scheduleLabel}</Text>
          </div>
        ),
      },
      {
        title: t("admin.dataSourceTableConnectionStatus"),
        key: "status",
        width: 110,
        render: (_value, record) => {
          const statusMeta = getStatusMeta(record.status, t);
          const connectionMeta = getConnectionMeta(record.connectionState, t);
          return (
            <Space direction="vertical" size={4}>
              <Tag color={statusMeta.color}>{statusMeta.text}</Tag>
              <Tag color={connectionMeta.color}>{connectionMeta.text}</Tag>
            </Space>
          );
        },
      },
      {
        title: t("admin.dataSourceTableLastSync"),
        key: "lastSync",
        width: 180,
        render: (_value, record) => (
          <div className="data-source-sync-cell">
            <Text>{record.lastSync}</Text>
            <Text type="secondary">{record.nextSync}</Text>
          </div>
        ),
      },
      {
        title: t("common.actions"),
        key: "actions",
        width: 140,
        fixed: "right",
        className: "data-source-action-column",
        render: (_value, record) => (
          <Flex gap={10} wrap align="center">
            <Button
              className="link-btn"
              type="link"
              onClick={() => {
                void handleCloudArchiveEdit(record);
              }}
            >
              {t("common.edit")}
            </Button>
            <Button
              className="link-btn"
              type="link"
              danger
              onClick={() => handleCloudArchiveDelete(record)}
            >
              {t("common.delete")}
            </Button>
          </Flex>
        ),
      },
    ],
    [handleCloudArchiveDelete, handleCloudArchiveEdit, navigateToKnowledgeDetail, t],
  );

  const setOfficialItemStatus = useCallback(
    (item: OfficialKnowledgeBase, next: Partial<KnowledgeSquareStatusMap[string]>) => {
      setOfficialStatus((current) => ({
        ...current,
        [item.id]: {
          installed: current[item.id]?.installed ?? item.installed,
          updateAvailable:
            current[item.id]?.updateAvailable ?? Boolean(item.updateAvailable),
          ...next,
        },
      }));
    },
    [],
  );

  const handleOfficialInstall = useCallback(
    (item: OfficialKnowledgeBase) => {
      setOfficialItemStatus(item, { installed: true, updateAvailable: false });
      message.success(t("knowledge.installSuccess", { name: item.name }));
    },
    [setOfficialItemStatus, t],
  );

  const handleOfficialUpdate = useCallback(
    (item: OfficialKnowledgeBase) => {
      setOfficialItemStatus(item, { installed: true, updateAvailable: false });
      message.success(t("knowledge.updateSuccess", { name: item.name }));
    },
    [setOfficialItemStatus, t],
  );

  const handleOfficialOpen = useCallback(() => {
    setActiveView("mine");
    setSourceCategory("official");
  }, []);

  const handleOfficialQuery = useCallback(
    (item: OfficialKnowledgeBase) => {
      navigate({
        pathname: "/agent/chat/home",
        search: `?officialKnowledge=${encodeURIComponent(item.id)}`,
      });
    },
    [navigate],
  );

  const installedOfficialItems = useMemo(() => {
    const items = OFFICIAL_KNOWLEDGE_BASES.filter(
      (item) => {
        if (!officialStatus[item.id]?.installed) return false;
        const keyword = officialSearch.trim().toLocaleLowerCase();
        if (!keyword) return true;
        return [item.name, item.desc, item.domain, ...item.tags]
          .join(" ")
          .toLocaleLowerCase()
          .includes(keyword);
      },
    );
    if (mineSort === "name") {
      return [...items].sort((a, b) => a.name.localeCompare(b.name, "zh-CN"));
    }
    if (mineSort === "docs") {
      return [...items].sort((a, b) => b.docs - a.docs);
    }
    return [...items].sort((a, b) => b.updated.localeCompare(a.updated));
  }, [mineSort, officialSearch, officialStatus]);

  const handleUpdateAllOfficial = useCallback(() => {
    const updateItems = OFFICIAL_KNOWLEDGE_BASES.filter(
      (item) => officialStatus[item.id]?.installed && officialStatus[item.id]?.updateAvailable,
    );
    if (updateItems.length === 0) {
      message.info(t("knowledge.noUpdates"));
      return;
    }
    setOfficialStatus((current) => {
      const next = { ...current };
      updateItems.forEach((item) => {
        next[item.id] = { installed: true, updateAvailable: false };
      });
      return next;
    });
    message.success(t("knowledge.updateAllSuccess", { count: updateItems.length }));
  }, [officialStatus, t]);

  const columns: ColumnsType<Dataset> = [
    {
      title: t("knowledge.nameDescription"),
      dataIndex: "display_name",
      width: 300,
      render: (name: string, data: Dataset) => {
        return (
          <div className="knowledge-list-name-cell">
            <span className="knowledge-list-name-icon"><DatabaseOutlined /></span>
            <span className="knowledge-list-name-copy">
              <Button
                className="knowledge-list-name-button"
                type="link"
                onClick={() => {
                  navigate({ pathname: `/lib/knowledge/detail/${data.dataset_id}` });
                }}
              >
                <Tooltip title={name}><span>{name}</span></Tooltip>
              </Button>
              <Tooltip title={data.desc} placement="topLeft">
                <span className="knowledge-list-description">{data.desc || "-"}</span>
              </Tooltip>
            </span>
          </div>
        );
      },
    },
    {
      title: t("knowledge.source"),
      key: "source",
      width: 132,
      render: () => <span className="knowledge-list-source">{t("knowledge.localUpload")}</span>,
    },
    {
      title: t("knowledge.tags"),
      dataIndex: "tags",
      width: 170,
      render: (knowledgeBaseTags: string[]) => {
        return (
          <div className="knowledge-list-tags">
            {(knowledgeBaseTags || []).slice(0, 2).map((tag, index) => (
              <KnowledgeTag key={`${tag}-${index}`} title={tag} checkable={false} />
            ))}
            {!knowledgeBaseTags?.length ? <span>-</span> : null}
          </div>
        );
      },
    },
    {
      title: t("knowledge.parseSize"),
      dataIndex: "document_size",
      width: 100,
      render: (document_size: string) => {
        return FileUtils.formatFileSize(document_size);
      },
    },
    {
      title: t("knowledge.documentCountLabel"),
      dataIndex: "document_count",
      width: 88,
      render: (count: number) => t("knowledge.documentCount", { count: count || 0 }),
    },
    {
      title: t("knowledge.updateDate"),
      dataIndex: "update_time",
      width: 116,
      render: (time: string) => (time ? moment(time).format("YYYY-MM-DD") : "-"),
    },
    {
      title: t("knowledge.status"),
      key: "status",
      width: 116,
      render: () => (
        <span className="knowledge-list-status is-ready"><i />{t("knowledge.available")}</span>
      ),
    },
    {
      title: t("common.actions"),
      key: "action",
      width: 190,
      fixed: "right",
      render: (data: Dataset) => {
        if (!data.acl?.includes(DatasetAclEnum.DatasetWrite)) {
          return null;
        }
        return (
          <Flex gap={10} wrap align="center">
            <Button
              className="link-btn"
              type="link"
              onClick={(event: MouseEvent<HTMLElement>) => {
                event.stopPropagation();
                createUpdateRef.current?.onOpen(data);
              }}
            >
              {t("common.edit")}
            </Button>
            {!runtimeFeatures.hideUserGroupSurfaces && (
                <Button
                  className="link-btn"
                  type="link"
                  onClick={(event: MouseEvent<HTMLElement>) => {
                    event.stopPropagation();
                    navigate({
                      pathname: `/lib/knowledge/auth/${data.dataset_id}`,
                    });
                  }}
                >
                {t("knowledge.authorize")}
              </Button>
            )}
            <Button
              className="link-btn"
              type="link"
              danger
              onClick={(event: MouseEvent<HTMLElement>) => {
                event.stopPropagation();
                const knowledgeName =
                  data.display_name || data.dataset_id || "";
                confirmRef.current?.onOpen({
                  id: data.dataset_id || "",
                  title: t("knowledge.deleteTitle", { name: knowledgeName }),
                  content: t("knowledge.deleteContent"),
                  confirmText: t("knowledge.deleteConfirmText", {
                    name: knowledgeName,
                  }),
                });
              }}
            >
              {t("common.delete")}
            </Button>
          </Flex>
        );
      },
    },
  ];

  const officialColumns = useMemo<ColumnsType<OfficialKnowledgeBase>>(
    () => [
      {
        title: t("knowledge.nameDescription"),
        dataIndex: "name",
        width: 300,
        render: (name, item) => (
          <div className="knowledge-list-name-cell">
            <span className="knowledge-list-name-icon is-official"><AppstoreOutlined /></span>
            <span className="knowledge-list-name-copy">
              <Button
                className="knowledge-list-name-button"
                type="link"
                onClick={() => handleOfficialOpen()}
              >
                <Tooltip title={name}><span>{name}</span></Tooltip>
              </Button>
              <Tooltip title={item.desc} placement="topLeft">
                <span className="knowledge-list-description">{item.desc}</span>
              </Tooltip>
            </span>
          </div>
        ),
      },
      {
        title: t("knowledge.source"),
        key: "source",
        width: 132,
        render: () => <span className="knowledge-list-source is-official">{t("knowledge.officialKnowledge")}</span>,
      },
      {
        title: t("knowledge.tags"),
        dataIndex: "tags",
        width: 170,
        render: (itemTags: string[]) => (
          <div className="knowledge-list-tags">
            {itemTags.slice(0, 2).map((tag) => <span key={tag}>{tag}</span>)}
          </div>
        ),
      },
      {
        title: t("knowledge.parseSize"),
        dataIndex: "size",
        width: 100,
      },
      {
        title: t("knowledge.documentCountLabel"),
        dataIndex: "docs",
        width: 88,
        render: (count: number) => t("knowledge.documentCount", { count }),
      },
      {
        title: t("knowledge.updateDate"),
        dataIndex: "updated",
        width: 116,
      },
      {
        title: t("knowledge.status"),
        key: "status",
        width: 116,
        render: (_, item) => (
          <span className={`knowledge-list-status ${officialStatus[item.id]?.updateAvailable ? "is-update" : "is-ready"}`}>
            <i />
            {officialStatus[item.id]?.updateAvailable
              ? t("knowledge.updateAvailable")
              : t("knowledge.available")}
          </span>
        ),
      },
      {
        title: t("common.actions"),
        key: "actions",
        width: 220,
        fixed: "right",
        render: (_, item) => (
          <Flex gap={10} align="center">
            <Button className="link-btn" type="link" onClick={() => handleOfficialQuery(item)}>
              {t("knowledge.onlineQuery")}
            </Button>
            <Button
              className="link-btn"
              type="link"
              onClick={() =>
                officialStatus[item.id]?.updateAvailable
                  ? handleOfficialUpdate(item)
                  : handleOfficialOpen()
              }
            >
              {officialStatus[item.id]?.updateAvailable ? t("common.update") : t("common.open")}
            </Button>
            <Button
              className="link-btn"
              type="link"
              danger
              onClick={() => {
                setOfficialItemStatus(item, { installed: false, updateAvailable: false });
                message.success(t("knowledge.uninstallSuccess", { name: item.name }));
              }}
            >
              {t("knowledge.uninstall")}
            </Button>
          </Flex>
        ),
      },
    ],
    [
      handleOfficialOpen,
      handleOfficialQuery,
      handleOfficialUpdate,
      officialStatus,
      setOfficialItemStatus,
      t,
    ],
  );

  function getTags() {
    KnowledgeBaseServiceApi()
      .datasetServiceAllDatasetTags()
      .then((res) => {
        const uniqueTags = Array.from(
          new Set((res.data.tags || []).filter(Boolean)),
        );
        setTags([ALL_TAGS, ...uniqueTags.filter((tag) => tag !== ALL_TAGS)]);
      })
      .catch(() => {
        setTags([ALL_TAGS]);
      });
  }

  const handleSuccess = (
    data: Dataset[],
    total: number,
    newPagination: TablePaginationConfig,
  ) => {
    setDataSource(data);
    setPagination({
      ...newPagination,
      total,
    });
  };

  const initData = () => {
    setDataSource([]);
    setCloudSources([]);
    setPagination({
      current: 1,
      pageSize: 10,
      total: 0,
    });
  };

  function getTableData(page = 1, pageSize = pagination.pageSize) {
    const values = form.getFieldsValue();

    if (sourceCategory === "official") {
      setLoading(false);
      return;
    }

    if (sourceCategory === "cloudArchive") {
      void loadCloudSources(page, pageSize || 10, values.keyword || "");
      return;
    }

    const newPagination = {
      ...pagination,
      current: page,
      pageSize: pageSize,
    };
    setPagination(newPagination);

    const pageToken = UIUtils.generatePageToken({
      page: page - 1,
      pageSize: pageSize || 10,
      total: pagination.total || 0,
    });

    setLoading(true);

    KnowledgeBaseServiceApi()
      .datasetServiceListDatasets({
        pageToken,
        pageSize: pageSize,
        keyword: values.keyword,
        tags: values?.tags && values.tags !== ALL_TAGS ? [values.tags] : [],
      }, {
        params: { source: "manual" },
      })
      .then((res) => {
        handleSuccess(
          (res.data.datasets as unknown as Dataset[]) || [],
          res.data.total_size || 0,
          newPagination,
        );
      })
      .catch(() => {
        initData();
      })
      .finally(() => {
        setLoading(false);
      });
  }

  function onDelete(id: string) {
    KnowledgeBaseServiceApi()
      .datasetServiceDeleteDataset({ dataset: id })
      .then(() => {
        message.success(t("knowledge.deleteSuccess"));
        getTags();
        getTableData();
      });
  }

  function onUpdate(data: Dataset): Promise<void> {
    setLoading(true);
    try {
      if (data.dataset_id) {
        return KnowledgeBaseServiceApi()
          .datasetServiceUpdateDataset({
            dataset: data.dataset_id,
            dataset2: data,
          })
          .then(() => {
            message.success(t("knowledge.editSuccess"));
            getTags();
            getTableData();
          });
      }
      return KnowledgeBaseServiceApi()
        .datasetServiceCreateDataset({
          dataset: data,
        })
        .then(() => {
          message.success(
            data.dataset_id
              ? t("knowledge.editSuccess")
              : t("knowledge.createSuccess"),
          );
          getTags();
          getTableData();
        });
    } finally {
      setLoading(false);
    }
  }
  function onTableChange(newPagination: TablePaginationConfig) {
    setPagination({
      current: newPagination.current,
      pageSize: newPagination.pageSize,
    });

    getTableData(newPagination.current, newPagination.pageSize);
  }

  const officialUpdateCount = OFFICIAL_KNOWLEDGE_BASES.filter(
    (item) => officialStatus[item.id]?.installed && officialStatus[item.id]?.updateAvailable,
  ).length;

  return (
    <div className="knowledge-list-page">
      <div className="knowledge-page-header">
        <div>
          <h1>{t("layout.knowledgeBase")}</h1>
          <p>{t("knowledge.pageDescription")}</p>
        </div>
        <div className="knowledge-page-header-actions">
          {activeView === "mine" ? (
            <Tooltip title={createActionDisabled ? createActionDisabledTooltip : undefined}>
              <span>
                <Button
                  type="primary"
                  icon={<PlusOutlined />}
                  disabled={createActionDisabled}
                  onClick={() => createKnowledgeRef.current?.onOpen()}
                >
                  {t("knowledge.createKnowledgeBase")}
                </Button>
              </span>
            </Tooltip>
          ) : null}
          <Button icon={<HistoryOutlined />} onClick={() => navigate(taskCenterPath)}>
            {t("knowledge.backgroundTasks")}
          </Button>
        </div>
      </div>

      {embeddingReady === false ? (
        <Alert
          banner
          className="knowledge-embedding-warning"
          message={
            isAdmin ? (
              <span>
                {t("knowledge.embeddingNotReadyBannerAdmin")}
                <a
                  href={modelSettingsPath}
                  style={{ marginLeft: 8, fontWeight: 500 }}
                  onClick={(e: MouseEvent<HTMLAnchorElement>) => {
                    e.preventDefault();
                    navigate(modelSettingsPath);
                  }}
                >
                  {t("knowledge.goToConfig")}
                </a>
              </span>
            ) : (
              t("knowledge.embeddingNotReadyBanner")
            )
          }
          showIcon
          type="warning"
        />
      ) : null}
      {multimodalEmbeddingReady === false ? (
        <Alert
          banner
          className="knowledge-embedding-warning"
          message={
            isAdmin ? (
              <span>
                {t("knowledge.multimodalEmbeddingNotReadyBannerAdmin")}
                <a
                  href={modelSettingsPath}
                  style={{ marginLeft: 8, fontWeight: 500 }}
                  onClick={(e: MouseEvent<HTMLAnchorElement>) => {
                    e.preventDefault();
                    navigate(modelSettingsPath);
                  }}
                >
                  {t("knowledge.goToConfig")}
                </a>
              </span>
            ) : (
              t("knowledge.multimodalEmbeddingNotReadyBanner")
            )
          }
          showIcon
          type="warning"
        />
      ) : null}

      <div className="knowledge-view-tabs" role="tablist" aria-label={t("knowledge.viewTabs")}>
        <button
          type="button"
          role="tab"
          className={activeView === "mine" ? "is-active" : ""}
          aria-selected={activeView === "mine"}
          onClick={() => setActiveView("mine")}
        >
          <span className="knowledge-view-tab-icon"><DatabaseOutlined /></span>
          <span><strong>{t("knowledge.myKnowledge")}</strong><small>{t("knowledge.myKnowledgeDescription")}</small></span>
        </button>
        <button
          type="button"
          role="tab"
          className={activeView === "square" ? "is-active" : ""}
          aria-selected={activeView === "square"}
          onClick={() => setActiveView("square")}
        >
          <span className="knowledge-view-tab-icon"><AppstoreOutlined /></span>
          <span><strong>{t("knowledge.knowledgeSquare")}</strong><small>{t("knowledge.knowledgeSquareDescription")}</small></span>
        </button>
      </div>

      {activeView === "square" ? (
        <KnowledgeSquare
          statusMap={officialStatus}
          onInstall={handleOfficialInstall}
          onUpdate={handleOfficialUpdate}
          onOpen={handleOfficialOpen}
          onQuery={handleOfficialQuery}
        />
      ) : (
        <div className="knowledge-mine-view">
          <div className="knowledge-source-tabs" role="tablist" aria-label={t("knowledge.sourceCategory")}>
            {([
              ["local", t("knowledge.localUpload")],
              ["cloudArchive", t("knowledge.cloudArchiveCreated")],
              ["official", t("knowledge.installedOfficialKnowledge")],
            ] as Array<[SourceCategory, string]>).map(([value, label]) => (
              <button
                key={value}
                type="button"
                role="tab"
                className={sourceCategory === value ? "is-active" : ""}
                aria-selected={sourceCategory === value}
                onClick={() => {
                  form.resetFields(["keyword", "tags"]);
                  form.setFieldsValue({ tags: ALL_TAGS });
                  setOfficialSearch("");
                  setSourceCategory(value);
                  if (value !== "official") initData();
                }}
              >
                {label}
              </button>
            ))}
          </div>

          <Form
            className={`knowledge-mine-toolbar ${isOfficialView ? "has-update-all" : ""}`}
            form={form}
          >
            <Form.Item name="keyword" noStyle>
              <Input.Search
                allowClear
                prefix={<SearchOutlined />}
                placeholder={
                  isCloudArchiveView
                    ? t("admin.dataSourceAssetSearchPlaceholder")
                    : t("knowledge.squareSearchPlaceholder")
                }
                onChange={(event: ChangeEvent<HTMLInputElement>) => {
                  if (isOfficialView) setOfficialSearch(event.target.value);
                }}
                onSearch={(value: string) => {
                  form.setFieldsValue({ keyword: value ?? "" });
                  if (isOfficialView) setOfficialSearch(value || "");
                  else getTableData();
                }}
              />
            </Form.Item>
            {isOfficialView ? (
              <Button type="primary" onClick={handleUpdateAllOfficial}>
                {t("knowledge.updateAll")}
                {officialUpdateCount > 0 ? ` (${officialUpdateCount})` : ""}
              </Button>
            ) : null}
            {sourceCategory === "local" ? (
              <Form.Item name="tags" noStyle initialValue={ALL_TAGS}>
                <Select
                  suffixIcon={<DownOutlined />}
                  options={tags.map((tag) => ({
                    label: tag === ALL_TAGS ? t("knowledge.filterAndSort") : tag,
                    value: tag,
                  }))}
                  onChange={() => getTableData()}
                />
              </Form.Item>
            ) : (
              <Select
                value={mineSort}
                suffixIcon={<DownOutlined />}
                options={[
                  { value: "updated", label: t("knowledge.sortByUpdated") },
                  { value: "name", label: t("knowledge.sortByName") },
                  { value: "docs", label: t("knowledge.sortByDocumentCount") },
                ]}
                onChange={setMineSort}
              />
            )}
            <Button
              onClick={() => {
                form.resetFields(["keyword", "tags"]);
                form.setFieldsValue({ tags: ALL_TAGS });
                setOfficialSearch("");
                setMineSort("updated");
                if (!isOfficialView) getTableData(1, pagination.pageSize);
              }}
            >
              {t("common.reset")}
            </Button>
          </Form>

          <ListPageTable
            rootClassName={`knowledge-mine-table ${isCloudArchiveView ? "data-source-asset-table" : ""}`}
            rowKey={isCloudArchiveView || isOfficialView ? "id" : "dataset_id"}
            columns={
              (isCloudArchiveView
                ? cloudSourceColumns
                : isOfficialView
                  ? officialColumns
                  : columns) as ColumnsType<any>
            }
            loading={isOfficialView ? false : loading}
            dataSource={
              isCloudArchiveView
                ? cloudSources
                : isOfficialView
                  ? installedOfficialItems
                  : dataSource
            }
            expandable={{ showExpandColumn: false }}
            pagination={
              isOfficialView
                ? false
                : {
                    ...pagination,
                    showSizeChanger: true,
                    showTotal: (total: number) => t("common.totalItems", { total }),
                  }
            }
            onChange={isOfficialView ? undefined : onTableChange}
            onRow={(record: any) => ({
              onClick: () => {
                if (isOfficialView) {
                  handleOfficialOpen();
                  return;
                }
                if (!isCloudArchiveView && "dataset_id" in record && record.dataset_id) {
                  navigate(`/lib/knowledge/detail/${record.dataset_id}`);
                }
              },
            })}
            scroll={{ x: isCloudArchiveView ? 1240 : 1200 }}
          />
        </div>
      )}

      <TypedConfirmModal ref={confirmRef} onClick={onDelete} />

      <CreateUpdateModal ref={createUpdateRef} onUpdate={onUpdate} />
      <CreateKnowledgeBaseModal
        ref={createKnowledgeRef}
        syncCreateVm={syncCreateVm}
        onCreate={onUpdate}
      />
      <SyncKnowledgeBaseCreationFlow vm={syncCreateVm} hideProviderModal />
    </div>
  );
};

export default KnowledgePage;
