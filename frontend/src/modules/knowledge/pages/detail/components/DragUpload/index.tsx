import { Alert, message } from "antd";
import { useTranslation } from "react-i18next";
import {
  DeleteOutlined,
  FileZipOutlined,
  InboxOutlined,
  InfoCircleFilled,
  PaperClipOutlined,
} from "@ant-design/icons";
import classNames from "classnames";
import { debounce, uniq } from "lodash";
import VirtualList from "rc-virtual-list";
import { ReactNode, useEffect, useRef, useState } from "react";
import { v4 as uuidv4 } from "uuid";

import { TaskOrigin } from "@/modules/knowledge/constants/common";
import FileUtils from "@/modules/knowledge/utils/file";
import "./index.scss";
import { compatibleUploadConfig } from "@/modules/knowledge/utils/batchUpload";
import RiskTip from "@/modules/knowledge/components/RiskTip";
import JSZip from "@progress/jszip-esm";

const ZIP_TOTAL_MAX_SIZE = 1 * 1024 * 1024 * 1024;
const ZIP_SINGLE_FILE_MAX_SIZE = 500 * 1024 * 1024;
export const ALLOWED_FILE_TYPES = [
  "pdf",
  "docx",
  "doc",
  "hwp",
  "ppt",
  "pptx",
  "jpg",
  "jpeg",
  "png",
  "gif",
  "bmp",
  "webp",
  "tiff",
  "tif",
  "ipynb",
  "epub",
  "md",
  "mbox",
  "csv",
  "xls",
  "xlsx",
  "mp3",
  "mp4",
  "txt",
  "xml",
  "json",
  "jsonl",
  "yaml",
  "yml",
  "html",
  "htm",
  "py",
];

const ZIP_ALLOWED_SUFFIX = ALLOWED_FILE_TYPES;

export interface IDragUploadProps {
  value?: any[];
  onChange?: (value: any[]) => void;
  disabled?: boolean; // Disabled.
  maxCount?: number; // Total quantity.
  maxSize?: number; // Total size Unit B.
  maxFileSize?: number; // Single file size (unit: B).
  accept?: string[]; // Supported suffixes.
  title?: string; // Title.
  description?: ReactNode; // Description
  targetPath?: string; // The imported path is used with maxLevel to limit the total level.
  maxLevel?: number; // Maximum directory level.
  hiddenFileList?: boolean; // Hidden Files List.
  className?: string;
  taskOrigin?: TaskOrigin; // The default source of the task is the knowledge base.
  disableDragFolder?: boolean; // Disable dragging of folders.
  onZipStatusChange?: (hasError: boolean) => void;
  selectDirectory?: boolean; // Enable selecting directory via input.
  zipMode?: boolean; // Zip import mode.
  invalidTypeMessage?: string; // Unsupported type toast.
  invalidDropMessage?: string; // Invalid drop object toast.
}

const DragUpload = (props: IDragUploadProps) => {
  const { t } = useTranslation();
  const {
    value = [],
    onChange = () => {},
    disabled,
    maxCount,
    maxSize,
    maxFileSize,
    accept,
    title,
    description,
    targetPath,
    maxLevel,
    hiddenFileList,
    className = "",
    taskOrigin,
    disableDragFolder,
    onZipStatusChange,
    selectDirectory,
    zipMode,
    invalidTypeMessage,
    invalidDropMessage,
  } = props;
  const [showAlert, setShowAlert] = useState(false);
  const dragFilesRef = useRef<any[]>([]);
  const singleUpload = maxCount === 1;
  const { ECompatibleFileState } = compatibleUploadConfig();
  const [zipStatusMap, setZipStatusMap] = useState<
    Record<string, { loading: boolean; error?: string }>
  >({});
  const dirAttrs: any = selectDirectory
    ? { webkitdirectory: "webkitdirectory", directory: "directory" }
    : {};

  useEffect(() => {
    value.forEach((file) => {
      if (
        file.path?.toLowerCase().endsWith(".zip") &&
        !zipStatusMap[file.uid] &&
        file.originFile
      ) {
        checkZip(file);
      }
    });
  }, [value]);

  useEffect(() => {
    if (onZipStatusChange) {
      const currentUids = new Set(value.map((f) => f.uid));
      const hasError = Object.entries(zipStatusMap).some(
        ([uid, status]) => currentUids.has(uid) && !!status.error,
      );
      onZipStatusChange(hasError);
    }
  }, [zipStatusMap, onZipStatusChange, value]);

  const checkZip = async (file: any) => {
    setZipStatusMap((prev) => ({ ...prev, [file.uid]: { loading: true } }));

    try {
      // @ts-ignore
      const zip = await new JSZip().loadAsync(file.originFile);

      // calculate total size
      let totalSize = 0;
      let hasSubFolder = false;
      let hasInvalidType = false;
      let hasOversizeFile = false;
      zip.forEach((_path: string, file: any) => {
        if (file?.dir) {
          return;
        }
        const normalizedPath = (_path || "").replace(/\\/g, "/");
        if (normalizedPath.includes("/")) {
          hasSubFolder = true;
        }
        const fileName = normalizedPath.split("/").pop() || normalizedPath;
        const suffix = FileUtils.getSuffix(fileName);
        if (!ZIP_ALLOWED_SUFFIX.includes(suffix)) {
          hasInvalidType = true;
        }
        // @ts-ignore
        const fileSize = file._data?.uncompressedSize || 0;
        if (fileSize > ZIP_SINGLE_FILE_MAX_SIZE) {
          hasOversizeFile = true;
        }
        totalSize += fileSize;
      });

      if (hasSubFolder) {
        message.warning(
          t("knowledge.zipRootOnly"),
        );
      }
      if (hasInvalidType) {
        message.warning(t("knowledge.supportedDocTypes"));
      }

      if (hasOversizeFile) {
        setZipStatusMap((prev) => ({
          ...prev,
          [file.uid]: {
            loading: false,
            error: t("knowledge.zipSingleFileMax"),
          },
        }));
        return;
      }

      if (totalSize > ZIP_TOTAL_MAX_SIZE) {
        setZipStatusMap((prev) => ({
          ...prev,
          [file.uid]: { loading: false, error: t("knowledge.fileTooLargeSplit") },
        }));
        return;
      }

      setZipStatusMap((prev) => ({ ...prev, [file.uid]: { loading: false } }));
    } catch (e) {
      const isEncrypted =
        e?.toString() === "Error: Encrypted zip are not supported";

      if (isEncrypted) {
        setZipStatusMap((prev) => ({
          ...prev,
          [file.uid]: {
            loading: false,
            error: t("knowledge.zipEncryptedUnsupported"),
          },
        }));
        return;
      }
      setZipStatusMap((prev) => ({
        ...prev,
        [file.uid]: { loading: false, error: t("knowledge.fileDamaged") },
      }));
    }
  };

  const isZipFileItem = (item: any) =>
    item?.path?.toLowerCase().endsWith(".zip") ||
    item?.originFile?.name?.toLowerCase().endsWith(".zip");

  const handleChange = (fileList: any[]) => {
    const getRootFolderName = (path: string) => {
      const parts = path?.split("/") || [];
      return parts.length > 1 ? parts[0] : null;
    };

    const rootFolder =
      value.map((i) => getRootFolderName(i.path)).find(Boolean) ||
      fileList.map((i) => getRootFolderName(i.path)).find(Boolean);

    let filteredFileList = fileList;
    if (rootFolder) {
      const isInvalidFile = (item: any) => {
        const folder = getRootFolderName(item.path);
        return folder && folder !== rootFolder;
      };

      if (fileList.some(isInvalidFile)) {
        message.warning(t("knowledge.onlyOneFolder"));
        filteredFileList = fileList.filter((item) => !isInvalidFile(item));
      }
    }

    const newFileList: any[] = [];
    const errorList: string[] = [];
    let totalSize = value.reduce((prev, cur) => prev + cur.size, 0);
    let totalCount = value.length;
    let hasInvalidType = false;
    filteredFileList.forEach((item) => {
      // File already exists.
      if (value.some((i) => i.path === item.path)) {
        return;
      }

      // Unsupported format.
      if (accept && !accept.includes(FileUtils.getSuffix(item.path))) {
        hasInvalidType = true;
        return;
      }

      const skipGenericSizeLimitForZip = zipMode && isZipFileItem(item);
      if (!skipGenericSizeLimitForZip && maxFileSize && item.size > maxFileSize) {
        errorList.push(
          t("knowledge.singleFileMax", {
            size: FileUtils.formatFileSize(maxFileSize, 0),
          }),
        );
        return;
      }

      if (maxLevel) {
        const pathArr = (targetPath?.split("/") || []).concat(
          item.path?.split("/") || [],
        );
        if (pathArr.length > maxLevel) {
          setShowAlert(true);
          return;
        }
      }

      totalSize += item.size;
      if (!skipGenericSizeLimitForZip && maxSize && totalSize > maxSize) {
        errorList.push(
          t("knowledge.totalSizeMax", {
            size: FileUtils.formatFileSize(maxSize, 0),
          }),
        );
        return;
      }

      totalCount += 1;
      if (!singleUpload && maxCount && totalCount > maxCount) {
        errorList.push(t("knowledge.totalCountMax", { count: maxCount }));
        return;
      }

      newFileList.push(item);
    });

    if (hasInvalidType) {
      errorList.push(invalidTypeMessage || t("knowledge.fileFormatUnsupported"));
    }

    uniq(errorList).forEach((err) => {
      message.error(err);
    });

    if (!newFileList.length) {
      return;
    }

    if (singleUpload) {
      onChange(newFileList.slice(0, 1));
    } else {
      onChange([...newFileList, ...value]);
    }
  };

  const addSelectFiles = (files: FileList | null) => {
    const newFileList: any[] = [];
    for (const file of Array.from(files || [])) {
      const relativePath = file.webkitRelativePath || file.name;
      const fileName = relativePath.split("/").pop() || "";
      if (fileName === ".DS_Store" || fileName.startsWith("._")) {
        continue;
      }
      const newFile = {
        uid: uuidv4(),
        path: relativePath,
        size: file.size,
        state: ECompatibleFileState.UploadPending,
        percent: 0,
        originFile: file,
        taskId: "",
        taskOrigin,
      };
      newFileList.push(newFile);
    }
    handleChange(newFileList);
  };

  const addDragFiles = debounce(() => {
    handleChange(dragFilesRef.current);
    dragFilesRef.current = [];
  }, 100);

  const dragUpload = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    if (disabled) {
      return;
    }

    let hasBlockedEntry = false;
    for (const item of Array.from(e.dataTransfer?.items || [])) {
      if (item.kind === "file") {
        const entry = item.webkitGetAsEntry();
        if (!entry) {
          continue;
        }
        if (zipMode && entry.isDirectory) {
          hasBlockedEntry = true;
          continue;
        }
        if (!selectDirectory && !zipMode && entry.isDirectory) {
          hasBlockedEntry = true;
          continue;
        }
        if (selectDirectory && entry.isFile) {
          hasBlockedEntry = true;
          continue;
        }
        getFileFormEntry(entry);
      }
    }
    if (hasBlockedEntry) {
      message.error(invalidDropMessage || t("knowledge.dropContentUnsupported"));
    }
  };

  const getFileFormEntry = (entry: FileSystemEntry | null) => {
    if (entry?.isFile) {
      (entry as any).file((file: { name: string; size: any }) => {
        if (file.name === ".DS_Store") {
          return;
        }
        dragFilesRef.current.push({
          uid: uuidv4(),
          path: entry.fullPath.slice(1),
          size: file?.size,
          state: ECompatibleFileState.UploadPending,
          percent: 0,
          originFile: file,
          taskId: "",
          taskOrigin,
        });
        addDragFiles();
      });
    }

    if (entry?.isDirectory && !disableDragFolder) {
      const reader = (entry as any).createReader();
      readDir(reader);
    }
  };

  const readDir = (dirReader: {
    readEntries: (arg0: (entries: any[]) => void) => void;
  }) => {
    dirReader.readEntries((entries: any[]) => {
      entries.forEach((v) => {
        getFileFormEntry(v);
      });
      if (entries.length > 0) {
        readDir(dirReader);
      }
    });
  };

  const deleteFile = (file: any) => {
    const newFiles = value.filter((v) => v.uid != file.uid);
    onChange(newFiles);
  };

  return (
    <>
      <label>
        <div
          className={classNames(
            "dragContainer",
            disabled ? "disabled" : "",
            className ? className : "",
          )}
          onDragEnter={(e) => e.preventDefault()}
          onDragOver={(e) => e.preventDefault()}
          onDrop={dragUpload}
        >
          <InboxOutlined className="uploadIcon" style={{ fontSize: 48 }} />
          <div className="drag-title">
            <span style={{ marginRight: 4 }}>
              {title || (
                <>
                  {t("knowledge.dragUploadOr")}{" "}
                  <span className="drag-text">
                    {selectDirectory
                      ? t("knowledge.selectFolder")
                      : t("knowledge.selectFileBtn")}
                  </span>
                </>
              )}
            </span>
            <RiskTip />
          </div>
          <div className="description">{description}</div>
        </div>
        <input
          type="file"
          value={[]}
          multiple={!singleUpload}
          style={{ display: "none" }}
          disabled={disabled}
          onChange={(e) => addSelectFiles(e.target.files)}
          accept={accept?.map((i) => `.${i}`).join(",")}
          {...dirAttrs}
        />
      </label>
      {showAlert && (
        <Alert
          message={t("knowledge.filteredNestedFolder")}
          type="warning"
          showIcon
          closable
          onClose={() => setShowAlert(false)}
          style={{ marginTop: 4 }}
        />
      )}
      {!hiddenFileList && (
        <VirtualList
          data={value}
          height={Math.min(200, value.length * 30)}
          itemHeight={30}
          itemKey="uid"
          style={{ marginTop: 4 }}
        >
          {(file) => {
            const error = zipStatusMap[file.uid]?.error;
            return (
              <div className="fileItem" key={file.uid}>
                {file.path?.toLowerCase().endsWith(".zip") ? (
                  <FileZipOutlined />
                ) : (
                  <PaperClipOutlined />
                )}
                <div title={file.path} className="fileName">
                  <span className={classNames("filePath", { error })}>
                    {file.path}
                  </span>
                  {error && (
                    <span
                      style={{
                        color: "#ff4d4f",
                        marginLeft: 8,
                        fontSize: 12,
                        float: "right",
                      }}
                    >
                      <InfoCircleFilled style={{ marginRight: 4 }} />
                      {error}
                    </span>
                  )}
                </div>
                <DeleteOutlined
                  className="deleteIcon"
                  onClick={() => deleteFile(file)}
                />
              </div>
            );
          }}
        </VirtualList>
      )}
    </>
  );
};

export default DragUpload;
