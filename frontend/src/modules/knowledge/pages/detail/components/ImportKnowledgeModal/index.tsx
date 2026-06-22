import { Alert, Button, Form, message, Modal } from "antd";
import { useTranslation } from "react-i18next";
import {
  forwardRef,
  Ref,
  useEffect,
  useImperativeHandle,
  useState,
} from "react";

import { DataSourceType } from "@/modules/knowledge/constants/common";
import DragUpload, { ALLOWED_FILE_TYPES } from "../DragUpload";
import {
  DocumentServiceApi,
  TaskServiceApi,
  uploadLargeFileToDataset,
} from "@/modules/knowledge/utils/request";
import TagSelect from "@/modules/knowledge/components/TagSelect";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";

const SINGLE_FILE_MAX_SIZE = 500 * 1024 * 1024;
const TOTAL_FILE_MAX_SIZE = 1 * 1024 * 1024 * 1024;
const ZIP_FILE_TYPES = ["zip"];

const LARGE_FILE_THRESHOLD = 10 * 1024 * 1024; // 10MB

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
  const [data, setData] = useState<IData>(InitData);
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);
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

  const { onOk, onParsingStart, onParsingSettled } = props;

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

  function handleOpen(currentData: IData) {
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
    form.resetFields();
    setData(InitData);
    setVisible(false);
    setLoading(false);
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
      if (isDirectoryMode) {
        await submitFolderMode(fileItems, values.tags, startMode);
      } else {
        await submitNormalMode(fileItems, values.tags, startMode);
      }

      message.success(t("knowledge.uploadCompleteParsingStarted"));
      handleClose();
      onOk({ pId: data.p_id });
    } catch (err) {
      console.error(err);
      message.error(
        err instanceof Error ? err.message : t("knowledge.uploadFailedRetry"),
      );
    } finally {
      setLoading(false);
    }
  }

  function startTasksAfterUpload(taskIds: string[], startMode: string | undefined) {
    onParsingStart?.();
    TaskServiceApi()
      .startTasks(data.dataset_id, {
        task_ids: taskIds,
        ...(startMode ? { start_mode: startMode } : {}),
      })
      .catch((err) => {
        console.error("Start parsing tasks failed:", err);
        message.error(t("knowledge.startParsingFailed"));
      })
      .finally(() => {
        onParsingSettled?.();
      });
  }

  // Folder mode: upload each file individually with relative_path,
  // then create tasks with relative_path so the backend creates the folder structure.
  async function submitFolderMode(
    fileItems: { originFile: File; path: string }[],
    tags: string[] | undefined,
    startMode: string | undefined,
  ) {
    // { upload_file_id, relative_path } pairs collected after upload
    const uploadedItems: { upload_file_id: string; relative_path: string }[] =
      [];

    for (const item of fileItems) {
      if (item.originFile.size > LARGE_FILE_THRESHOLD) {
        // Large file: multi-step upload with relative_path
        const uploadFileId = await uploadLargeFileToDataset(
          data.dataset_id,
          item.originFile,
          { documentPid: data.p_id, relativePath: item.path },
        );
        uploadedItems.push({
          upload_file_id: uploadFileId,
          relative_path: item.path,
        });
      } else {
        // Small file: single upload with relative_path in form data
        const formData = new FormData();
        formData.append("files", item.originFile);
        formData.append("relative_path", item.path);
        if (data.p_id) formData.append("document_pid", data.p_id);

        const uploadRes = await TaskServiceApi().uploadFiles(
          data.dataset_id,
          formData,
        );
        const uploaded = uploadRes.data.files || [];
        if (!uploaded.length) {
          throw new Error(t("knowledge.uploadResultMissing"));
        }
        uploadedItems.push({
          upload_file_id: uploaded[0].upload_file_id!,
          relative_path: item.path,
        });
      }
    }

    if (!uploadedItems.length) {
      throw new Error(t("knowledge.uploadResultMissing"));
    }

    // Create tasks with relative_path so the backend creates the folder
    const createRes = await TaskServiceApi().createTasks(data.dataset_id, {
      items: uploadedItems.map(({ upload_file_id, relative_path }) => ({
        upload_file_id,
        task: {
          relative_path,
          ...(tags?.length ? { document_tags: tags } : {}),
        },
      })),
    });

    const taskIds = (createRes.data.tasks || [])
      .map((t) => t.task_id)
      .filter(Boolean) as string[];

    if (!taskIds.length) {
      throw new Error(t("knowledge.createTaskFailed"));
    }

    startTasksAfterUpload(taskIds, startMode);
  }

  // Normal mode (plain files / zip): batch upload small files, multi-step for large files.
  async function submitNormalMode(
    fileItems: { originFile: File; path: string }[],
    tags: string[] | undefined,
    startMode: string | undefined,
  ) {
    const smallItems = fileItems.filter(
      (f) => f.originFile.size <= LARGE_FILE_THRESHOLD,
    );
    const largeItems = fileItems.filter(
      (f) => f.originFile.size > LARGE_FILE_THRESHOLD,
    );

    const allUploadFileIds: string[] = [];

    if (smallItems.length > 0) {
      const formData = new FormData();
      smallItems.forEach(({ originFile }) => formData.append("files", originFile));
      if (data.p_id) formData.append("document_pid", data.p_id);
      if (tags?.length) formData.append("document_tags", JSON.stringify(tags));

      const uploadRes = await TaskServiceApi().uploadFiles(
        data.dataset_id,
        formData,
      );
      const uploadedFiles = uploadRes.data.files || [];
      if (!uploadedFiles.length) {
        throw new Error(t("knowledge.uploadResultMissing"));
      }
      uploadedFiles.forEach((f) => allUploadFileIds.push(f.upload_file_id!));
    }

    for (const item of largeItems) {
      const uploadFileId = await uploadLargeFileToDataset(
        data.dataset_id,
        item.originFile,
        { documentPid: data.p_id },
      );
      allUploadFileIds.push(uploadFileId);
    }

    if (!allUploadFileIds.length) {
      throw new Error(t("knowledge.uploadResultMissing"));
    }

    const createRes = await TaskServiceApi().createTasks(data.dataset_id, {
      items: allUploadFileIds.map((upload_file_id) => ({
        upload_file_id,
        task: tags?.length ? { document_tags: tags } : {},
      })),
    });

    const taskIds = (createRes.data.tasks || [])
      .map((t) => t.task_id)
      .filter(Boolean) as string[];

    if (!taskIds.length) {
      throw new Error(t("knowledge.createTaskFailed"));
    }

    startTasksAfterUpload(taskIds, startMode);
  }

  // function changeSourceType() {
  //   form.resetFields(['fileList', 'urlList', 'notionAccount', 'notionPages'])
  // }

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
            {isOnlyRead
              ? t("knowledge.uploadKnowledgeFile")
              : t("knowledge.parseAndImport")}
          </Button>
        </div>
      }
    >
      {loading && (
        <Alert
          message={t("knowledge.documentParsingKeepTabOpen")}
          type="warning"
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
                  maxSize={TOTAL_FILE_MAX_SIZE}
                  maxFileSize={SINGLE_FILE_MAX_SIZE}
                  accept={isZipMode ? ZIP_FILE_TYPES : ALLOWED_FILE_TYPES}
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
                      {t("knowledge.scannedPdfHint")}
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
