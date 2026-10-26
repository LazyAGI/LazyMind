import { Alert, Button, Form, message, Modal } from "antd";
import { useRAGComponent } from "@/modules/modelProvider/contexts/RAGComponentContext";
import { useTranslation } from "react-i18next";
import {
  forwardRef,
  Ref,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";

import { DataSourceType } from "@/modules/knowledge/constants/common";
import DragUpload, { ALLOWED_FILE_TYPES } from "../DragUpload";
import { DocumentServiceApi, TaskServiceApi, getKnowledgeOptions, type KnowledgeOptions } from "@/modules/knowledge/utils/request";
import { buildUploadTaskItems } from "@/modules/knowledge/utils/uploadByHash";
import TagSelect from "@/modules/knowledge/components/TagSelect";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import { localizeErrorCode } from "@/components/request";
import {
  RuntimeReadinessError,
  waitForRuntimeCapability,
} from "@/runtime/readiness";

const SINGLE_FILE_MAX_SIZE = 500 * 1024 * 1024;
const TOTAL_FILE_MAX_SIZE = 1 * 1024 * 1024 * 1024;
const ZIP_FILE_TYPES = ["zip"];

type ImportMode = "file" | "folder" | "zip";

interface IData {
  dataset_id: string;
  targetPath?: string;
  p_id?: string;
  data_source_type?: DataSourceType;
  selectDirectory?: boolean;
  importMode?: ImportMode;
}

export interface IImportKnowledgeModalRef {
  handleOpen: (data: IData) => void;
}

interface IProps {
  storageOnly?: boolean;
  onOk: (payload?: { pId?: string }) => void;
  onParsingStart?: () => void;
  onParsingSettled?: () => void;
}

const InitData = {
  dataset_id: "",
  targetPath: "",
  p_id: "",
  data_source_type: DataSourceType.LOCAL,
  selectDirectory: false,
  importMode: "file" as ImportMode,
};

const ImportKnowledgeModal = (props: IProps, ref: Ref<unknown> | undefined) => {
  const { t } = useTranslation();
  const { availability } = useRAGComponent();
  const storageOnly = availability !== "ready" || props.storageOnly === true;
  const [data, setData] = useState<IData>(InitData);
  const [uploadOptions, setUploadOptions] = useState<KnowledgeOptions["upload"]>();
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);
  const [runtimeWaiting, setRuntimeWaiting] = useState(false);
  const [tags, setTags] = useState<string[]>([]);
  const [hasZipError, setHasZipError] = useState(false);
  const runtimeWaitAbortRef = useRef<AbortController | null>(null);
  const hasOnlyReadPermission = useDatasetPermissionStore((state) =>
    state.hasOnlyReadPermission(),
  );
  const hasUploadPermission = useDatasetPermissionStore((state) =>
    state.hasUploadPermission(),
  );
  const hasWritePermission = useDatasetPermissionStore((state) =>
    state.hasWritePermission(),
  );
  const isOnlyRead =
    (hasOnlyReadPermission || hasUploadPermission) && !hasWritePermission;

  const { onOk, onParsingStart, onParsingSettled } = props;

  const [form] = Form.useForm();

  useImperativeHandle(ref, () => ({
    handleOpen,
  }));

  useEffect(() => {
    getTags();
    return () => runtimeWaitAbortRef.current?.abort();
  }, []);

  function getTags() {
    DocumentServiceApi()
      .documentServiceAllDocumentTags()
      .then((res) => {
        setTags(res.data.tags);
      });
  }

  async function handleOpen(currentData: IData) {
    const options = await getKnowledgeOptions();
    setUploadOptions(options.upload);
    if (currentData.data_source_type) {
      form.setFieldsValue({ dataSourceType: currentData.data_source_type });
    }
    setData(currentData);
    setVisible(true);
  }

  const importMode: ImportMode =
    data.importMode || (data.selectDirectory ? "folder" : "file");
  const isDirectoryMode = importMode === "folder";
  const isZipMode = importMode === "zip";

  function handleClose() {
    runtimeWaitAbortRef.current?.abort();
    runtimeWaitAbortRef.current = null;
    form.resetFields();
    setData(InitData);
    setVisible(false);
    setLoading(false);
    setRuntimeWaiting(false);
    setHasZipError(false);
  }

  async function submit(values: any) {
    setLoading(true);
    // Each item from DragUpload carries { originFile: File, path: string }
    // path = webkitRelativePath (folder select) or entry.fullPath.slice(1) (drag)
    const fileItems: { originFile: File; path: string }[] = (
      values.fileList || []
    ).map((f: any) => ({
      originFile: f.originFile ?? f,
      path: f.path ?? (f.originFile ?? f).name,
    }));

    const startMode = hasWritePermission
      ? "DEFAULT"
      : hasUploadPermission
        ? "UPLOAD"
        : undefined;

    try {
      const controller = new AbortController();
      runtimeWaitAbortRef.current = controller;
      if (!storageOnly) await waitForRuntimeCapability("parser", {
        signal: controller.signal,
        onWaiting: () => setRuntimeWaiting(true),
      });
      setRuntimeWaiting(false);

      await submitWithHashReuse(fileItems, values.tags, startMode);
      message.success(props.storageOnly ? "文件已保留在知识库内，未解析、未建立索引"
        : availability !== "ready" ? "原文件、解析文本和基础切片已保存；组件及模型就绪后将自动建立索引"
        : t("knowledge.uploadCompleteParsingStarted"));
      handleClose();
      onOk({ pId: data.p_id });
    } catch (err) {
      if ((err as Error)?.name === "AbortError") {
        return;
      }
      console.error(err);
      message.error(
        err instanceof RuntimeReadinessError
          ? t("runtime.initializationFailed")
          : localizeErrorCode("2000509"),
      );
    } finally {
      runtimeWaitAbortRef.current = null;
      setRuntimeWaiting(false);
      setLoading(false);
    }
  }

  async function startTasksAfterUpload(
    taskIds: string[],
    startMode: string | undefined,
  ) {
    onParsingStart?.();
    try {
      await TaskServiceApi().startTasks(data.dataset_id, {
        task_ids: taskIds,
        ...(startMode ? { start_mode: startMode } : {}),
      });
    } finally {
      onParsingSettled?.();
    }
  }

  /**
   * Hash → checkHashes → upload only missing files → create tasks
   * (content_hash for existing / duplicates, upload_file_id for newly uploaded).
   */
  async function submitWithHashReuse(
    fileItems: { originFile: File; path: string }[],
    tags: string[] | undefined,
    startMode: string | undefined,
  ) {
    const items = await buildUploadTaskItems({
      datasetId: data.dataset_id,
      fileItems,
      tags,
      documentPid: data.p_id,
      folderMode: isDirectoryMode,
    });

    if (!items.length) {
      throw new Error(t("knowledge.uploadResultMissing"));
    }

    const createRes = await TaskServiceApi().createTasks(data.dataset_id, {
      items,
    });

    const taskIds = (createRes.data.tasks || [])
      .map((task) => task.task_id)
      .filter(Boolean) as string[];

    if (!taskIds.length) {
      throw new Error(localizeErrorCode("2000509"));
    }

    await startTasksAfterUpload(taskIds, startMode);
  }

  return (
    <Modal
      open={visible}
      destroyOnHidden
      title={t("knowledge.importFileTitle")}
      onCancel={handleClose}
      centered
      width={896}
      style={{ paddingBottom: 0, minHeight: 300 }}
      className="modal-max-height"
      maskClosable={false}
      footer={
        <div style={{ display: "flex", justifyContent: "flex-end" }}>
          <Button onClick={handleClose}>{t("common.cancel")}</Button>
          <Button
            type="primary"
            disabled={loading || hasZipError}
            onClick={() => form.submit()}
            style={{ marginLeft: 16 }}
          >
            {storageOnly ? "上传并保存" : isOnlyRead
              ? t("knowledge.uploadKnowledgeFile")
              : t("knowledge.parseAndImport")}
          </Button>
        </div>
      }
    >
      {storageOnly && (
        <Alert type="info" showIcon style={{ marginBottom: 16 }}
          message={props.storageOnly ? "文件将保留在知识库内，仅保存原文件，不解析、不建立索引。"
            : "无需安装 RAG 即可保存解析文本和基础切片。安装组件并配置 embedding 后自动建立索引；扫描件需要 OCR。"} />
      )}
      {loading && (
        <Alert
          message={
            storageOnly ? "正在上传并保存文件，请勿关闭页面" : runtimeWaiting
              ? t("runtime.aiServiceInitializingDocument")
              : t("knowledge.documentParsingKeepTabOpen")
          }
          type={runtimeWaiting ? "info" : "warning"}
          showIcon
          style={{ marginBottom: 16 }}
        />
      )}
      <Form
        form={form}
        layout="vertical"
        colon={false}
        onFinish={submit}
        scrollToFirstError
        initialValues={{
          dataSourceType: DataSourceType.LOCAL,
          // urlList: [''],
          isDfs: false,
        }}
      >
        <Form.Item
          noStyle
          shouldUpdate={(prev, next) =>
            prev.dataSourceType !== next.dataSourceType
          }
        >
          {() => {
            return (
              <Form.Item
                name="fileList"
                rules={[{ required: true, message: t("knowledge.selectFile") }]}
              >
                <DragUpload
                  disabled={loading}
                  maxCount={300}
                  maxSize={uploadOptions?.max_batch_bytes ?? TOTAL_FILE_MAX_SIZE}
                  maxFileSize={uploadOptions?.max_file_bytes ?? SINGLE_FILE_MAX_SIZE}
                  supportedFileTypes={uploadOptions?.file_types}
                  accept={isZipMode ? ZIP_FILE_TYPES : uploadOptions?.file_types ?? ALLOWED_FILE_TYPES}
                  targetPath={data.targetPath}
                  maxLevel={2}
                  onZipStatusChange={setHasZipError}
                  zipMode={isZipMode}
                  selectDirectory={isDirectoryMode}
                  disableDragFolder={!isDirectoryMode}
                  invalidTypeMessage={
                    isDirectoryMode
                      ? t("knowledge.supportedDocTypes")
                      : isZipMode
                        ? t("knowledge.supportedZipFile")
                        : t("knowledge.supportedDocTypes")
                  }
                  invalidDropMessage={
                    isDirectoryMode
                      ? t("knowledge.importFolder")
                      : isZipMode
                        ? t("knowledge.supportedZipFile")
                        : t("knowledge.supportedDocTypes")
                  }
                  description={
                    <>
                      {isDirectoryMode
                        ? t("knowledge.supportedFolderImport")
                        : isZipMode
                          ? t("knowledge.supportedZipFile")
                          : t("knowledge.supportedDocTypes")}
                      <br />
                      {isZipMode && (
                        <>
                          {t("knowledge.zipRootOnly")}
                          <br />
                        </>
                      )}
                      {t("knowledge.uploadLimitHint")}
                      <br />
                      {!storageOnly && t("knowledge.scannedPdfHint")}
                    </>
                  }
                />
              </Form.Item>
            );
          }}
        </Form.Item>
        <Form.Item name="tags" label={t("knowledge.tags")}>
          <TagSelect tags={tags} />
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default forwardRef(ImportKnowledgeModal);
