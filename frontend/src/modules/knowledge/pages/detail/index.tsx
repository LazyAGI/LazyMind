import type { PaperUploadContext } from "../../api/academicReferences";
import {
  Alert,
  message,
  Modal,
  Button,
  Badge,
  Dropdown,
  Tooltip,
  Input,
} from "antd";
import { axiosInstance, BASE_URL } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import {
  fetchModelFeatures,
  isImageEmbedRequired,
  MODEL_FEATURES_CHANGED_EVENT,
} from "@/hooks/useModelFeatures";
import type { MenuProps } from "antd";
import { useEffect, useRef, useState, useCallback, useMemo, MouseEvent } from "react";
import { useParams } from "react-router-dom";
import {
  CopyOutlined,
  DownOutlined,
} from "@ant-design/icons";
import { Link, useNavigate } from "react-router-dom";
import { useKnowledgeListReturn } from "../../hooks/useKnowledgeListNavigation";
import { useTranslation } from "react-i18next";
import {
  Dataset,
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
import { getImportErrorMessage } from "@/modules/knowledge/utils/importError";
import { inspectFinishedImportTasks, mergeTrackedImportTasks, reconcileImportTaskFailures, type ImportTaskFailure, type ImportTaskState } from "./importTaskFailures";
import { TaskTab } from "./components/ImportTaskList";
import { createPaperPdfUploader } from "@/modules/knowledge/paper/pdfUpload";
import { RuntimeReadinessError, waitForRuntimeCapability } from "@/runtime/readiness";
import { DocumentServiceApi, TaskServiceApi } from "../../utils/request";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import {
  fetchUserUiPreferences,
  USER_UI_PREFERENCES_CHANGED_EVENT,
} from "@/modules/user/uiPreferencesApi";
import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";

import { DetailPageHeader } from "@/components/ui";
import KnowledgeBaseSyncNow from "@/modules/knowledge/components/KnowledgeBaseSyncNow";
import {
  effectiveProcessingLevel,
  type ProcessingLevel,
} from "@/modules/knowledge/utils/processingLevel";
import { useRAGComponent } from "@/modules/modelProvider/contexts/RAGComponentContext";

import "./index.scss";
import PaperLibrary from "@/modules/knowledge/paper/PaperLibrary";
import { isPaperLibrary } from "@/modules/knowledge/paper/store";

type DatasetWithDataSourceFlag = Dataset & {
  created_by_data_source?: boolean;
};

type DatasetWithProcessingLevel = DatasetWithDataSourceFlag & {
  processing_level?: ProcessingLevel;
};

function isDatasetCreatedByDataSource(dataset?: DatasetWithDataSourceFlag) {
  return Boolean(dataset?.created_by_data_source);
}

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
  const { availability: ragAvailability } = useRAGComponent();
  const ragReady = ragAvailability === "ready";
  const ragUnavailableReason = ragAvailability === "checking"
    ? "正在检查本地知识库组件状态"
    : ragAvailability === "error"
      ? "无法确认本地知识库组件状态，请重试"
      : "请先安装本地知识库组件，并重启本地服务";
  const knowledgeListRef = useRef<IKnowledgeListRef>(null);
  const createFolderRef = useRef<RenameModalRef>(null);
  const importKnowledgeRef = useRef<IImportKnowledgeModalRef>();
  const importTaskRef = useRef<IImportTaskManageRef>();
  const pollingRef = useRef(new Polling());
  const importingTaskListRef = useRef<ImportTaskState[]>([]);
  const taskInspectionTokensRef = useRef(new Map<string, symbol>());

  const { id = "" } = useParams();
  const currentDataset = useDatasetPermissionStore(state => state.currentDataset);
  const detail = currentDataset?.dataset_id === id ? currentDataset as DatasetWithProcessingLevel : undefined;
  const [runningTotal, setRunningTotal] = useState(0);
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const [embeddingReady, setEmbeddingReady] = useState<boolean | null>(null);
  const [multimodalEmbeddingReady, setMultimodalEmbeddingReady] = useState<
    boolean | null
  >(null);
  const [documentParsingEnabled, setDocumentParsingEnabled] = useState<boolean | null>(null);
  const [uploadingNoticeVisible, setUploadingNoticeVisible] = useState(false);
  const [taskPollingError, setTaskPollingError] = useState("");
  const [importTaskFailures, setImportTaskFailures] = useState<ImportTaskFailure[]>([]);
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === "system-admin";

  const uploadPaperPdfTask = useMemo(() => createPaperPdfUploader(id), [id]);
  const activeDatasetIdRef = useRef(id);
  activeDatasetIdRef.current = id;
  const paperLibrary = detail?.dataset_id === id && isPaperLibrary(detail.scene);

  const navigate = useNavigate();
  const { returnToList, listUrl } = useKnowledgeListReturn();

  const loadDataset = useDatasetPermissionStore(state => state.loadDataset);
  const getDetail = useCallback((refresh = true) => {
    void loadDataset(id, refresh).catch(() => {});
  }, [id, loadDataset]);

  useEffect(() => {
    importingTaskListRef.current = [];
    taskInspectionTokensRef.current.clear();
    setTaskPollingError("");
    setImportTaskFailures([]);
    getDetail(false);
    getImportingTotal();
    const unwrap = (
      resp: { data: { data?: { ready: boolean } } | { ready: boolean } } | null,
    ): boolean | null => {
      if (!resp) return null;
      const body = resp.data;
      const d =
        body && typeof body === "object" && "data" in body
          ? (body as { data?: { ready: boolean } }).data
          : (body as { ready: boolean });
      return d?.ready ?? null;
    };
    const loadEmbeddingReady = () => {
      fetchModelFeatures(true)
        .then((features) => {
          const imageEmbedRequired = isImageEmbedRequired(features);
          return Promise.all([
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
          ]).then(([embResp, multiResp]) => {
            setEmbeddingReady(unwrap(embResp));
            setMultimodalEmbeddingReady(
              imageEmbedRequired ? unwrap(multiResp) : null,
            );
          });
        })
        .catch(() => {
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
      window.removeEventListener(
        MODEL_FEATURES_CHANGED_EVENT,
        loadEmbeddingReady,
      );
      document.removeEventListener("visibilitychange", onVisibilityChange);
      pollingRef.current.cancel();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [getDetail]);

  useEffect(() => {
    let cancelled = false;
    const loadDocumentParsingState = () => fetchUserUiPreferences({ silentError: true } as never)
      .then((preferences) => {
        if (!cancelled) setDocumentParsingEnabled(preferences.document_parsing_enabled);
      })
      .catch(() => {
        // Keep the existing backend protection authoritative when the optional
        // preference request cannot be read.
      });
    void loadDocumentParsingState();
    window.addEventListener(USER_UI_PREFERENCES_CHANGED_EVENT, loadDocumentParsingState);
    return () => {
      cancelled = true;
      window.removeEventListener(USER_UI_PREFERENCES_CHANGED_EVENT, loadDocumentParsingState);
    };
  }, []);

  useEffect(() => {
    const syncDeveloperActive = () => {
      setDeveloperActive(isDeveloperModeActive());
    };

    const handleDeveloperActiveChange = (event: Event) => {
      const nextActive = (event as CustomEvent<{ active?: boolean }>).detail
        ?.active;
      setDeveloperActive(
        typeof nextActive === "boolean" ? nextActive : isDeveloperModeActive(),
      );
    };

    window.addEventListener("storage", syncDeveloperActive);
    window.addEventListener(
      DEVELOPER_ACTIVE_EVENT,
      handleDeveloperActiveChange,
    );

    return () => {
      window.removeEventListener("storage", syncDeveloperActive);
      window.removeEventListener(
        DEVELOPER_ACTIVE_EVENT,
        handleDeveloperActiveChange,
      );
    };
  }, []);

  function getImportingTotal() {
    pollingRef.current.cancel();
    pollingRef.current.start({
      interval: IMPORT_TASK_POLL_INTERVAL,
      // Filter to running tasks on the backend so total_size is accurate and
      // we are not limited by the default page size of 20.
      request: () =>
        TaskServiceApi().listTasks(id, {
          taskStatus: "running",
          pageSize: 1000,
        }, { silentError: true } as never),
      onSuccess: ({ data = {} }) => {
        setTaskPollingError("");
        const newTaskList = data.tasks || [];
        newTaskList.forEach((task: ImportTaskState) => {
          if (task.task_id) taskInspectionTokensRef.current.delete(task.task_id);
        });
        // Tasks in WORKING state are actively being parsed by the algorithm service.
        // Tasks in WAITING state are still uploading / queued before parsing starts.
        const uploadingTasks = newTaskList.filter(
          (t: any) => t.task_state === "WAITING",
        );
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
      onError: (error) => {
        setTaskPollingError(getImportErrorMessage(error, "无法查询导入任务状态，请重试"));
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
    void notifyImportTaskFailures(completeTasks);

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

  async function notifyImportTaskFailures(tasks: any[]) {
    const pending = mergeTrackedImportTasks([], tasks);
    const inspection = Symbol();
    pending.forEach((task) => taskInspectionTokensRef.current.set(String(task.task_id), inspection));
    const requestedDatasetId = id;
    const failures = await inspectFinishedImportTasks(pending, async (taskId) => {
      const response = await TaskServiceApi().getTask(id, taskId, { silentError: true } as never);
      return response.data;
    });
    if (activeDatasetIdRef.current !== requestedDatasetId) return;
    // A retry can reuse the task ID while an earlier inspection is in flight.
    const currentTasks = pending.filter((task) => taskInspectionTokensRef.current.get(String(task.task_id)) === inspection);
    const currentIds = new Set(currentTasks.map((task) => String(task.task_id)));
    currentIds.forEach((taskId) => taskInspectionTokensRef.current.delete(taskId));
    const currentFailures = failures.filter((failure) => currentIds.has(failure.taskId));
    setImportTaskFailures((previous) => reconcileImportTaskFailures(previous, currentTasks, currentFailures));
    if (currentFailures.some((failure) => failure.needsFFmpeg)) {
      Modal.confirm({
        title: t("knowledge.ffmpegRequiredTitle"),
        content: t("knowledge.ffmpegRequiredDesc"),
        okText: t("knowledge.configureFfmpeg"),
        cancelText: t("common.close"),
        onOk: () => navigate("/settings?section=system_tools#ffmpeg-dependency"),
      });
    }
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

  async function uploadPaperPdf(file: File, importContext?: PaperUploadContext): Promise<string | void> {
    if (!canImport) throw new Error("没有向此论文库上传文件的权限");
    if (!file.name.toLowerCase().endsWith(".pdf")) throw new Error("请选择 PDF 文件");
    if (file.size > 500 * 1024 * 1024) throw new Error("单个 PDF 文件不能超过 500 MB");

    try {
      if (effectiveProcessingLevel(detail?.processing_level) !== "stored") {
        await waitForRuntimeCapability("parser");
      }
    } catch (error) {
      if (error instanceof RuntimeReadinessError) throw new Error(t("runtime.initializationFailed"));
      throw error;
    }

    const { documentId, tasks } = await uploadPaperPdfTask(file, hasWritePermission ? "DEFAULT" : "UPLOAD", importContext);
    if (activeDatasetIdRef.current === id) {
      importingTaskListRef.current = mergeTrackedImportTasks(importingTaskListRef.current, tasks);
      getImportingTotal();
      getDetail();
    }
    return documentId;
  }

  const isDocumentParsingUnavailable = documentParsingEnabled !== true;
  const storageOnly = !ragReady || detail?.processing_level === "stored";
  const importDisabled = ragAvailability === "checking" || ragAvailability === "error" ||
    (!storageOnly && (isDocumentParsingUnavailable || embeddingReady === false || multimodalEmbeddingReady === false));
  const importDisabledReason = ragAvailability === "checking" || ragAvailability === "error"
    ? ragUnavailableReason
    : !storageOnly && isDocumentParsingUnavailable
      ? documentParsingEnabled === false
        ? "文档解析已暂停，请在设置中重新启用"
        : "正在读取文档解析状态"
      : undefined;
  const showDataSourceSync = isDatasetCreatedByDataSource(
    detail as DatasetWithDataSourceFlag | undefined,
  );

  const refreshKnowledgeAfterSync = useCallback(() => {
    getDetail();
    getImportingTotal();
    knowledgeListRef.current?.getTableData();
  }, [getDetail]);

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
      {!paperLibrary && <DetailPageHeader
        className="knowledge-detail-header"
        title={
          <span className="knowledge-detail-title-copy">
            <strong>{detail?.display_name}</strong>
            {detail?.desc ? <small>{detail.desc}</small> : null}
          </span>
        }
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
        breadcrumbs={[
          { title: <Link to={listUrl} onClick={(event: MouseEvent<HTMLAnchorElement>) => {
            if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
            event.preventDefault();
            returnToList();
          }}>{t("layout.knowledgeBase")}</Link> },
          { title: detail?.display_name },
        ]}
        onBack={() => returnToList()}
      />}
      {(taskPollingError || importTaskFailures.length > 0) && (
        <Alert
          className="knowledge-parsing-notice"
          type="error"
          showIcon
          message={importTaskFailures.length ? "文献导入出现错误" : "无法获取导入进度"}
          description={
            <div style={{ maxHeight: 180, overflowY: "auto", whiteSpace: "pre-wrap" }}>
              {taskPollingError && <div>{taskPollingError}</div>}
              {importTaskFailures.map((failure) => <div key={failure.taskId}>{failure.message}</div>)}
            </div>
          }
          action={
            <Button size="small" onClick={() => importTaskRef.current?.handleOpen({ dataset_id: id, initialTab: TaskTab.Failed })}>
              查看失败任务
            </Button>
          }
        />
      )}
      {!paperLibrary && uploadingNoticeVisible && (
        <Alert
          className="knowledge-parsing-notice"
          message={t("knowledge.documentUploadingKeepTabOpen")}
          type="warning"
          showIcon
        />
      )}
      {paperLibrary && detail ? (
        <div className="paper-library-page">
          <PaperLibrary
            key={id}
            datasetId={id}
            title={detail.display_name || "论文库"}
            onBack={() => returnToList()}
            onUploadPdf={uploadPaperPdf}
            processingLevel={effectiveProcessingLevel(detail.processing_level)}
            parsers={detail.parsers}
            canParse={ragReady && documentParsingEnabled !== false}
          />
        </div>
      ) : null}
      {!paperLibrary && <div className="toolbar my-4 mt-6 w-full">
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
            {showDataSourceSync && detail?.dataset_id ? (
              <KnowledgeBaseSyncNow
                datasetId={detail.dataset_id}
                documentParsingEnabled={documentParsingEnabled}
                enabled={ragReady}
                onSyncComplete={refreshKnowledgeAfterSync}
              />
            ) : null}
            {hasWritePermission && (
              <Button
                color="primary"
                variant="outlined"
                ghost
                disabled={!ragReady}
                title={!ragReady ? ragUnavailableReason : undefined}
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
                              return Promise.reject(
                                t("knowledge.inputFolderName"),
                              );
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
              <Tooltip
                title={
                  importDisabledReason ||
                  (!storageOnly && (embeddingReady === false || multimodalEmbeddingReady === false) ? (
                    isAdmin ? (
                      <span>
                        {embeddingReady === false
                          ? t("knowledge.embeddingNotReadyBannerAdmin")
                          : t(
                              "knowledge.multimodalEmbeddingNotReadyBannerAdmin",
                            )}
                        <a
                          href="/settings?section=models"
                          style={{
                            marginLeft: 8,
                            color: "#fff",
                            textDecoration: "underline",
                          }}
                          onClick={(e: MouseEvent<HTMLAnchorElement>) => {
                            e.preventDefault();
                            navigate("/settings?section=models");
                          }}
                        >
                          {t("knowledge.goToConfig")}
                        </a>
                      </span>
                    ) : embeddingReady === false ? (
                      t("knowledge.embeddingNotReadyBanner")
                    ) : (
                      t("knowledge.multimodalEmbeddingNotReadyBanner")
                    )
                  ) : undefined)
                }
              >
                <Dropdown
                  menu={{
                    items: [
                      {
                        key: "importFile",
                        label: t("knowledge.importFile"),
                        disabled: importDisabled,
                      },
                      {
                        key: "importFolder",
                        label: t("knowledge.importFolder"),
                        disabled: importDisabled,
                      },
                      {
                        key: "importZip",
                        label: t("knowledge.importZip"),
                        disabled: importDisabled,
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
                        disabled: !ragReady,
                      },
                    ],
                    onClick: ({ key }: { key: string }) => {
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
                  trigger={["click"]}
                >
                  <Button
                    className="knowledge-toolbar-dropdown-button"
                    type="primary"
                  >
                    {t("knowledge.importFile")}
                    <DownOutlined />
                  </Button>
                </Dropdown>
              </Tooltip>
            </Badge>
            {hasWritePermission && (
              <Dropdown
                menu={{
                  items: [
                    {
                      key: "importPaperReferences",
                      label: "导入所选论文的参考文献",
                      disabled: !ragReady,
                      onClick: () => {
                        knowledgeListRef.current?.importSelectedPaperReferences?.();
                      },
                    },
                    {
                      key: "batchMove",
                      label: t("knowledge.batchMove"),
                      disabled: !ragReady,
                      onClick: () => {
                        knowledgeListRef.current?.openBatchMove?.();
                      },
                    },
                    {
                      key: "batchDelete",
                      label: t("knowledge.batchDelete"),
                      disabled: !ragReady,
                      onClick: () => {
                        knowledgeListRef.current?.deleteKnowledge();
                      },
                    },
                    {
                      key: "batchReparse",
                      label: t("knowledge.batchReparse"),
                      disabled: !ragReady || isDocumentParsingUnavailable,
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
                  <Button
                    className="knowledge-toolbar-dropdown-button"
                    variant="outlined"
                    color="primary"
                    ghost
                  >
                    {t("knowledge.batchActions")}
                    <DownOutlined />
                  </Button>
                </span>
              </Dropdown>
            )}
          </div>
        )}
      </div>}
      {detail && !paperLibrary && (
        <KnowledgeTable
          ref={knowledgeListRef}
          detail={detail}
          documentParsingEnabled={documentParsingEnabled}
          onImportKnowledge={(data) => openImportModal(data)}
          getImportingTotal={getImportingTotal}
          getDetail={getDetail}
        />
      )}

      <RenameModel
        ref={createFolderRef}
        onSubmit={async (data) => onCreateFolder(data)}
      />

      <ImportKnowledgeModal
        storageOnly={detail?.processing_level === "stored"}
        ref={importKnowledgeRef}
        onParsingStart={() => setUploadingNoticeVisible(true)}
        onParsingSettled={() => setUploadingNoticeVisible(false)}
        onOk={({ pId, tasks = [] } = {}) => {
          importingTaskListRef.current = mergeTrackedImportTasks(importingTaskListRef.current, tasks);
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
        onRetrySuccess={(task) => {
          if (task.task_id) taskInspectionTokensRef.current.delete(task.task_id);
          importingTaskListRef.current = mergeTrackedImportTasks(importingTaskListRef.current, [task]);
          getImportingTotal();
        }}
        onClose={(hasSuspended) => {
          if (hasSuspended) {
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
