import { FC, useState, useEffect, useRef, MouseEvent } from "react";
import {
  Alert,
  Button,
  Form,
  Tooltip,
  Flex,
  message,
  TablePaginationConfig,
  Select,
  Tag,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useNavigate } from "react-router-dom";
import moment from "moment";
import { EditFilled } from "@ant-design/icons";

import ListPageHeader from "@/modules/knowledge/components/ListPageHeader";
import TypedConfirmModal, {
  type TypedConfirmModalRef,
} from '@/components/ui/TypedConfirmModal';
import CreateUpdateModal, {
  UpdateImperativeProps,
} from "@/modules/knowledge/components/UpdateModal";
import UIUtils from "@/modules/knowledge/utils/ui";
import {
  DocumentServiceApi,
  KnowledgeBaseServiceApi,
} from "@/modules/knowledge/utils/request";
import { ALL_TAGS, TIME_FORMAT } from "@/modules/knowledge/constants/common";
import {
  Dataset,
  DatasetAclEnum,
  DocDocumentStageEnum,
  DocTypeEnum,
} from "@/api/generated/knowledge-client";
import KnowledgeTag from "@/modules/knowledge/components/KnowledgeTag";
import FileUtils from "@/modules/knowledge/utils/file";
import { isDocumentDetailUnsupported } from "@/modules/knowledge/utils/document";

import { ListPageTable } from "@/components/ui";
import EditTags from "@/modules/knowledge/pages/detail/components/KnowledgeTable/editTags";
import type { TreeNode } from "@/modules/knowledge/pages/detail/components/KnowledgeTable";
import { useTranslation } from "react-i18next";
import { axiosInstance, BASE_URL } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import {
  fetchModelFeatures,
  isImageEmbedRequired,
  MODEL_FEATURES_CHANGED_EVENT,
} from "@/hooks/useModelFeatures";

import "./index.scss";

type DocRow = {
  dataset_id?: string;
  document_id?: string;
  display_name?: string;
  rel_path?: string;
  document_stage?: string;
  type?: string;
  document_size?: number | string;
  update_time?: string;
  creator?: string;
  uri?: string;
  data_source_type?: string;
  tags?: string[];
  p_id?: string;
};

const KnowledgePage: FC = () => {
  const [form] = Form.useForm();
  const navigate = useNavigate();
  const { t } = useTranslation();

  const DocumentStageEnum = {
    WAITING: t("knowledge.stageParsing"),
    WORKING: t("knowledge.stageParsing"),
    SUCCESS: t("knowledge.stageParsed"),
    FAILED: t("knowledge.stageFailed"),
    CANCELED: t("knowledge.stageCanceled"),
    DELETING: t("knowledge.stageDeleting"),
    DELETED: t("knowledge.stageDeleted"),

    [DocDocumentStageEnum.DocumentUploaded]: t("knowledge.stageUploaded"),
    [DocDocumentStageEnum.DocumentQueued]: t("knowledge.stageParsing"),
    [DocDocumentStageEnum.DocumentParsing]: t("knowledge.stageParsing"),
    [DocDocumentStageEnum.DocumentParseSuccessfully]: t("knowledge.stageParsed"),
    [DocDocumentStageEnum.DocumentParsingFailed]: t("knowledge.stageFailed"),
    [DocDocumentStageEnum.DocumentParsingCancelled]: t("knowledge.stageCanceled"),
  };

  const confirmRef = useRef<TypedConfirmModalRef>(null);
  const createUpdateRef = useRef<UpdateImperativeProps>(null);

  const [loading, setLoading] = useState(false);
  const [pagination, setPagination] = useState<TablePaginationConfig>({
    current: 1,
    pageSize: 10,
    total: 0,
  });
  const [dataSource, setDataSource] = useState<Dataset[] | undefined>([]);
  // Keep a local default option to avoid label flicker while tags are loading.
  const [tags, setTags] = useState<string[]>([ALL_TAGS]);
  const [knowledgeType, setKnowledgeType] = useState<string>("knowledgeBase");
  const [showTagEditModal, setShowTagEditModal] = useState(false);
  const [tagEditRecord, setTagEditRecord] = useState<DocRow | null>(null);
  const [embeddingReady, setEmbeddingReady] = useState<boolean | null>(null);
  const [multimodalEmbeddingReady, setMultimodalEmbeddingReady] = useState<boolean | null>(null);
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === 'system-admin';

  useEffect(() => {
    getTags();
    getTableData();
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
      window.removeEventListener(MODEL_FEATURES_CHANGED_EVENT, onFeaturesChanged);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, []);

  async function checkEmbeddingReady() {
    try {
      const features = await fetchModelFeatures(true);
      const imageEmbedRequired = isImageEmbedRequired(features);

      const [embResp, multiResp] = await Promise.all([
        axiosInstance.get<{ data?: { ready: boolean } } | { ready: boolean }>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_main`
        ).catch(() => null),
        imageEmbedRequired
          ? axiosInstance.get<{ data?: { ready: boolean } } | { ready: boolean }>(
              `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_image`
            ).catch(() => null)
          : Promise.resolve(null),
      ]);
      const unwrap = (resp: typeof embResp): boolean | null => {
        if (!resp) return null;
        const d = resp.data && typeof resp.data === 'object' && 'data' in resp.data
          ? (resp.data as { data?: { ready: boolean } }).data
          : resp.data as { ready: boolean };
        return d?.ready ?? null;
      };
      setEmbeddingReady(unwrap(embResp));
      // null means "not applicable" — does not trigger disabled state.
      setMultimodalEmbeddingReady(imageEmbedRequired ? unwrap(multiResp) : null);
    } catch {
      setEmbeddingReady(null);
      setMultimodalEmbeddingReady(null);
    }
  }

  useEffect(() => {
    if (knowledgeType) {
      getTableData(1, pagination.pageSize);
    }
  }, [knowledgeType]);
  const handleOpenTagEdit = (record: DocRow) => {
    setTagEditRecord(record);
    setShowTagEditModal(true);
  };
  const handleCloseTagEdit = () => {
    setShowTagEditModal(false);
    setTagEditRecord(null);
  };
  const handleTagEditSuccess = () => {
    getTableData(pagination.current, pagination.pageSize);
  };

  const columns: ColumnsType<Dataset> = [
    {
      title: t("knowledge.nameId"),
      dataIndex: "display_name",
      width: 350,
      render: (name: string, data: Dataset) => {
        return (
          <Flex vertical align={"flex-start"}>
            <Button
              className="link-btn"
              type="link"
              style={{ maxWidth: "100%" }}
              onClick={() => {
                navigate({
                  pathname: `/lib/knowledge/detail/${data.dataset_id}`,
                });
              }}
            >
              <Tooltip title={name}>
                <span className="text-ellipsis">{name}</span>
              </Tooltip>
            </Button>
            <Tooltip title={data.dataset_id}>
              <span
                className="text-ellipsis"
                style={{ color: "var(--color-text-description)" }}
              >
                {data.dataset_id}
              </span>
            </Tooltip>
          </Flex>
        );
      },
    },
    {
      title: t("common.description"),
      dataIndex: "desc",
      ellipsis: {
        showTitle: false,
      },
      width: 200,
      render: (desc: string) => (
        <Tooltip placement="topLeft" title={desc}>
          <span>{desc}</span>
        </Tooltip>
      ),
    },
    {
      title: t("knowledge.tags"),
      dataIndex: "tags",
      width: 180,
      render: (knowledgeBaseTags: string[]) => {
        return (
          <Flex style={{ overflowX: "auto", padding: "13px 0" }}>
            {knowledgeBaseTags.map((tag, index) => {
              return <KnowledgeTag key={index} title={tag} checkable={false} />;
            })}
          </Flex>
        );
      },
    },
    {
      title: t("knowledge.updateDate"),
      dataIndex: "update_time",
      width: 180,
      render: (time: string) => {
        return moment(time).format("YYYY-MM-DD HH:mm:ss");
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
      title: t("knowledge.fileCount"),
      dataIndex: "document_count",
      width: 100,
    },
    {
      title: t("common.actions"),
      key: "action",
      width: 160,
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
              onClick={() => {
                createUpdateRef.current?.onOpen(data);
              }}
            >
              {t("common.edit")}
            </Button>
            <Button
              className="link-btn"
              type="link"
              onClick={() =>
                navigate({
                  pathname: `/lib/knowledge/auth/${data.dataset_id}`,
                })
              }
            >
              {t("knowledge.authorize")}
            </Button>
            <Button
              className="link-btn"
              type="link"
              danger
              onClick={() => {
                const knowledgeName = data.display_name || data.dataset_id || "";
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

  const knowledgeColumns: ColumnsType<DocRow> = [
    {
      title: t("knowledge.docName"),
      dataIndex: "display_name",
      width: 350,
      render: (name: string, record) => {
        return (
          <Flex vertical align={"flex-start"}>
            <Button
              className="link-btn"
              type="link"
              style={{ maxWidth: "100%" }}
              onClick={() => {
                const documentId = record?.document_id;
                const datasetId = record?.dataset_id;
                const relPathtype = record?.type;
                if (relPathtype === "FOLDER") {
                  navigate({ pathname: `/lib/knowledge/detail/${datasetId}` });
                } else {
                  if (isDocumentDetailUnsupported(record?.display_name)) {
                    message.info(t("knowledge.documentDetailUnsupported"));
                    return;
                  }
                  navigate({
                    pathname:
                      documentId && datasetId
                        ? `/lib/knowledge/knowledge/${datasetId}/${documentId}`
                        : `/lib/knowledge/detail/${datasetId}`,
                  });
                }
              }}
            >
              <Tooltip title={name}>
                <span className="text-ellipsis">{name}</span>
              </Tooltip>
            </Button>
          </Flex>
        );
      },
    },
    {
      title: t("knowledge.tags"),
      dataIndex: "tags",
      width: 120,
      render: (rowTags: string[] | undefined, record: DocRow) => {
        if (record.type === DocTypeEnum.Folder) {
          return <span>-</span>;
        }
        if (!rowTags || rowTags.length === 0) {
          return (
            <div style={{ display: "flex", alignItems: "center", gap: "4px" }}>
              <span>-</span>
              <Button
                type="text"
                size="small"
                icon={<EditFilled style={{ color: "#1890ff" }} />}
                onClick={(e) => {
                  e.stopPropagation();
                  handleOpenTagEdit(record);
                }}
                style={{ padding: 0, minWidth: "auto", height: "auto" }}
              />
            </div>
          );
        }
        return (
          <div style={{ display: "flex", alignItems: "center", gap: "4px" }}>
            <div
              style={{
                display: "flex",
                gap: "4px",
                overflowX: "auto",
                overflowY: "hidden",
                maxWidth: "100%",
                paddingBottom: "2px",
                WebkitOverflowScrolling: "touch",
                flex: 1,
              }}
              className="tags-scroll-container"
            >
              {rowTags.map((tag) => (
                <Tag
                  key={tag}
                  style={{ flexShrink: 0, margin: 0, whiteSpace: "nowrap" }}
                >
                  {tag}
                </Tag>
              ))}
            </div>
            <Button
              type="text"
              size="small"
              icon={<EditFilled style={{ color: "#1890ff" }} />}
              onClick={(e) => {
                e.stopPropagation();
                handleOpenTagEdit(record);
              }}
              style={{
                padding: 0,
                minWidth: "auto",
                height: "auto",
                flexShrink: 0,
              }}
            />
          </div>
        );
      },
    },
    {
      title: t("knowledge.directory"),
      dataIndex: "rel_path",
      width: 120,
      render: (rel_path: string) => {
        if (rel_path?.length) {
          const relArr = rel_path?.split("/");
          if (relArr?.[1]) {
            return relArr?.[0];
          }
          if (
            ["pdf", "docx", "doc", "pptx"].includes(
              rel_path?.split(".")?.at(-1) ?? "",
            )
          ) {
            return "/";
          }
          if (!relArr?.[1]?.length) {
            return "/";
          }
          return rel_path;
        }
        return "/";
      },
    },
    {
      title: t("knowledge.parseStatus"),
      dataIndex: "document_stage",
      width: 120,
      render: (document_stage: string) => {
        return (
          DocumentStageEnum[document_stage as keyof typeof DocumentStageEnum] ||
          "-"
        );
      },
    },
    {
      title: t("knowledge.docType"),
      dataIndex: "type",
      width: 120,
      render: (type: string, record: DocRow) => {
        if (type === DocTypeEnum.Folder) {
          return t("knowledge.folder");
        }
        return FileUtils.getSuffix(record.display_name || "") || t("knowledge.unknown");
      },
    },
    {
      title: t("knowledge.size"),
      dataIndex: "document_size",
      width: 120,
      render: (_: number, record: DocRow) => {
        return FileUtils.formatFileSize(record.document_size);
      },
    },
    {
      title: t("knowledge.updateDate"),
      dataIndex: "update_time",
      width: 180,
      render: (text: string) => moment(text).format(TIME_FORMAT),
    },
    {
      title: t("knowledge.updater"),
      dataIndex: "creator",
      width: 120,
    },
  ];

  function getTags() {
    KnowledgeBaseServiceApi()
      .datasetServiceAllDatasetTags()
      .then((res) => {
        const uniqueTags = Array.from(new Set((res.data.tags || []).filter(Boolean)));
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
    setPagination({
      current: 1,
      pageSize: 10,
      total: 0,
    });
  };

  function getTableData(page = 1, pageSize = pagination.pageSize) {
    const values = form.getFieldsValue();

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

    if (knowledgeType === "knowledgeBase") {
      KnowledgeBaseServiceApi()
        .datasetServiceListDatasets({
          pageToken,
          pageSize: pageSize,
          keyword: values.keyword,
          tags: values?.tags && values.tags !== ALL_TAGS ? [values.tags] : [],
        })
        .then((res) => {
          handleSuccess(
            res.data.datasets || [],
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
    } else {
      DocumentServiceApi()
        .documentServiceSearchAllDocuments({
          searchAllDocumentsRequest: {
            page_token: pageToken,
            page_size: pageSize,
            keyword: values.keyword || "",
          },
        })
        .then((res) => {
          handleSuccess(
            (res.data.documents as unknown as Dataset[]) || [],
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
          message.success(data.dataset_id ? t("knowledge.editSuccess") : t("knowledge.createSuccess"));
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

  return (
    <div className="knowledge-list-page">
      <h2 className="knowledge-title admin-page-title">{t("layout.knowledgeBase")}</h2>
      {embeddingReady === false ? (
        <Alert
          banner
          className="knowledge-embedding-warning"
          message={
            isAdmin ? (
              <span>
                {t("knowledge.embeddingNotReadyBannerAdmin")}
                <a
                  href="/model-providers"
                  style={{ marginLeft: 8, fontWeight: 500 }}
                  onClick={(e: MouseEvent<HTMLAnchorElement>) => { e.preventDefault(); navigate('/model-providers'); }}
                >
                  {t("knowledge.goToConfig")}
                </a>
              </span>
            ) : t("knowledge.embeddingNotReadyBanner")
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
                  href="/model-providers"
                  style={{ marginLeft: 8, fontWeight: 500 }}
                  onClick={(e: MouseEvent<HTMLAnchorElement>) => { e.preventDefault(); navigate('/model-providers'); }}
                >
                  {t("knowledge.goToConfig")}
                </a>
              </span>
            ) : t("knowledge.multimodalEmbeddingNotReadyBanner")
          }
          showIcon
          type="warning"
        />
      ) : null}
      <Form className="list-header" form={form}>
        <ListPageHeader
          placeholder={
            knowledgeType === "knowledgeBase"
              ? t("knowledge.searchPlaceholder")
              : t("knowledge.searchDocPlaceholder")
          }
          searchKey="keyword"
          btnText={t("knowledge.createKnowledgeBase")}
          btnDisabled={embeddingReady === false || multimodalEmbeddingReady === false}
          btnDisabledTooltip={
            isAdmin ? (
              <span>
                {embeddingReady === false
                  ? t("knowledge.embeddingNotReadyBannerAdmin")
                  : t("knowledge.multimodalEmbeddingNotReadyBannerAdmin")}
                <a
                  href="/model-providers"
                  style={{ marginLeft: 8, color: '#fff', textDecoration: 'underline' }}
                  onClick={(e: MouseEvent<HTMLAnchorElement>) => { e.preventDefault(); navigate('/model-providers'); }}
                >
                  {t("knowledge.goToConfig")}
                </a>
              </span>
            ) : (
              embeddingReady === false
                ? t("knowledge.embeddingNotReadyBanner")
                : t("knowledge.multimodalEmbeddingNotReadyBanner")
            )
          }
          onClick={() => {
            createUpdateRef.current?.onOpen();
          }}
          onSearch={() => {
            getTableData();
          }}
          extra={
            <>
              {knowledgeType === "knowledgeBase" && (
                <Form.Item
                  label={t("knowledge.tags")}
                  name="tags"
                  style={{ marginBottom: 0 }}
                  initialValue={ALL_TAGS}
                >
                  <Select
                    className="ghost-custom-border !w-[260px]"
                    options={tags.map((tag) => ({
                      label: tag === ALL_TAGS ? t("knowledge.allTags") : tag,
                      value: tag,
                    }))}
                    placeholder={t("knowledge.selectTag")}
                    allowClear
                    variant="borderless"
                    onChange={() => {
                      getTableData();
                    }}
                  />
                </Form.Item>
              )}
            </>
          }
          prefix={
            <Select
              className="ghost-custom-border !w-[100px]"
              options={[
                { key: "knowledgeBase", value: t("layout.knowledgeBase") },
                { key: "knowledge", value: t("knowledge.knowledge") },
              ].map(({ key, value }) => ({ label: value, value: key }))}
              variant="borderless"
              onChange={(key) => {
                form.resetFields(["keyword", "tags"]);
                initData();
                form.setFieldsValue({ tags: ALL_TAGS });
                setKnowledgeType(key);
              }}
              value={knowledgeType}
            />
          }
        />
      </Form>
      <ListPageTable
        rowKey={
          knowledgeType === "knowledgeBase" ? "dataset_id" : "document_id"
        }
        columns={
          (knowledgeType === "knowledgeBase" ? columns : knowledgeColumns) as ColumnsType<any>
        }
        loading={loading}
        dataSource={dataSource}
        expandable={{ showExpandColumn: false }}
        pagination={{
          ...pagination,
          showSizeChanger: true,
          showTotal: (total: number) => t("common.totalItems", { total }),
        }}
        onChange={onTableChange}
        scroll={{
          y: "calc(100vh - 260px)",
        }}
      />

      <TypedConfirmModal ref={confirmRef} onClick={onDelete} />

      <CreateUpdateModal ref={createUpdateRef} onUpdate={onUpdate} />
      <EditTags
        open={showTagEditModal}
        record={tagEditRecord as TreeNode | null}
        datasetId={tagEditRecord?.dataset_id ?? ""}
        onCancel={handleCloseTagEdit}
        onSuccess={handleTagEditSuccess}
      />
    </div>
  );
};

export default KnowledgePage;
