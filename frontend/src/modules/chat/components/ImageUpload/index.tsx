import React, {
  useState,
  forwardRef,
  useImperativeHandle,
  useRef,
  useEffect,
} from "react";
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

function toRawFile(file: UploadFile): File {
  return (file.originFileObj ?? file) as File;
}

function attachUriToFile(list: FileItem[], uid: string, uri: string): FileItem[] {
  return list.map((f) => {
    if (f.uid === uid) {
      f.uri = uri;
    }
    return f;
  });
}

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
    const filesRef = useRef<FileItem[]>(files);

    useEffect(() => {
      filesRef.current = files;
    }, [files]);

    const commitFiles = (nextFiles: FileItem[]) => {
      filesRef.current = nextFiles;
      setFiles(nextFiles);
      updateFiles?.(nextFiles);
    };

    const tryAppendFile = (rcFile: RcFile): boolean => {
      const prev = filesRef.current;
      if (!checkFileCountLimit(prev, rcFile, max)) {
        return false;
      }
      commitFiles([...prev, rcFile] as FileItem[]);
      return true;
    };

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
        if (!tryAppendFile(rcFile)) {
          return;
        }
        uploadFile(
          rcFile,
          (uri) => {
            commitFiles(attachUriToFile(filesRef.current, rcFile.uid, uri));
          },
          () => {
            commitFiles(
              filesRef.current.filter((f) => f.uid !== rcFile.uid),
            );
          },
        );
      });
    };

    const uploadProps: UploadProps = {
      multiple: false,
      showUploadList: false,
      disabled: disabled || listNum >= max,
      accept: types.join(","),
      // Do not bind fileList/maxCount here — file list is managed locally and
      // rendered via ShowChatFileList. Controlled fileList + beforeUpload:false
      // causes Ant Design to skip onChange on subsequent picks when files exist.
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
      clear: () => commitFiles([]),
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
          applyBeforeAddResult(result, current);
          return;
        }
        runAddFiles(droppedFiles, current);
      },
    }));

    function applyBeforeAddResult(
      result: OnBeforeAddFilesResult,
      current: FileItem[],
    ) {
      if (!result) {
        return;
      }
      const docExclusive = t("chat.docImageExclusive");
      result.toasts.forEach((toastText) => {
        if (toastText === docExclusive) {
          message.warning(toastText);
        } else {
          message.info(toastText);
        }
      });
      if (result.clearFirst) {
        commitFiles([]);
      }
      if (result.filesToAdd.length > 0) {
        runAddFiles(result.filesToAdd, result.clearFirst ? [] : current);
      }
    }

    function handleOnUploadChange(info: UploadChangeParam): string | void {
      if (disabled) {
        if (disabledReason) {
          message.warning(disabledReason);
        }
        return Upload.LIST_IGNORE;
      }
      const { file } = info;

      if (file.status === "removed") {
        return;
      }

      const rawFile = toRawFile(file);

      if (!validateFileType(rawFile, types)) return Upload.LIST_IGNORE;
      if (!validateFileSize(rawFile)) return Upload.LIST_IGNORE;

      const current = [...files];
      const result = onBeforeAddFiles?.([rawFile], current);
      if (result) {
        applyBeforeAddResult(result, current);
        return Upload.LIST_IGNORE;
      }

      const rcFile = rawFile as RcFile;
      if (!rcFile.uid) {
        rcFile.uid = `rc-upload-${Date.now()}-${Math.random()}`;
      }

      if (!tryAppendFile(rcFile)) {
        return Upload.LIST_IGNORE;
      }
      uploadFile(
        rcFile,
        (uri) => {
          commitFiles(attachUriToFile(filesRef.current, rcFile.uid, uri));
        },
        () => {
          commitFiles(filesRef.current.filter((f) => f.uid !== rcFile.uid));
        },
      );
      return Upload.LIST_IGNORE;
    }

    function onRemove(uid: string) {
      commitFiles(filesRef.current.filter((item: FileItem) => item.uid !== uid));
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
