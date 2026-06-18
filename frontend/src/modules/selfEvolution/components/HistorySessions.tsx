import { type MouseEvent } from "react";
import { Modal, Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  DeleteOutlined,
  HistoryOutlined,
  LoadingOutlined,
  MessageOutlined,
} from "@ant-design/icons";
import { type SelfEvolutionHistoryEntry } from "./types";

const { Text } = Typography;

type HistorySessionItemProps = {
  entry: SelfEvolutionHistoryEntry;
  isDeleting: boolean;
  onSelect: (entry: SelfEvolutionHistoryEntry) => void;
  onDelete: (entry: SelfEvolutionHistoryEntry, event: MouseEvent<HTMLElement>) => void;
};

export function HistorySessionItem({
  entry,
  isDeleting,
  onSelect,
  onDelete,
}: HistorySessionItemProps) {
  const { t } = useTranslation();
  return (
    <div
      className={`self-evolution-history-modal-item${entry.isCurrent ? " is-current" : ""}`}
      role="listitem"
    >
      <button
        type="button"
        className="self-evolution-history-modal-item-select"
        onClick={() => onSelect(entry)}
        disabled={isDeleting || entry.isCurrent}
        aria-current={entry.isCurrent ? "true" : undefined}
      >
        <div className="self-evolution-history-modal-item-main">
          <div className="self-evolution-history-modal-item-title-row">
            <strong>{entry.title}</strong>
            {entry.isCurrent && (
              <span className="self-evolution-history-modal-current-badge">
                <span className="self-evolution-history-modal-current-dot" />
                {t("selfEvolutionRun.currentSession")}
              </span>
            )}
            <span className={`self-evolution-history-modal-item-badge is-${entry.source}`}>
              {entry.source === "thread" ? t("selfEvolutionRun.threadSession") : t("selfEvolutionRun.localSession")}
            </span>
          </div>
          <span className="self-evolution-history-modal-item-meta">
            {entry.threadId ? t("selfEvolutionRun.threadIdLabel", { id: entry.threadId }) : t("selfEvolutionRun.messageCount", { count: entry.messageCount || 0 })}
          </span>
        </div>
        <div className="self-evolution-history-modal-item-side">
          {entry.status && (
            <span className="self-evolution-history-modal-item-status">{entry.status}</span>
          )}
          <span>{entry.updatedAt}</span>
          <span>{entry.isCurrent ? t("selfEvolutionRun.viewing") : t("selfEvolutionRun.enter")}</span>
        </div>
      </button>
      <button
        type="button"
        className="self-evolution-history-modal-item-delete"
        onClick={(event) => onDelete(entry, event)}
        disabled={isDeleting}
        aria-label={t("selfEvolutionRun.deleteHistoryAria", { title: entry.title })}
        title={t("selfEvolutionRun.deleteHistoryTitle")}
      >
        {isDeleting ? <LoadingOutlined spin /> : <DeleteOutlined />}
      </button>
    </div>
  );
}

type HistorySessionTabProps = {
  entry: SelfEvolutionHistoryEntry;
  isDeleting: boolean;
  onSelect: (entry: Pick<SelfEvolutionHistoryEntry, "sessionId" | "threadId" | "title">) => void;
  onDelete: (entry: SelfEvolutionHistoryEntry, event: MouseEvent<HTMLElement>) => void;
};

export function HistorySessionTab({
  entry,
  isDeleting,
  onSelect,
  onDelete,
}: HistorySessionTabProps) {
  const { t } = useTranslation();
  return (
    <div className="self-evolution-history-tab" title={entry.title}>
      <button
        type="button"
        className="self-evolution-history-tab-main"
        onClick={() => onSelect({ sessionId: entry.sessionId, threadId: entry.threadId, title: entry.title })}
        disabled={isDeleting}
      >
        <span className="self-evolution-history-tab-icon">
          {entry.source === "thread" ? <HistoryOutlined /> : <MessageOutlined />}
        </span>
        <span className="self-evolution-history-tab-content">
          <span className="self-evolution-history-tab-label">{entry.title}</span>
        </span>
      </button>
      <button
        type="button"
        className="self-evolution-history-tab-delete"
        onClick={(event) => onDelete(entry, event)}
        disabled={isDeleting}
        aria-label={t("selfEvolutionRun.deleteHistoryAria", { title: entry.title })}
        title={t("selfEvolutionRun.deleteHistoryTitle")}
      >
        {isDeleting ? <LoadingOutlined spin /> : <DeleteOutlined />}
      </button>
    </div>
  );
}

export type HistorySessionModalProps = {
  open: boolean;
  threadHistoryListError: string;
  isLoadingThreadHistoryList: boolean;
  historySessionEntries: SelfEvolutionHistoryEntry[];
  deletingHistoryKeys: string[];
  onCancel: () => void;
  onRetry: () => void;
  onSelectHistorySession: (entry: SelfEvolutionHistoryEntry) => void;
  onEnterHistorySession?: (entry: SelfEvolutionHistoryEntry) => void;
  onDeleteHistorySession: (
    entry: SelfEvolutionHistoryEntry,
    event: MouseEvent<HTMLElement>,
  ) => void;
};

export function HistorySessionModal({
  open,
  threadHistoryListError,
  isLoadingThreadHistoryList,
  historySessionEntries,
  deletingHistoryKeys,
  onCancel,
  onRetry,
  onSelectHistorySession,
  onEnterHistorySession,
  onDeleteHistorySession,
}: HistorySessionModalProps) {
  const { t } = useTranslation();
  const handleSelectHistorySession = onEnterHistorySession || onSelectHistorySession;
  return (
    <Modal
      open={open}
      onCancel={onCancel}
      footer={null}
      width={720}
      centered
      className="self-evolution-history-modal"
      title={null}
    >
      <section className="self-evolution-history-modal-shell" aria-label={t("selfEvolutionRun.historyModalAria")}>
        <header className="self-evolution-history-modal-head">
          <div className="self-evolution-history-modal-copy">
            <Text className="self-evolution-history-modal-kicker">{t("selfEvolutionRun.historySessions")}</Text>
            <Typography.Title level={4} className="self-evolution-history-modal-title">
              {t("selfEvolutionRun.historyModalTitle")}
            </Typography.Title>
            <Text className="self-evolution-history-modal-subtitle">
              {t("selfEvolutionRun.historyModalSubtitle")}
            </Text>
          </div>
        </header>

        {threadHistoryListError && (
          <div className="self-evolution-history-modal-alert">
            <span>{threadHistoryListError}</span>
            <button type="button" onClick={onRetry}>
              {t("selfEvolutionRun.retry")}
            </button>
          </div>
        )}

        <div className="self-evolution-history-modal-list" role="list" aria-label={t("selfEvolutionRun.historyListAria")}>
          {isLoadingThreadHistoryList && historySessionEntries.length === 0 ? (
            <div className="self-evolution-history-modal-empty is-loading">
              <LoadingOutlined spin />
              <Text>{t("selfEvolutionRun.loadingHistory")}</Text>
            </div>
          ) : historySessionEntries.length > 0 ? (
            historySessionEntries.map((entry) => (
              <HistorySessionItem
                key={entry.key}
                entry={entry}
                isDeleting={deletingHistoryKeys.includes(entry.key)}
                onSelect={handleSelectHistorySession}
                onDelete={onDeleteHistorySession}
              />
            ))
          ) : (
            <div className="self-evolution-history-modal-empty">
              <Text>{t("selfEvolutionRun.noHistory")}</Text>
            </div>
          )}
        </div>
      </section>
    </Modal>
  );
}
