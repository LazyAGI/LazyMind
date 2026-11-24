import {
  ClockCircleOutlined,
  DeleteOutlined,
  EyeOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Descriptions,
  Empty,
  Modal,
  Popconfirm,
  Skeleton,
  Tag,
  message,
} from "antd";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  deleteEpisode,
  getEpisode,
  type EpisodeRecord,
} from "../../episodeApi";
import { createEpisodePager } from "../../episodePagination";
import { sortEpisodesByRecordedTime } from "../../episodeViewModel";
import { getMemorySourceLabelKey } from "../../memorySourceLabels";

import MemoryPagination from "../MemoryPagination";

const PAGE_SIZE = 5;

const getEpisodeTypeTone = (episodeType: string) => {
  const normalized = episodeType.trim().toLowerCase();

  if (["decision", "decided"].includes(normalized)) {
    return "decision";
  }
  if (["progress", "discussion", "discussed"].includes(normalized)) {
    return "progress";
  }
  if (["result", "completed", "success"].includes(normalized)) {
    return "result";
  }
  if (["blocked", "blocker", "failed"].includes(normalized)) {
    return "blocked";
  }
  return "neutral";
};

export default function EpisodeMemorySection() {
  const { i18n, t } = useTranslation();
  const detailRequestIdRef = useRef(0);
  const [episodes, setEpisodes] = useState<EpisodeRecord[]>([]);
  const [totalSize, setTotalSize] = useState(0);
  const [pageIndex, setPageIndex] = useState(0);
  const [displayedPageIndex, setDisplayedPageIndex] = useState(0);
  const [pager] = useState(() => createEpisodePager(PAGE_SIZE));
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detail, setDetail] = useState<EpisodeRecord | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [deletingIds, setDeletingIds] = useState<Set<string>>(new Set());

  useEffect(() => {
    let ignore = false;

    setLoading(true);
    setLoadError("");
    void pager.load(pageIndex + 1)
      .then((page) => {
        if (ignore) {
          return;
        }
        const lastPageIndex = Math.max(0, Math.ceil(page.totalSize / PAGE_SIZE) - 1);
        if (pageIndex > lastPageIndex) {
          setPageIndex(lastPageIndex);
          return;
        }
        setEpisodes(sortEpisodesByRecordedTime(page.items));
        setTotalSize(page.totalSize);
        setDisplayedPageIndex(pageIndex);
        setLoading(false);
      })
      .catch((error) => {
        if (ignore) {
          return;
        }
        console.error("Load Episode memory failed:", error);
        setLoadError(getLocalizedErrorMessage(error));
        setLoading(false);
      });

    return () => {
      ignore = true;
    };
  }, [pageIndex, reloadKey, pager]);

  const formatDateTime = useCallback(
    (timestampMs: number) =>
      timestampMs
        ? new Intl.DateTimeFormat(i18n.resolvedLanguage || i18n.language, {
            dateStyle: "medium",
            timeStyle: "short",
          }).format(new Date(timestampMs))
        : t("admin.memoryEpisodeUnknownTime"),
    [i18n.language, i18n.resolvedLanguage, t],
  );

  const getEpisodeTypeLabel = useCallback(
    (episodeType: string) => {
      const normalized = episodeType.trim().toLowerCase();

      if (["decision", "decided"].includes(normalized)) {
        return t("admin.memoryEpisodeTypeDecision");
      }
      if (["progress", "discussion", "discussed"].includes(normalized)) {
        return t("admin.memoryEpisodeTypeProgress");
      }
      if (["result", "completed", "success"].includes(normalized)) {
        return t("admin.memoryEpisodeTypeResult");
      }
      if (["blocked", "blocker", "failed"].includes(normalized)) {
        return t("admin.memoryEpisodeTypeBlocked");
      }
      if (["event", "episode"].includes(normalized)) {
        return t("admin.memoryEpisodeTypeOther");
      }

      return episodeType || t("admin.memoryEpisodeTypeOther");
    },
    [t],
  );

  const getEpisodeSourceLabel = useCallback(
    (sourceKind: string) => {
      const translationKey = getMemorySourceLabelKey(sourceKind);
      return translationKey ? t(translationKey) : sourceKind;
    },
    [t],
  );

  const handleOpenDetail = async (episode: EpisodeRecord) => {
    const requestId = detailRequestIdRef.current + 1;
    detailRequestIdRef.current = requestId;
    setDetailOpen(true);
    setDetail(episode);
    setDetailError("");
    setDetailLoading(true);

    try {
      const nextDetail = await getEpisode(episode.id);
      if (detailRequestIdRef.current === requestId) {
        setDetail(nextDetail);
      }
    } catch (error) {
      if (detailRequestIdRef.current === requestId) {
        console.error("Load Episode detail failed:", error);
        setDetailError(getLocalizedErrorMessage(error));
      }
    } finally {
      if (detailRequestIdRef.current === requestId) {
        setDetailLoading(false);
      }
    }
  };

  const handleDelete = async (episode: EpisodeRecord) => {
    setDeletingIds((current) => new Set(current).add(episode.id));
    try {
      await deleteEpisode(episode.id);
      pager.clearCursors();
      setEpisodes((current) =>
        current.filter((item) => item.id !== episode.id),
      );
      setTotalSize((current) => Math.max(0, current - 1));
      if (episodes.length === 1 && pageIndex > 0) {
        setPageIndex((current) => Math.max(0, current - 1));
      } else {
        setReloadKey((value) => value + 1);
      }
      if (detail?.id === episode.id) {
        setDetailOpen(false);
        setDetail(null);
      }
      message.success(t("admin.memoryEpisodeDeleteSuccess"));
    } catch (error) {
      console.error("Delete Episode memory failed:", error);
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setDeletingIds((current) => {
        const next = new Set(current);
        next.delete(episode.id);
        return next;
      });
    }
  };

  return (
    <section
      className="memory-episode-section"
      aria-label={t("admin.memoryEpisodeTitle")}
    >
      <div className="memory-episode-heading">
        <div className="memory-experience-section-heading">
          <span className="memory-experience-section-icon">
            <ClockCircleOutlined />
          </span>
          <div>
            <h3>{t("admin.memoryEpisodeTitle")}</h3>
            <p>{t("admin.memoryEpisodeDescription")}</p>
          </div>
        </div>
        <span className="memory-episode-total">
          {t("admin.memoryEpisodeTotal", { count: totalSize })}
        </span>
      </div>

      {loading && !episodes.length ? (
        <div className="memory-episode-loading" aria-busy="true">
          <Skeleton active paragraph={{ rows: 3 }} />
        </div>
      ) : loadError ? (
        <Alert
          action={
            <Button
              size="small"
              onClick={() => setReloadKey((value) => value + 1)}
            >
              {t("common.retry")}
            </Button>
          }
          description={loadError}
          message={t("admin.memoryEpisodeLoadFailed")}
          showIcon
          type="error"
        />
      ) : !episodes.length ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memoryEpisodeEmpty")}
        />
      ) : (
        <>
          <div className="memory-episode-list" aria-busy={loading}>
            {episodes.map((episode) => {
              const tone = getEpisodeTypeTone(episode.episodeType);
              const deleting = deletingIds.has(episode.id);

              return (
                <article className="memory-episode-card" key={episode.id}>
                  <button
                    type="button"
                    className="memory-episode-card-main"
                    aria-label={t("admin.memoryEpisodeViewDetail")}
                    onClick={() => void handleOpenDetail(episode)}
                  >
                    <span
                      className="memory-episode-time"
                      title={t("admin.memoryEpisodeRecordedAt")}
                    >
                      {formatDateTime(episode.recordedAtMs)}
                    </span>
                    <span className="memory-episode-card-copy">
                      <span className="memory-episode-card-meta">
                        <Tag className={`memory-episode-type is-${tone}`}>
                          {getEpisodeTypeLabel(episode.episodeType)}
                        </Tag>
                        {episode.sourceKind ? (
                          <span>
                            {getEpisodeSourceLabel(episode.sourceKind)}
                          </span>
                        ) : null}
                      </span>
                      <strong>
                        {episode.summary ||
                          t("admin.memoryEpisodeNoSummary")}
                      </strong>
                    </span>
                    <span className="memory-episode-hit-count">
                      {t("admin.memoryEpisodeHitCount", {
                        count: episode.hitCount,
                      })}
                    </span>
                    <EyeOutlined className="memory-episode-view-icon" />
                  </button>
                  <Popconfirm
                    cancelText={t("common.cancel")}
                    description={t(
                      "admin.memoryEpisodeDeleteConfirmDescription",
                    )}
                    okButtonProps={{ danger: true, loading: deleting }}
                    okText={t("common.delete")}
                    title={t("admin.memoryEpisodeDeleteConfirmTitle")}
                    onConfirm={() => handleDelete(episode)}
                  >
                    <Button
                      aria-label={t("admin.memoryEpisodeDelete")}
                      className="memory-episode-delete"
                      danger
                      disabled={loading || deleting}
                      icon={<DeleteOutlined />}
                      loading={deleting}
                      size="small"
                      type="text"
                    />
                  </Popconfirm>
                </article>
              );
            })}
          </div>

          <MemoryPagination
            page={displayedPageIndex + 1}
            pageSize={PAGE_SIZE}
            total={totalSize}
            disabled={loading || deletingIds.size > 0}
            label={t("admin.memoryEpisodeTitle")}
            onChange={page => setPageIndex(page - 1)}
          />
        </>
      )}

      <Modal
        destroyOnClose
        footer={null}
        open={detailOpen}
        title={t("admin.memoryEpisodeDetailTitle")}
        width={720}
        onCancel={() => {
          detailRequestIdRef.current += 1;
          setDetailOpen(false);
          setDetailError("");
        }}
      >
        {detailLoading && !detail ? (
          <Skeleton active paragraph={{ rows: 5 }} />
        ) : detailError ? (
          <Alert
            description={detailError}
            message={t("admin.memoryEpisodeDetailLoadFailed")}
            showIcon
            type="error"
          />
        ) : detail ? (
          <div className="memory-episode-detail">
            <div className="memory-episode-detail-hero">
              <span className="memory-experience-section-icon">
                <ClockCircleOutlined />
              </span>
              <div>
                <Tag
                  className={`memory-episode-type is-${getEpisodeTypeTone(
                    detail.episodeType,
                  )}`}
                >
                  {getEpisodeTypeLabel(detail.episodeType)}
                </Tag>
                <h4>
                  {detail.summary || t("admin.memoryEpisodeNoSummary")}
                </h4>
                <p>{formatDateTime(detail.recordedAtMs)}</p>
              </div>
            </div>
            {detailLoading ? <Skeleton active paragraph={{ rows: 1 }} /> : null}
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label={t("admin.memoryEpisodeSummary")}>
                {detail.summary || t("admin.memoryEpisodeNoSummary")}
              </Descriptions.Item>
              <Descriptions.Item label={t("admin.memoryEpisodeSourceKind")}>
                {detail.sourceKind
                  ? getEpisodeSourceLabel(detail.sourceKind)
                  : "—"}
              </Descriptions.Item>
              <Descriptions.Item label={t("admin.memoryEpisodeConversationId")}>
                {detail.conversationId || "—"}
              </Descriptions.Item>
              <Descriptions.Item label={t("admin.memoryEpisodeOccurredAt")}>
                {formatDateTime(detail.occurredAtMs)}
              </Descriptions.Item>
              <Descriptions.Item label={t("admin.memoryEpisodeRecordedAt")}>
                {formatDateTime(detail.recordedAtMs)}
              </Descriptions.Item>
              <Descriptions.Item label={t("admin.memoryEpisodeHitCountLabel")}>
                {detail.hitCount}
              </Descriptions.Item>
              <Descriptions.Item label={t("admin.memoryEpisodeId")}>
                <code>{detail.id}</code>
              </Descriptions.Item>
            </Descriptions>
          </div>
        ) : null}
      </Modal>
    </section>
  );
}
