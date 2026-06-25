import React, { useState, forwardRef, useImperativeHandle } from "react";
import { Upload, message, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import {
  RcFile,
  UploadChangeParam,
  UploadProps,
  UploadFile,
} from "antd/es/upload/interface";

import "./index.scss";
import { uploadFileInChunks } from "@/modules/chat/utils/chunkUpload";

export interface ImageUploadImperativeProps {
  removeFile: (uid?: string) => void;
  getFiles: () => (RcFile & { uri: string })[];
  clear: () => void;
  uploadFiles: (files: File[]) => void;
  getUploadingCount: () => number;
}

interface Props {
  max: number;
  types: string[];
  icon: React.ReactNode;
  updateFiles: (files: RcFile[]) => void;
  listNum: number;
  disabled?: boolean;
  disabledReason?: string;
  
  onBeforeAddFiles?: (
    newFiles: File[],
    currentFiles: (RcFile & { uri?: string })[],
  ) => OnBeforeAddFilesResult;
}
interface FileItem extends RcFile {
  uri: string;
}

export const allowedImageTypes = [".png", ".jpg", ".jpeg"];
export const allowedFileTypes = [".pdf", ".docx", ".doc", ".pptx"];
export const allowedUploadTypes = [...allowedImageTypes, ...allowedFileTypes];

export type OnBeforeAddFilesResult = {
  filesToAdd: File[];
  clearFirst: boolean;
  toasts: string[];
} | null;

const ImageUpload = forwardRef<ImageUploadImperativeProps, Props>(
  (props, ref) => {
    const { t } = useTranslation();
    const {
      max,
      types,
      icon,
      updateFiles,
      listNum,
      disabled = false,
      disabledReason,
      onBeforeAddFiles,
    } = props;
    const [files, setFiles] = useState<FileItem[]>([]);
    const [uploadingCount, setUploadingCount] = useState(0);

    const validateFileType = (
      file: File | UploadFile,
      allowedTypes: string[],
    ): boolean => {
      const ext = file.name.substring(file.name.lastIndexOf(".")).toLowerCase();
      if (!allowedTypes.includes(ext)) {
        message.warning(
          t("chat.unsupportedFileType", { types: allowedTypes.join(",") }),
        );
        return false;
      }
      return true;
    };

    const validateFileSize = (file: File | UploadFile): boolean => {
      const ext = file.name.substring(file.name.lastIndexOf(".")).toLowerCase();
      const currentFileSizeMB = (file.size ?? 0) / 1024 / 1024;

      if (allowedImageTypes.includes(ext)) {
        if (currentFileSizeMB > 5) {
          message.error(t("chat.uploadSizeLimit5MB"));
          return false;
        }
      }
      if (allowedFileTypes.includes(ext)) {
        if (currentFileSizeMB > 100) {
          message.error(t("chat.uploadSizeLimit100MB"));
          return false;
        }
      }
      return true;
    };

    const checkFileCountLimit = (
      currentFiles: FileItem[],
      newFile: FileItem | RcFile | UploadFile,
      maxCount: number,
    ): boolean => {
      // const tempGroup = Object.groupBy([...currentFiles, newFile], (item) => {
      //   const suffix = item.name.substring(item.name.lastIndexOf('.')).toLowerCase();
      //   return allowedImageTypes.includes(suffix) ? 'image' : 'file';
      // });
      // const tempGroup = Object.groupBy([...currentFiles, newFile], (item) => {
      //   const suffix = item.name.substring(item.name.lastIndexOf('.')).toLowerCase();
      //   return allowedImageTypes.includes(suffix) ? 'image' : 'file';
      // });

      // if ((tempGroup?.file?.length ?? 0) > 3) {
      //   return false;
      // }
      // if ((tempGroup?.image?.length ?? 0) > 3) {
      //   return false;
      // }

      if ([...currentFiles, newFile].length > maxCount) {
        message.warning(t("chat.maxThreeFilesAndImages"));
        return false;
      }

      if (currentFiles.length >= maxCount) {
        // const ext = newFile.name.substring(newFile.name.lastIndexOf('.')).toLowerCase();
        message.warning(t("chat.maxThreeFilesAndImages"));
        return false;
      }

      return true;
    };

    const uploadFile = (
      file: RcFile | UploadFile,
      onSuccess?: (uri: string) => void,
      onError?: () => void,
    ) => {
      setUploadingCount((prev) => prev + 1);
      
      uploadFileInChunks(file as File, {
        timeout: 2 * 60 * 1000,
        onProgress: (progress) => {
          console.log(
            t("chat.uploadProgressLog", { percentage: progress.percentage }),
          );
        },
      })
        .then((storedPath) => {
          setUploadingCount((prev) => prev - 1);
          onSuccess?.(storedPath);
        })
        .catch((error) => {
          console.error(t("chat.fileUploadFailedLog"), error);
          message.error(t("chat.fileUploadFailedRetry"));
          setUploadingCount((prev) => prev - 1);
          onError?.();
        });
    };

    const runAddFiles = (toAdd: File[], _baseFileList: FileItem[]) => {
      toAdd.forEach((file) => {
        const rcFile = file as RcFile;
        rcFile.uid = `rc-upload-${Date.now()}-${Math.random()}`;
        if (!validateFileType(file, types)) {
          return;
        }
        if (!validateFileSize(file)) {
          return;
        }
        setFiles((prev) => {
          if (!checkFileCountLimit(prev, rcFile, max)) {
            return prev;
          }
          const newFiles = [...prev, rcFile] as FileItem[];
          updateFiles?.(newFiles);
          uploadFile(
            rcFile,
            (uri) => {
              setFiles((prevFiles) => {
                const nextFiles = prevFiles.map((f) =>
                  f.uid === rcFile.uid ? { ...f, uri } : f,
                );
                updateFiles?.(nextFiles);
                return nextFiles;
              });
            },
            () => {
              setFiles((prevFiles) => {
                const nextFiles = prevFiles.filter((f) => f.uid !== rcFile.uid);
                updateFiles?.(nextFiles);
                return nextFiles;
              });
            },
          );
          return newFiles;
        });
      });
    };

    const uploadProps: UploadProps = {
      multiple: false,
      showUploadList: false,
      disabled: disabled || listNum >= max,
      maxCount: max,
      accept: types.join(","),
      fileList: files,
      className: "chat-image-upload",
      beforeUpload: () => false,
      onChange: handleOnUploadChange,
    };

    useImperativeHandle(ref, () => ({
      removeFile: (uid?: string) => {
        if (uid) {
          onRemove(uid);
        }
      },
      getFiles: () => files,
      clear: () => setFiles([]),
      getUploadingCount: () => uploadingCount,
      uploadFiles: (droppedFiles: File[]) => {
        if (disabled) {
          if (disabledReason) {
            message.warning(disabledReason);
          }
          return;
        }
        const current = [...files];
        const result = onBeforeAddFiles?.(droppedFiles, current);
        if (result) {
          const docExclusive = t("chat.docImageExclusive");
          result.toasts.forEach((toastText) => {
            if (toastText === docExclusive) {
              message.warning(toastText);
            } else {
              message.info(toastText);
            }
          });
          if (result.clearFirst) {
            setFiles([]);
            updateFiles?.([]);
          }
          runAddFiles(result.filesToAdd, result.clearFirst ? [] : current);
          return;
        }
        runAddFiles(droppedFiles, current);
      },
    }));

    function handleOnUploadChange(info: UploadChangeParam): string | void {
      if (disabled) {
        if (disabledReason) {
          message.warning(disabledReason);
        }
        return Upload.LIST_IGNORE;
      }
      const { file } = info;

      if (!validateFileType(file, types)) return Upload.LIST_IGNORE;
      if (!validateFileSize(file)) return Upload.LIST_IGNORE;

      const current = [...files];
      const result = onBeforeAddFiles?.([file as unknown as File], current);
      if (result) {
        const docExclusive = t("chat.docImageExclusive");
        result.toasts.forEach((toastText) => {
          if (toastText === docExclusive) {
            message.warning(toastText);
          } else {
            message.info(toastText);
          }
        });
        if (result.clearFirst) {
          setFiles([]);
          updateFiles?.([]);
        }
        if (result.filesToAdd.length > 0) {
          runAddFiles(result.filesToAdd, result.clearFirst ? [] : current);
        }
        return;
      }

      setFiles((prev) => {
        if (!checkFileCountLimit(prev, file, max)) {
          return prev;
        }
        const newFiles = [...prev, file] as FileItem[];
        updateFiles?.(newFiles);
        return newFiles;
      });
      uploadFile(file, (uri) => {
        setFiles((prev) => {
          const nextFiles = prev.map((f) =>
            f.uid === file?.uid ? { ...f, uri } : f,
          );
          updateFiles?.(nextFiles);
          return nextFiles;
        });
      });
    }

    function onRemove(uid: string) {
      setFiles((prev) => prev.filter((item: FileItem) => item.uid !== uid));
    }

    return (
      <Upload {...uploadProps}>
        <Tooltip placement="top" title={t("chat.uploadTooltipLimit")}>
          {icon}
        </Tooltip>
      </Upload>
    );
  },
);

ImageUpload.displayName = "ImageUpload";

export default ImageUpload;
