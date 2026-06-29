import {
  Alert,
  message,
  Button,
  Badge,
  Dropdown,
  Tooltip,
  Input,
  Tag,
  Space,
} from "antd";
import { axiosInstance, BASE_URL } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import {
  fetchModelFeatures,
  isImageEmbedRequired,
  MODEL_FEATURES_CHANGED_EVENT,
} from "@/hooks/useModelFeatures";
import type { MenuProps } from "antd";
import { useEffect, useRef, useState, useCallback, MouseEvent } from "react";
import { useParams } from "react-router-dom";
import {
  EditOutlined,
  SettingOutlined,
  DeleteOutlined,
  CopyOutlined,
  DownOutlined,
} from "@ant-design/icons";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Dataset,
  DatasetAclEnum,
  DocTypeEnum,
} from "@/api/generated/knowledge-client";

import Polling from "@/modules/knowledge/utils/polling";
import RenameModel, {
  RenameFormItem,
  RenameModalRef,
} from "@/modules/knowledge/components/RenameModel";
import KnowledgeTable, {
  IKnowledgeListRef,
  TreeNode,
} from "./components/KnowledgeTable";
import ImportKnowledgeModal, {
  IImportKnowledgeModalRef,
} from "./components/ImportKnowledgeModal";
import ImportTaskManage, {
  IImportTaskManageRef,
} from "./components/ImportTaskManage";
import TreeUtils from "@/modules/knowledge/utils/tree";
import { IMPORT_TASK_POLL_INTERVAL } from "@/modules/knowledge/constants/common";
import ConfirmModal, {
  ConfirmImperativeProps,
} from "@/modules/knowledge/components/ConfirmModal";
import CreateUpdateModal, {
  UpdateImperativeProps,
} from "@/modules/knowledge/components/UpdateModal";
import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";
import { DocumentServiceApi, TaskServiceApi } from "../../utils/request";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";

import { DetailPageHeader } from "@/components/ui";

import "./index.scss";

const { Search } = Input;

async function writeTextToClipboard(text: string) {
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.readOnly = true;
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.top = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();

  let copied = false;
  try {
    if (typeof document.execCommand === "function") {
      copied = document.execCommand("copy");
    }
  } finally {
    document.body.removeChild(textarea);
  }

  if (copied) {
    return;
  }

  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  throw new Error("Copy command failed");
}

const Detail = () => {
  const { t } = useTranslation();
  const knowledgeListRef = useRef<IKnowledgeListRef>(null);
  const createFolderRef = useRef<RenameModalRef>(null);
  const importKnowledgeRef = useRef<IImportKnowledgeModalRef>();
  const importTaskRef = useRef<IImportTaskManageRef>();
  const pollingRef = useRef(new Polling());
  const importingTaskListRef = useRef([]);
  const confirmRef = useRef<ConfirmImperativeProps>(null);
  const createUpdateRef = useRef<UpdateImperativeProps>(null);

  const [detail, setDetail] = useState<Dataset>();
  const [runningTotal, setRunningTotal] = useState(0);
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const [embeddingReady, setEmbeddingReady] = useState<boolean | null>(null);
  const [multimodalEmbeddingReady, setMultimodalEmbeddingReady] = useState<boolean | null>(null);
  const [uploadingNoticeVisible, setUploadingNoticeVisible] = useState(false);
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === 'system-admin';

  const { id = "" } = useParams();

  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const { setCurrentDataset, clearDataset } = useDatasetPermissionStore();

  const getDetail = useCallback(() => {
    KnowledgeBaseServiceApi()
      .datasetServiceGetDataset({ dataset: id })
      .then((res) => {
        const dataset = res.data as unknown as Dataset;
        setDetail(dataset);
        setCurrentDataset(dataset);
      });
  }, [id, setCurrentDataset]);

  useEffect(() => {
    console.log("searchParams", searchParams);
    getDetail();
    getImportingTotal();
    const unwrap = (resp: { data: { data?: { ready: boolean } } | { ready: boolean } } | null): boolean | null => {
      if (!resp) return null;
      const body = resp.data;
      const d = body && typeof body === "object" && "data" in body
        ? (body as { data?: { ready: boolean } }).data
        : (body as { ready: boolean });
      return d?.ready ?? null;
    };
    const loadEmbeddingReady = () => {
      fetchModelFeatures(true).then((features) => {
        const imageEmbedRequired = isImageEmbedRequired(features);
        return Promise.all([
          axiosInstance
            .get<{ data?: { ready: boolean } } | { ready: boolean }>(
              `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_main`
            )
            .catch(() => null),
          imageEmbedRequired
            ? axiosInstance
                .get<{ data?: { ready: boolean } } | { ready: boolean }>(
                  `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_image`
                )
                .catch(() => null)
            : Promise.resolve(null),
        ]).then(([embResp, multiResp]) => {
          setEmbeddingReady(unwrap(embResp));
          setMultimodalEmbeddingReady(imageEmbedRequired ? unwrap(multiResp) : null);
        });
      }).catch(() => {
        setEmbeddingReady(null);
        setMultimodalEmbeddingReady(null);
      });
    };
    loadEmbeddingReady();
    window.addEventListener(MODEL_FEATURES_CHANGED_EVENT, loadEmbeddingReady);
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        loadEmbeddingReady();
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);

    return () => {
      window.removeEventListener(MODEL_FEATURES_CHANGED_EVENT, loadEmbeddingReady);
      document.removeEventListener("visibilitychange", onVisibilityChange);
      pollingRef.current.cancel();
      clearDataset();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [getDetail, clearDataset]);

  useEffect(() => {
    const syncDeveloperActive = () => {
      setDeveloperActive(isDeveloperModeActive());
    };

    const handleDeveloperActiveChange = (event: Event) => {
      const nextActive = (event as CustomEvent<{ active?: boolean }>).detail?.active;
      setDeveloperActive(
        typeof nextActive === "boolean" ? nextActive : isDeveloperModeActive(),
      );
    };

    window.addEventListener("storage", syncDeveloperActive);
    window.addEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperActiveChange);

    return () => {
      window.removeEventListener("storage", syncDeveloperActive);
      window.removeEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperActiveChange);
    };
  }, []);

  function getImportingTotal() {
    pollingRef.current.cancel();
    pollingRef.current.start({
      interval: IMPORT_TASK_POLL_INTERVAL,
      // Filter to running tasks on the backend so total_size is accurate and
      // we are not limited by the default page size of 20.
      request: () => TaskServiceApi().listTasks(id, { taskStatus: 'running', pageSize: 1000 }),
      onSuccess: ({ data = {} }) => {
        const newTaskList = data.tasks || [];
        // Tasks in WORKING state are actively being parsed by the algorithm service.
        // Tasks in WAITING state are still uploading / queued before parsing starts.
        const uploadingTasks = newTaskList.filter((t: any) => t.task_state === 'WAITING');
        if (newTaskList.length === 0) {
          pollingRef.current.cancel();
        }
        compareTaskChange(newTaskList, importingTaskListRef.current);
        // Use total_size from the backend for an accurate count; fall back to
        // the length of the current page if total_size is absent.
        setRunningTotal(data.total_size ?? newTaskList.length);
        // Show notice only while files are still uploading; once upload is done
        // the user can safely close the tab even if parsing continues in the background.
        setUploadingNoticeVisible(uploadingTasks.length > 0);
        importingTaskListRef.current = newTaskList;
      },
    });
  }

  function compareTaskChange(newTaskList: any[], prevTaskList: any[]) {
    const completeTasks = prevTaskList.filter(
      (item) => !newTaskList.some((i) => item.task_id === i.task_id),
    );
    if (!completeTasks.length) {
      return;
    }

    // Update document count.
    if (completeTasks.length > 0) {
      getDetail();
    }

    // There are multiple tasks to complete or the root node needs to be updated.
    if (
      completeTasks.length > 1 ||
      completeTasks.find((item) => !item.target_pid)
    ) {
      knowledgeListRef.current?.getTableData();
      return;
    }

    // Only one task is completed to update the parent node and child node.
    const task = completeTasks[0];
    const parentNode: TreeNode | undefined = TreeUtils.findNode(
      knowledgeListRef.current?.treeData || [],
      (node: TreeNode) => {
        return node.document_id === task.target_pid;
      },
    );
    if (!parentNode) {
      return;
    }
    if (parentNode?.loaded) {
      knowledgeListRef.current!.getTableData({
        pId: parentNode.document_id ?? "",
        level: parentNode.level + 1,
        parentNode: { ...parentNode, loaded: false },
      });
      return;
    }
    knowledgeListRef.current!.updateDocument({
      documentId: parentNode.document_id ?? "",
    });
  }

  function openImportModal(data?: any) {
    const modalData = { ...detail, ...data };
    importKnowledgeRef.current?.handleOpen(modalData);
  }

  function onCreateFolder(data: RenameFormItem) {
    DocumentServiceApi()
      .documentServiceCreateDocument({
        dataset: id,
        doc: {
          display_name: data.name,
          name: data.name,
          type: DocTypeEnum.Folder,
        },
      })
      .then(() => {
        message.success(t("knowledge.createFolderSuccess"));
        knowledgeListRef.current?.getTableData();
      });
  }

  function onUpdate(data: Dataset): Promise<void> {
    return KnowledgeBaseServiceApi()
      .datasetServiceUpdateDataset({
        dataset: data.dataset_id || "",
        dataset2: data,
      })
      .then(() => {
        message.success(t("knowledge.editSuccess"));
        getDetail();
      });
  }

  function onDelete(knowledgeBaseId: string) {
    KnowledgeBaseServiceApi()
      .datasetServiceDeleteDataset({ dataset: knowledgeBaseId })
      .then(() => {
        message.success(t("knowledge.deleteSuccess"));
        navigate({
          pathname: "/lib/knowledge/list",
        });
      });
  }

  function onSearch(value: string) {
    knowledgeListRef.current?.refresh(value);
  }

  const hasWritePermission = useDatasetPermissionStore((state) =>
    state.hasWritePermission(),
  );

  const hasUploadPermission = useDatasetPermissionStore((state) =>
    state.hasUploadPermission(),
  );
  const canImport = hasUploadPermission || hasWritePermission;

  return (
    <div
      className="knowledge-detail-page"
      style={{
        width: "100%",
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        paddingBottom: "24px",
      }}
    >
      <DetailPageHeader
        title={detail?.display_name}
        titleExtra={
          developerActive ? (
            <>
              <span
                style={{
                  marginRight: "4px",
                  color: "var(--color-text-description)",
                }}
              >
                ID: {detail?.dataset_id}
              </span>
              <CopyOutlined
                style={{ color: "var(--color-text-description)" }}
                onClick={async () => {
                  try {
                    await writeTextToClipboard(detail?.dataset_id || "");
                    message.success(t("knowledge.copySuccess"));
                  } catch {
                    message.error(t("knowledge.copyFailedManual"));
                  }
                }}
              />
            </>
          ) : null
        }
        settingsMenu={
          detail?.acl?.includes(DatasetAclEnum.DatasetWrite) && (
            <div>
              <Tooltip title={t("common.edit")}>
                <Button
                  icon={<EditOutlined />}
                  style={{ marginLeft: "12px", width: "24px", height: "24px" }}
                  onClick={() => {
                    createUpdateRef.current?.onOpen(detail);
                  }}
                />
              </Tooltip>
              <Tooltip title={t("knowledge.authorize")}>
                <Button
                  icon={<SettingOutlined />}
                  style={{ marginLeft: "12px", width: "24px", height: "24px" }}
                  onClick={() =>
                    navigate({
                      pathname: `/lib/knowledge/auth/${id}`,
                    })
                  }
                />
              </Tooltip>
              <Tooltip title={t("common.delete")}>
                <Button
                  icon={<DeleteOutlined />}
                  style={{ marginLeft: "12px", width: "24px", height: "24px" }}
                  onClick={() => {
                    const knowledgeName = detail?.display_name || id;
                    confirmRef.current?.onOpen({
                      id,
                      title: t("knowledge.deleteTitle", {
                        name: knowledgeName,
                      }),
                      content: t("knowledge.deleteContent"),
                      confirmText: t("knowledge.deleteConfirmText", {
                        name: knowledgeName,
                      }),
                    });
                  }}
                />
              </Tooltip>
            </div>
          )
        }
        breadcrumbs={[
          { title: t("layout.knowledgeBase"), href: "/lib/knowledge/list" },
          { title: detail?.display_name },
        ]}
        description={detail?.desc}
        extraContent={[
          {
            label: t("knowledge.tags"),
            value:
              detail?.tags && detail?.tags.length > 0
                ? detail.tags.map((tag) => (
                    <Tooltip key={tag} title={tag}>
                      <Tag
                        style={{
                          marginLeft: "8px",
                          maxWidth: "240px",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {tag}
                      </Tag>
                    </Tooltip>
                  ))
                : "-",
          },
        ]}
        onBack={() => {
          const bool = ["aiwrite", "aireview", "chat"].includes(
            searchParams.get("from") ?? "",
          );
          if (bool) {
            navigate("/lib/knowledge/list");
          } else {
            navigate(-1);
          }
        }}
      />
      {uploadingNoticeVisible && (
        <Alert
          className="knowledge-parsing-notice"
          message={t("knowledge.documentUploadingKeepTabOpen")}
          type="warning"
          showIcon
        />
      )}
      <div className="toolbar my-4 mt-6 w-full">
        <Search
          className="search-input"
          placeholder={t("knowledge.searchDocPlaceholder")}
          allowClear
          variant="borderless"
          onSearch={onSearch}
          style={{
            width: 300,
          }}
        />
        {canImport && (
          <div className="toolbar-actions">
            {hasWritePermission && (
              <Button
                color="primary"
                variant="outlined"
                ghost
                onClick={() => {
                  createFolderRef.current?.onOpen({
                    title: t("knowledge.createFolder"),
                    form: {
                      name: t("knowledge.folderName"),
                      namePlaceholder: t("knowledge.folderNameRule"),
                      nameLen: 30,
                      nameRules: [
                        {
                          required: true,
                          validator: (_: any, value: string) => {
                            if (!value) {
                              return Promise.reject(t("knowledge.inputFolderName"));
                            }
                            if (
                              !/^[a-zA-Z\d\u4e00-\u9fa5_]+$/.test(value) ||
                              value.length > 30
                            ) {
                              return Promise.reject(
                                t("knowledge.folderNameRule"),
                              );
                            }
                            return Promise.resolve();
                          },
                        },
                      ],
                    },
                    data: {
                      name: "",
                    },
                  });
                }}
              >
                {t("knowledge.createFolder")}
              </Button>
            )}
            <Badge count={runningTotal} size="small" style={{ zIndex: 2 }}>
              <Space.Compact>
                <Tooltip title={
                  (embeddingReady === false || multimodalEmbeddingReady === false)
                    ? (
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
                    )
                    : undefined
                }>
                  <Button
                    type="primary"
                    disabled={embeddingReady === false || multimodalEmbeddingReady === false}
                    onClick={() => openImportModal({ importMode: "file" })}
                  >
                    {t("knowledge.importFile")}
                  </Button>
                </Tooltip>
                <Dropdown
                  menu={{
                    items: [
                      {
                        key: "importFile",
                        label: t("knowledge.importFile"),
                        disabled: embeddingReady === false || multimodalEmbeddingReady === false,
                      },
                      {
                        key: "importFolder",
                        label: t("knowledge.importFolder"),
                        disabled: embeddingReady === false || multimodalEmbeddingReady === false,
                      },
                      {
                        key: "importZip",
                        label: t("knowledge.importZip"),
                        disabled: embeddingReady === false || multimodalEmbeddingReady === false,
                      },
                      {
                        key: "taskManage",
                        label: (
                          <>
                            {t("knowledge.taskManageParse")}
                            {runningTotal > 0 && (
                              <Badge
                                count={runningTotal}
                                size="small"
                                offset={[-4, 6]}
                              >
                                <span
                                  style={{
                                    marginLeft: runningTotal >= 10 ? 6 : 12,
                                    opacity: 0,
                                  }}
                                >
                                  {runningTotal}
                                </span>
                              </Badge>
                            )}
                          </>
                        ),
                      },
                    ],
                    onClick: ({ key }) => {
                      if (key === "importFile") {
                        openImportModal({ importMode: "file" });
                        return;
                      }

                      if (key === "importFolder") {
                        openImportModal({
                          selectDirectory: true,
                          importMode: "folder",
                        });
                        return;
                      }

                      if (key === "importZip") {
                        openImportModal({ importMode: "zip" });
                        return;
                      }

                      if (key === "taskManage") {
                        importTaskRef.current?.handleOpen(detail);
                      }
                    },
                  }}
                >
                  <span style={{ display: "inline-flex" }}>
                    <Button type="primary">
                      <DownOutlined />
                    </Button>
                  </span>
                </Dropdown>
              </Space.Compact>
            </Badge>
            {hasWritePermission && (
              <Dropdown
                menu={{
                  items: [
                    {
                      key: "batchMove",
                      label: t("knowledge.batchMove"),
                      onClick: () => {
                        knowledgeListRef.current?.openBatchMove?.();
                      },
                    },
                    {
                      key: "batchDelete",
                      label: t("knowledge.batchDelete"),
                      onClick: () => {
                        knowledgeListRef.current?.deleteKnowledge();
                      },
                    },
                    {
                      key: "batchReparse",
                      label: t("knowledge.batchReparse"),
                      onClick: () => {
                        knowledgeListRef.current?.restartCheckedKnowledge();
                      },
                    },
                    {
                      key: "batchEditTags",
                      label: t("knowledge.batchEditTags"),
                      onClick: () => {
                        knowledgeListRef.current?.openBatchEditTags?.();
                      },
                    },
                  ] as MenuProps["items"],
                }}
                trigger={["click"]}
              >
                <span style={{ display: "inline-flex" }}>
                  <Space.Compact>
                    <Button variant="outlined" color="primary" ghost>
                      {t("knowledge.batchActions")}
                    </Button>
                    <Button variant="outlined" color="primary" ghost>
                      <DownOutlined />
                    </Button>
                  </Space.Compact>
                </span>
              </Dropdown>
            )}
          </div>
        )}
      </div>
      {detail && (
        <KnowledgeTable
          ref={knowledgeListRef}
          detail={detail}
          onImportKnowledge={(data) => openImportModal(data)}
          getImportingTotal={getImportingTotal}
          getDetail={getDetail}
        />
      )}

      <ConfirmModal ref={confirmRef} onClick={onDelete} />

      <CreateUpdateModal ref={createUpdateRef} onUpdate={onUpdate} />

      <RenameModel
        ref={createFolderRef}
        onSubmit={async (data) => onCreateFolder(data)}
      />

      <ImportKnowledgeModal
        ref={importKnowledgeRef}
        onParsingStart={() => setUploadingNoticeVisible(true)}
        onParsingSettled={() => setUploadingNoticeVisible(false)}
        onOk={({ pId } = {}) => {
          importingTaskListRef.current = [];
          getImportingTotal();
          getDetail();

          if (pId) {
            const parentNode = TreeUtils.findNode(
              knowledgeListRef.current?.treeData || [],
              (node: TreeNode) => node.document_id === pId,
            );

            if (parentNode) {
              knowledgeListRef.current?.getTableData({
                pId,
                level: parentNode.level + 1,
                parentNode,
              });
              return;
            }
          }

          knowledgeListRef.current?.getTableData({ pId: "", level: 0 });
        }}
      />

      <ImportTaskManage
        ref={importTaskRef}
        onClose={(hasSuspended) => {
          if (hasSuspended) {
            importingTaskListRef.current = [];
            getImportingTotal();
            knowledgeListRef.current?.getTableData({ pId: "", level: 0 });
          } else {
            getImportingTotal();
          }
        }}
      />
    </div>
  );
};

export default Detail;
