import { useState, useEffect, useCallback, useMemo } from "react";
import { Button, Checkbox, Tabs, message } from "antd";
import {
  DownloadOutlined,
  FileTextOutlined,
  CloseOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useTaskCenterStore, type ConversationArtifact } from "@/modules/chat/store/taskCenter";
import { openConversationArtifactPanel } from "@/modules/chat/constants/chat";
import {
  artifactFileKey,
  artifactSourceKey,
  formatFileSize,
  toArtifactFiles,
  type ArtifactScope,
  downloadArtifactToDisk,
  downloadArtifactZip,
} from "./artifactFiles";
import "./index.scss";

const EMPTY_ARTIFACTS: ConversationArtifact[] = [];

interface Props {
  sessionId: string;
  historyId: string;
  onClose?: () => void;
  onLayoutChange?: () => void;
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
    (state) => state.artifactsByConversation[sessionId] ?? EMPTY_ARTIFACTS,
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

  const downloadSingleToDisk = useCallback(
    async (filename: string, id: string) => {
      const file = files.find((item) => artifactFileKey(item) === id);
      if (!file) return;
      const ok = await downloadArtifactToDisk(file);
      if (!ok) {
        message.error(
          t("chat.artifactCollectorDownloadFailed", { filename }),
        );
      }
    },
    [files, t],
  );

  const downloadSelected = useCallback(async () => {
    const targetFiles = files.filter((file) =>
      selected.has(artifactFileKey(file)),
    );
    if (targetFiles.length === 1) {
      const ok = await downloadArtifactToDisk(targetFiles[0]);
      if (!ok) {
        message.error(
          t("chat.artifactCollectorDownloadFailed", {
            filename: targetFiles[0].filename,
          }),
        );
      }
      return;
    }
    setDownloading(true);
    try {
      const { failed } = await downloadArtifactZip(targetFiles);
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
  }, [files, selected, t]);

  const openPanel = useCallback(() => {
    openConversationArtifactPanel({
      conversationId: sessionId,
      historyId,
    });
    onClose?.();
  }, [historyId, onClose, sessionId]);

  const selectedCount = files.filter((file) =>
    selected.has(artifactFileKey(file)),
  ).length;

  return (
    <div className="artifact-collector">
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
                    <span className="artifact-collector__file-meta">
                      {t(artifactSourceKey(file.sourceType))} · v{file.revision}
                      {file.size != null && file.size > 0
                        ? ` · ${formatFileSize(file.size)}`
                        : ""}
                    </span>
                  </div>
                  <Button
                    type="link"
                    size="small"
                    icon={<DownloadOutlined />}
                    onClick={() => downloadSingleToDisk(file.filename, key)}
                    className="artifact-collector__file-download"
                    title={`${t("chat.artifactCollectorDownload")} ${file.filename}`}
                  />
                </div>
              );
            })}
          </div>

          <div className="artifact-collector__footer">
            <Button type="link" onClick={openPanel}>
              {t("chat.artifactPanelOpenFromCollector")}
            </Button>
            <Button
              type="primary"
              onClick={() => void downloadSelected()}
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
