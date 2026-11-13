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
import { getImportErrorMessage } from "@/modules/knowledge/utils/importError";
import TagSelect from "@/modules/knowledge/components/TagSelect";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import { localizeErrorCode } from "@/components/request";

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

export interface ImportTaskRef {
  task_id?: string;
  target_pid?: string;
  display_name?: string;
}

interface IProps {
  storageOnly?: boolean;
  onOk: (payload?: { pId?: string; tasks?: ImportTaskRef[] }) => void;
  onUploadStart?: () => void;
  onUploadSettled?: () => void;
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
  const [importError, setImportError] = useState("");
  const retryParents = useRef(new Map<File, string>());
  const [tags, setTags] = useState<string[]>([]);
  const [hasZipError, setHasZipError] = useState(false);
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

  const { onOk, onUploadStart, onUploadSettled } = props;

  const [form] = Form.useForm();

  useImperativeHandle(ref, () => ({
    handleOpen,
  }));

  useEffect(() => {
    getTags();
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
    retryParents.current.clear();
    form.resetFields();
    setData(InitData);
    setVisible(false);
    setLoading(false);
    setHasZipError(false);
    setImportError("");
  }

  async function submit(values: any) {
    setLoading(true);
    setImportError("");
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
      const {tasks, failures, retryFiles} = await submitWithHashReuse(fileItems, values.tags, startMode);
      if (tasks.length) onOk({ pId: data.p_id, tasks });
      if (failures.length) {
        form.setFieldValue("fileList", (values.fileList || []).filter((file: any) => retryFiles.has(file.originFile ?? file)));
        const summary = failures.join("\n");
        setImportError(summary);
        message.warning(tasks.length ? "部分文件已保存，未成功的文件可修正后重试" : "文件未保存，请查看具体原因");
        return;
      }
      message.success(props.storageOnly ? "文件已保留在知识库内，未解析、未建立索引"
        : "原文件已保存，可在知识库中查看解析和索引状态");
      handleClose();
    } catch (err) {
      if ((err as Error)?.name === "AbortError") {
        return;
      }
      console.error(err);
      const errorMessage = getImportErrorMessage(err, localizeErrorCode("2000509"));
      setImportError(errorMessage);
      message.error(errorMessage);
    } finally {
      setLoading(false);
    }
  }

  async function startTasksAfterUpload(
    taskIds: string[],
    startMode: string | undefined,
  ) {
    return TaskServiceApi().startTasks(data.dataset_id, {
      task_ids: taskIds,
      ...(startMode ? { start_mode: startMode } : {}),
    });
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
    onUploadStart?.();
    try {
      const failures: string[] = [];
      const retryFiles = new Set<File>();
      const failedUploads = new Set<File>();
      const items = await buildUploadTaskItems({
        datasetId: data.dataset_id,
        fileItems,
        tags,
        documentPid: data.p_id,
        folderMode: isDirectoryMode,
        onFileError: (item, error) => {
          failedUploads.add(item.originFile);
          retryFiles.add(item.originFile);
          failures.push(`${item.path}：${getImportErrorMessage(error)}`);
        },
      });

      if (!items.length) {
        if (failures.length) return {tasks: [] as ImportTaskRef[], failures, retryFiles};
        throw new Error(t("knowledge.uploadResultMissing"));
      }
      const uploadedFiles = fileItems.filter(item => !failedUploads.has(item.originFile));

      const createRes = await TaskServiceApi().createTasks(data.dataset_id, {
        items: items.map((item, index) => {
          const parent = retryParents.current.get(uploadedFiles[index]?.originFile);
          return parent ? {...item, task: {...item.task, document_pid: parent}} : item;
        }),
        allow_partial: true,
      });
      for (const outcome of createRes.data.outcomes || []) {
        if (outcome.status === "CREATED") continue;
        const file = uploadedFiles[outcome.input_index];
        const partialNotice = outcome.status === "PARTIAL" ? "；部分成员已保存，请在知识库核对后仅重传失败成员" : "";
        failures.push(`${file?.path || outcome.display_name}：${outcome.message || "导入失败"}${partialNotice}`);
        if (file && outcome.target_pid) retryParents.current.set(file.originFile, outcome.target_pid);
        // A partially committed archive/input must be inspected, not re-uploaded wholesale.
        if (file && outcome.status === "FAILED" && !outcome.task_ids?.length) retryFiles.add(file.originFile);
      }

      // Upload/hash failures never reach CreateTask, so they have no outcome of
      // their own. Reuse the folder created for a successful sibling prefix.
      if (isDirectoryMode && !data.p_id && failedUploads.size) {
        const prefixOf = (path: string) => {
          const parts = path.trim().replace(/\\/g, "/").split("/");
          return parts.length > 1 ? parts[0].trim() : "";
        };
        const parentsByPrefix = new Map<string, string>();
        for (const outcome of createRes.data.outcomes || []) {
          const file = uploadedFiles[outcome.input_index];
          if (file && outcome.target_pid) {
            const prefix = prefixOf(file.path);
            if (prefix) parentsByPrefix.set(prefix, outcome.target_pid);
          }
        }
        for (const file of fileItems) {
          if (!failedUploads.has(file.originFile) || retryParents.current.has(file.originFile)) continue;
          const parent = parentsByPrefix.get(prefixOf(file.path));
          if (parent) retryParents.current.set(file.originFile, parent);
        }
      }

      const taskIds = (createRes.data.tasks || [])
        .map((task) => task.task_id)
        .filter(Boolean) as string[];

      if (!taskIds.length) {
        if (failures.length) return {tasks: [] as ImportTaskRef[], failures, retryFiles};
        throw new Error(localizeErrorCode("2000509"));
      }

      let startedTasks;
      try {
        const started = await startTasksAfterUpload(taskIds, startMode);
        startedTasks = started.data.tasks || [];
      } catch {
        // Creation already committed the originals. A processing outage must
        // not ask the user to upload the same files again.
        message.warning("原文件已保存，处理暂未启动；服务就绪后可在知识库中重试");
        startedTasks = createRes.data.tasks || [];
      }
      // Track every saved task even if start returns only a subset of outcomes.
      const tasks = (createRes.data.tasks || []).map((created): ImportTaskRef => {
        const task = startedTasks.find(item => item.task_id === created.task_id) || created;
        return {
          task_id: task.task_id,
          target_pid: created?.target_pid || data.p_id,
          display_name: task.display_name || created?.display_name,
        };
      });
      return {tasks, failures, retryFiles};
    } finally {
      onUploadSettled?.();
    }
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
            : "原文件可以正常上传。解析和索引进度可在知识库中查看，服务未就绪时可稍后重试。"} />
      )}
      {importError && <Alert type="error" showIcon message={importError} style={{ marginBottom: 16, whiteSpace: "pre-wrap" }} />}
      {loading && (
        <Alert
          message="正在上传并保存文件，请勿关闭页面"
          type="info"
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
