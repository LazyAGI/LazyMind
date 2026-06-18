import { useCallback, useEffect, useMemo, useState } from "react";
import { Spin, message, Empty } from "antd";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import FileUtils from "@/modules/knowledge/utils/file";
import { Segment } from "@/api/generated/knowledge-client";
import {
  RenderHtml,
  RenderTxt,
  RenderPpt,
  RenderExcel,
  RenderWord,
} from "./renderers";

import { RenderPdf } from "@/components/ui";
import { normalizeProxyableUrl } from "@/modules/knowledge/utils/request";

import "./index.scss";

interface FileViewerProps {
  file?: string;
  fileName: string;
  segment?: Segment;
}

const IMAGE_FILE_TYPES = [
  "jpg",
  "jpeg",
  "png",
  "gif",
  "bmp",
  "webp",
  "tiff",
  "tif",
];

const VIDEO_FILE_TYPES = ["mp4", "webm", "ogg", "ogv", "mov", "m4v"];
const AUDIO_FILE_TYPES = ["mp3", "wav", "m4a", "aac", "flac"];

const MEDIA_MIME_TYPES: Record<string, string> = {
  aac: "audio/aac",
  bmp: "image/bmp",
  flac: "audio/flac",
  gif: "image/gif",
  jpeg: "image/jpeg",
  jpg: "image/jpeg",
  m4a: "audio/mp4",
  m4v: "video/mp4",
  mov: "video/quicktime",
  mp3: "audio/mpeg",
  mp4: "video/mp4",
  ogg: "video/ogg",
  ogv: "video/ogg",
  png: "image/png",
  tif: "image/tiff",
  tiff: "image/tiff",
  wav: "audio/wav",
  webm: "video/webm",
  webp: "image/webp",
};

const FileViewer = (props: FileViewerProps) => {
  const { t } = useTranslation();
  const { file, segment } = props;
  const resolvedFileUrl = useMemo(() => normalizeProxyableUrl(file), [file]);
  const [loading, setLoading] = useState(false);
  const [fileData, setFileData] = useState<ArrayBuffer | null>(null);
  const [meta, setMeta] = useState<Record<string, unknown> | null>(null);
  const [content, setContent] = useState<string | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [mediaObjectUrl, setMediaObjectUrl] = useState("");

  useEffect(() => {
    if (!segment) {
      setMeta(null);
      setContent(null);
      return;
    }
    if (segment?.meta) {
      try {
        const parsedMeta = JSON.parse(segment.meta);
        setMeta(parsedMeta);
      } catch {
        const uiMessage = t("knowledge.loadingFailedReload");
        message.error(uiMessage);
        setMeta(null);
      }
    } else {
      setMeta(null);
    }

    if (segment?.content) {
      setContent(segment.content);
    } else {
      setContent(null);
    }
  }, [segment?.meta, segment?.content]);

  const fileSuffix = useMemo(() => {
    const suffixFromUrl = FileUtils.getFileTypeFromURI(resolvedFileUrl || (file as string));
    const suffixFromName = FileUtils.getFileTypeFromURI(props.fileName || "");
    return suffixFromUrl || suffixFromName;
  }, [file, props.fileName, resolvedFileUrl]);

  const fileType = useMemo(() => {
    if (["txt", "md", "json", "log", "csv"].includes(fileSuffix)) {
      return "text";
    }
    if (["html", "xml", "svg"].includes(fileSuffix)) {
      return "html";
    }
    if (["pdf"].includes(fileSuffix)) {
      return "pdf";
    }
    if (["pptx", "ppt"].includes(fileSuffix)) {
      return "pptx";
    }
    if (["docx", "doc"].includes(fileSuffix)) {
      return "docx";
    }
    if (["xlsx", "xls"].includes(fileSuffix)) {
      return "excel";
    }
    if (IMAGE_FILE_TYPES.includes(fileSuffix)) {
      return "image";
    }
    if (VIDEO_FILE_TYPES.includes(fileSuffix)) {
      return "video";
    }
    if (AUDIO_FILE_TYPES.includes(fileSuffix)) {
      return "audio";
    }
    return "unknown";
  }, [fileSuffix]);

  const getFileData = useCallback(
    async (
      fileInput: string | ArrayBuffer | File | Blob,
    ): Promise<ArrayBuffer> => {
      try {
        if (fileInput instanceof ArrayBuffer) {
          return Promise.resolve(fileInput);
        }
        if (fileInput instanceof File || fileInput instanceof Blob) {
          return await fileInput.arrayBuffer();
        }
        if (typeof fileInput === "string") {
          const authHeaders = AgentAppsAuth.getAuthHeaders();
          const headers = new Headers();

          Object.entries(authHeaders).forEach(([key, value]) => {
            if (value) {
              headers.set(key, value);
            }
          });

          const response = await fetch(fileInput, {
            headers: headers.keys().next().done ? undefined : headers,
            signal: FileUtils.timeoutSignal(5 * 60 * 1000),
          });
          if (!response.ok) {
            throw new Error(`Network response was not ok: ${response.status}`);
          }
          return await response.arrayBuffer();
        }
        throw new Error("Unsupported file input type");
      } catch (err) {
        throw new Error(
          `Failed to read file: ${err instanceof Error ? err.message : String(err)}`,
        );
      }
    },
    [],
  );

  useEffect(() => {
    if (!resolvedFileUrl) {
      setFileData(null);
      setPreviewError(null);
      return;
    }
    setLoading(true);
    setPreviewError(null);
    getFileData(resolvedFileUrl as string | ArrayBuffer | File | Blob)
      .then((data) => {
        setFileData(data);
        setLoading(false);
      })
      .catch(() => {
        const uiMessage = t("knowledge.loadingFailedReload");
        message.error(uiMessage);
        setPreviewError(uiMessage);
        setLoading(false);
        setFileData(null);
      });
  }, [resolvedFileUrl, getFileData]);

  useEffect(() => {
    if (!fileData || (fileType !== "image" && fileType !== "video" && fileType !== "audio")) {
      setMediaObjectUrl("");
      return;
    }

    const objectUrl = URL.createObjectURL(
      new Blob([fileData], {
        type: MEDIA_MIME_TYPES[fileSuffix] || undefined,
      }),
    );
    setMediaObjectUrl(objectUrl);

    return () => {
      URL.revokeObjectURL(objectUrl);
    };
  }, [fileData, fileSuffix, fileType]);

  const renderLoading = useMemo(() => {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2">
        <Spin spinning={loading} />
        <p className="text-gray-500">{t("knowledge.dataLoading")}</p>
      </div>
    );
  }, [loading]);

  const renderEmpty = useMemo(() => {
    return <Empty description={previewError || t("common.noData")} />;
  }, [previewError]);

  const renderFile = useMemo(() => {
    if (!fileData) {
      return null;
    }
    switch (fileType) {
      case "text":
        return <RenderTxt fileData={fileData} content={content} />;
      case "html":
        return <RenderHtml fileData={fileData} content={content} />;
      case "pdf":
        return (
          <RenderPdf
            className="scroll-container"
            style={{
              height: "calc(100vh - 150px)",
            }}
            fileData={fileData}
            metadata={meta}
            content={content}
          />
        );
      case "docx":
        return (
          <RenderWord
            fileData={fileData}
            content={content}
          />
        );
      case "excel":
        return (
          <RenderExcel
            fileData={fileData}
            fileType={fileType}
            metadata={meta}
            content={content}
          />
        );
      case "pptx":
        return <RenderPpt fileData={fileData} />;
      case "image":
        return mediaObjectUrl ? (
          <div className="file-viewer-media-container">
            <img
              alt={props.fileName}
              className="file-viewer-media file-viewer-image"
              src={mediaObjectUrl}
            />
          </div>
        ) : null;
      case "video":
        return mediaObjectUrl ? (
          <div className="file-viewer-media-container">
            <video
              className="file-viewer-media file-viewer-video"
              controls
              preload="metadata"
              src={mediaObjectUrl}
              title={props.fileName}
            />
          </div>
        ) : null;
      case "audio":
        return mediaObjectUrl ? (
          <div className="file-viewer-media-container">
            <audio
              className="file-viewer-media file-viewer-audio"
              controls
              preload="metadata"
              src={mediaObjectUrl}
              title={props.fileName}
            />
          </div>
        ) : null;
      case "unknown":
      default:
        return (
          <div
            style={{
              display: "flex",
              justifyContent: "center",
              alignItems: "center",
              height: "200px",
              color: "#ff4d4f",
              fontSize: "14px",
            }}
          >
            {t("knowledge.previewUnsupported")}
          </div>
        );
      }
  }, [fileData, content, fileType, mediaObjectUrl, meta, props.fileName]);

  return (
    <div className="file-viewer-container">
      <div className="file-viewer-content">
        {loading && renderLoading}
        {!loading && !fileData && renderEmpty}
        {!loading && fileData && renderFile}
      </div>
    </div>
  );
};

export default FileViewer;
