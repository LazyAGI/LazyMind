import { useState, useEffect, useCallback, useMemo } from "react";
import { Button, Checkbox, Tabs, message } from "antd";
import {
  DownloadOutlined,
  FileTextOutlined,
  CloseOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import JSZip from "@progress/jszip-esm";
import { downloadStream } from "@/modules/chat/utils/download";
import {
  useTaskCenterStore,
  type ConversationArtifact,
} from "@/modules/chat/store/taskCenter";
import {
  basenameFromPath,
  resolveCoreAssetUrl,
} from "@/modules/knowledge/utils/imageUrl";
import "./index.scss";

const encoder = new TextEncoder();

interface ArtifactFile {
  id: string;
  triggerHistoryId?: string;
  filename: string;
  size?: number;
  url?: string;
  /** Original artifact reference for text-type blob downloads. */
  artifact: ConversationArtifact;
}

type ArtifactScope = "turn" | "conversation";

interface Props {
  sessionId: string;
  historyId: string;
  onClose?: () => void;
  onLayoutChange?: () => void;
}

function formatFileSize(bytes?: number): string {
  if (bytes == null || bytes <= 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function extractTextContent(a: ConversationArtifact): string {
  const v = a.value;
  if (!v) return "";
  if (a.content_type === "json") {
    try {
      return JSON.stringify(v.data ?? v, null, 2);
    } catch {
      return String(v.data ?? v ?? "");
    }
  }
  return v.text ?? "";
}

function artifactFileKey(file: ArtifactFile): string {
  return file.id;
}

function toArtifactFiles(artifacts: ConversationArtifact[]): ArtifactFile[] {
  return artifacts.flatMap<ArtifactFile>((artifact): ArtifactFile[] => {
    const common = {
      id: artifact.artifact_id,
      triggerHistoryId: artifact.history_id,
      artifact,
    };
    if (artifact.content_type === "file") {
      const url = resolveCoreAssetUrl(artifact.value?.url || "");
      return url
        ? [{
            ...common,
            filename:
              artifact.filename || artifact.value?.filename || artifact.slot || "file",
            size: artifact.value?.size,
            url,
          }]
        : [];
    }
    if (artifact.content_type === "image") {
      const source = artifact.value?.url || artifact.value?.path || "";
      const url = resolveCoreAssetUrl(source);
      return url
        ? [{
            ...common,
            filename: basenameFromPath(source || artifact.slot),
            url,
          }]
        : [];
    }
    if (artifact.content_type === "file_list") {
      const paths: string[] = Array.isArray(artifact.value?.paths)
        ? artifact.value.paths.filter(
            (path: unknown): path is string => typeof path === "string",
          )
        : [];
      return paths.flatMap((path, pathIndex) => {
        const url = resolveCoreAssetUrl(path);
        return url
          ? [{
              ...common,
              id: `${artifact.artifact_id}:${pathIndex}`,
              filename: basenameFromPath(path),
              url,
            }]
          : [];
      });
    }
    if (artifact.content_type === "text" || artifact.content_type === "json") {
      const filename = artifact.filename || (
        artifact.slot?.includes(".")
          ? artifact.slot
          : `${artifact.slot || "artifact"}.txt`
      );
      return [{
        ...common,
        filename,
        size: new Blob([extractTextContent(artifact)]).size,
      }];
    }
    return [];
  });
}

export default function ArtifactCollectorCard({
  sessionId,
  historyId,
  onClose,
  onLayoutChange,
}: Props) {
  const { t } = useTranslation();
  const title = t("chat.artifactCollectorTitle");
  const description = t("chat.artifactCollectorDescription");
  const [scope, setScope] = useState<ArtifactScope>("turn");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [downloading, setDownloading] = useState(false);
  const artifacts = useTaskCenterStore(
    (state) => state.artifactsByConversation[sessionId] ?? [],
  );
  const loadConversationArtifacts = useTaskCenterStore(
    (state) => state.loadConversationArtifacts,
  );
  const allFiles = useMemo(() => toArtifactFiles(artifacts), [artifacts]);

  const turnFiles = useMemo(
    () => allFiles.filter((file) => file.triggerHistoryId === historyId),
    [allFiles, historyId],
  );
  const files = scope === "turn" ? turnFiles : allFiles;

  useEffect(() => {
    setScope("turn");
  }, [historyId]);

  useEffect(() => {
    setSelected(new Set(files.map(artifactFileKey)));
  }, [files]);

  // Switching from an empty turn to a populated conversation changes the popup
  // height substantially. Ask the Popover to realign after the new layout lands.
  useEffect(() => {
    const frame = window.requestAnimationFrame(() => onLayoutChange?.());
    return () => window.cancelAnimationFrame(frame);
  }, [scope, files.length, onLayoutChange]);

  // Refresh signed URLs when the card opens; render the existing store snapshot
  // immediately so opening the popup never causes a second loading state.
  useEffect(() => {
    void loadConversationArtifacts(sessionId);
  }, [sessionId, loadConversationArtifacts]);

  const toggleSelect = useCallback((idx: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) next.delete(idx);
      else next.add(idx);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    setSelected((prev) => {
      const visibleKeys = files.map(artifactFileKey);
      const allVisibleSelected = visibleKeys.every((key) => prev.has(key));
      if (allVisibleSelected) return new Set();
      return new Set(visibleKeys);
    });
  }, [files]);

  const downloadSingle = useCallback(
    async (f: ArtifactFile): Promise<Uint8Array | null> => {
      if (f.url) {
        // File artifact: fetch from server.
        try {
          const resp = await fetch(f.url);
          if (!resp.ok) return null;
          return new Uint8Array(await resp.arrayBuffer());
        } catch {
          return null;
        }
      }
      // Text artifact: encode directly.
      return encoder.encode(extractTextContent(f.artifact));
    },
    [],
  );

  const downloadSingleToDisk = useCallback(
    async (f: ArtifactFile) => {
      const data = await downloadSingle(f);
      if (!data) {
        message.error(
          t("chat.artifactCollectorDownloadFailed", { filename: f.filename }),
        );
        return;
      }
      const blob = new Blob([data], { type: "application/octet-stream" });
      downloadStream(blob, f.filename);
    },
    [downloadSingle, t],
  );

  const downloadZip = useCallback(async (targetFiles: ArtifactFile[]) => {
    if (targetFiles.length === 0) return;
    setDownloading(true);
    try {
      const zip = new JSZip();
      const usedNames = new Set<string>();
      const failed: string[] = [];
      for (const f of targetFiles) {
        const data = await downloadSingle(f);
        if (!data) {
          failed.push(f.filename);
          continue;
        }
        const safeOriginal = (f.filename || "artifact").replace(/[\\/]/g, "_");
        const dot = safeOriginal.lastIndexOf(".");
        const base = dot > 0 ? safeOriginal.slice(0, dot) : safeOriginal;
        const ext = dot > 0 ? safeOriginal.slice(dot) : "";
        let name = safeOriginal;
        let suffix = 1;
        while (usedNames.has(name)) {
          name = `${base} (${suffix})${ext}`;
          suffix += 1;
        }
        usedNames.add(name);
        zip.file(name, data);
      }
      if (usedNames.size === 0) throw new Error("No artifact could be downloaded");
      const blob = await zip.generateAsync({ type: "blob" });
      downloadStream(blob, "artifacts.zip");
      if (failed.length > 0) {
        message.warning(
          t("chat.artifactCollectorPartialFailed", { count: failed.length }),
        );
      }
    } catch {
      message.error(t("chat.artifactCollectorBatchFailed"));
    } finally {
      setDownloading(false);
    }
  }, [downloadSingle, t]);

  const downloadSelected = useCallback(() => {
    const targetFiles = files.filter((file) =>
      selected.has(artifactFileKey(file)),
    );
    if (targetFiles.length === 1) {
      void downloadSingleToDisk(targetFiles[0]);
      return;
    }
    void downloadZip(targetFiles);
  }, [files, selected, downloadSingleToDisk, downloadZip]);

  const selectedCount = files.filter((file) =>
    selected.has(artifactFileKey(file)),
  ).length;

  return (
    <div className="artifact-collector">
      {/* Header */}
      <div className="artifact-collector__header">
        <div className="artifact-collector__header-top">
          <div className="artifact-collector__title-area">
            {title && (
              <h3 className="artifact-collector__title">{title}</h3>
            )}
            {description && (
              <p className="artifact-collector__description">
                {description}
              </p>
            )}
          </div>
          <div className="artifact-collector__meta">
            {onClose && (
              <Button
                type="text"
                size="small"
                icon={<CloseOutlined />}
                onClick={onClose}
                className="artifact-collector__close-btn"
              />
            )}
          </div>
        </div>
      </div>

      <Tabs
        className="artifact-collector__tabs"
        activeKey={scope}
        onChange={(key) => setScope(key as ArtifactScope)}
        items={[
          {
            key: "turn",
            label: `${t("chat.artifactCollectorCurrentTurnTab")} (${turnFiles.length})`,
          },
          {
            key: "conversation",
            label: `${t("chat.artifactCollectorConversationTab")} (${allFiles.length})`,
          },
        ]}
      />

      {files.length === 0 ? (
        <div className="artifact-collector__body">
          <p className="artifact-collector__empty-text">
            {t(
              scope === "turn"
                ? "chat.artifactCollectorNoFilesCurrentTurn"
                : "chat.artifactCollectorNoFilesConversation",
            )}
          </p>
        </div>
      ) : (
        <>
          <div className="artifact-collector__select-all">
            <Checkbox
              checked={selectedCount === files.length}
              indeterminate={
                selectedCount > 0 && selectedCount < files.length
              }
              onChange={toggleSelectAll}
            >
              {t("chat.artifactCollectorSelectAll")}
            </Checkbox>
            <span className="artifact-collector__count">
              {files.length} {t("chat.artifactCollectorFiles")}
            </span>
          </div>

          <div className="artifact-collector__body">
            {files.map((file) => {
              const key = artifactFileKey(file);
              return (
                <div
                  key={key}
                  className={`artifact-collector__file-item${selected.has(key) ? " is-selected" : ""}`}
                >
                  <Checkbox
                    checked={selected.has(key)}
                    onChange={() => toggleSelect(key)}
                    className="artifact-collector__checkbox"
                  />
                  <span
                    className="artifact-collector__file-icon"
                    aria-hidden="true"
                  >
                    <FileTextOutlined />
                  </span>
                  <div className="artifact-collector__file-info">
                    <span
                      className="artifact-collector__file-name"
                      title={file.filename}
                    >
                      {file.filename}
                    </span>
                    {file.size != null && file.size > 0 && (
                      <span className="artifact-collector__file-size">
                        {formatFileSize(file.size)}
                      </span>
                    )}
                  </div>
                  <Button
                    type="link"
                    size="small"
                    icon={<DownloadOutlined />}
                    onClick={() => downloadSingleToDisk(file)}
                    className="artifact-collector__file-download"
                    title={`${t("chat.artifactCollectorDownload")} ${file.filename}`}
                  />
                </div>
              );
            })}
          </div>

          <div className="artifact-collector__footer">
            <Button
              type="primary"
              onClick={downloadSelected}
              disabled={selectedCount === 0}
              loading={downloading}
            >
              {t("chat.artifactCollectorDownloadSelected")} ({selectedCount})
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
