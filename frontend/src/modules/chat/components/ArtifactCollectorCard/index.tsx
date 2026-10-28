import { getLocalizedErrorMessage } from "@/components/request";
import { useState, useEffect, useCallback, useMemo, useRef } from "react";
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
import { toArtifactFiles, extractTextContent, formatFileSize, type ArtifactFile } from './artifactFiles';
import { useArtifactPreview } from '../ArtifactPreview/context';
import { filePreviewTarget } from '../ArtifactPreview/target';
import "./index.scss";

const encoder = new TextEncoder();

type ArtifactScope = "turn" | "conversation";

interface Props {
  sessionId: string;
  historyId: string;
  currentExportIds?: string[];
  previewArtifactId?: string;
  onPreview?: (artifact: ConversationArtifact, file: ArtifactFile) => void;
  onClose?: () => void;
  onLayoutChange?: () => void;
}

const EMPTY_ARTIFACTS: ConversationArtifact[] = [];
const artifactFileKey = (file: ArtifactFile) => file.id;

export default function ArtifactCollectorCard({
  sessionId,
  historyId,
  currentExportIds,
  previewArtifactId,
  onPreview,
  onClose,
  onLayoutChange,
}: Props) {
  const { t } = useTranslation();
  const preview = useArtifactPreview();
  const title = t("chat.artifactCollectorTitle");
  const description = t("chat.artifactCollectorDescription");
  const [scope, setScope] = useState<ArtifactScope>("turn");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [downloading, setDownloading] = useState(false);
  const artifacts = useTaskCenterStore(
    (state) => state.artifactsByConversation[sessionId] ?? EMPTY_ARTIFACTS,
  );
  const loadConversationArtifacts = useTaskCenterStore(
    (state) => state.loadConversationArtifacts,
  );
  const allFiles = useMemo(() => toArtifactFiles(artifacts), [artifacts]);

  const turnFiles = useMemo(
    () => allFiles.filter((file) => file.artifact.history_id === historyId),
    [allFiles, historyId],
  );
  const files = scope === "turn" ? turnFiles : allFiles;

  useEffect(() => {
    setScope("turn");
  }, [historyId]);

  const selectionScope = `${sessionId}:${historyId}:${scope}`;
  const previousFiles = useRef<{ scope: string; ids: Set<string> }>({ scope: '', ids: new Set() });
  useEffect(() => {
    const ids = new Set(files.map(artifactFileKey));
    const previous = previousFiles.current;
    setSelected(current => new Set([...ids].filter(id =>
      previous.scope !== selectionScope || !previous.ids.has(id) || current.has(id))));
    previousFiles.current = { scope: selectionScope, ids };
  }, [files, selectionScope]);

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
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
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
                  <button type="button" className="artifact-collector__preview-main" aria-label={file.filename}
                    onClick={() => {
                      if (window.getSelection()?.toString()) return;
                      if (onPreview) onPreview(file.artifact, file);
                      else preview?.open(filePreviewTarget(file));
                    }}>
                  <span
                    className="artifact-collector__file-icon"
                    aria-hidden="true"
                  >
                    <FileTextOutlined />
                  </span>
                  <div className="artifact-collector__file-info">
                    <span className="artifact-collector__file-name" title={file.filename}>{file.filename}</span>
                    {previewArtifactId === file.artifact.artifact_id && (
                      <span className="artifact-collector__preview-label">{t("chat.exportPreviewing")}</span>
                    )}
                    {file.artifact.value?.chat_export === true && file.artifact.history_id === historyId && currentExportIds !== undefined && (
                      <span className="artifact-collector__file-size">
                        {t(currentExportIds.includes(file.artifact.artifact_id)
                          ? "chat.exportCurrentGeneration"
                          : "chat.exportPreviousGeneration")}
                      </span>
                    )}
                    {file.artifact.created_at && !Number.isNaN(Date.parse(file.artifact.created_at)) && (
                      <time className="artifact-collector__file-size" dateTime={file.artifact.created_at}>
                        {t("chat.exportSavedAt")} {new Date(file.artifact.created_at).toLocaleString()}
                      </time>
                    )}
                    {file.size != null && file.size > 0 && (
                      <span className="artifact-collector__file-size">
                        {formatFileSize(file.size)}
                      </span>
                    )}
                  </div>
                  </button>
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
