import { useEffect, useMemo, useRef, useState, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { useImmer } from "use-immer";
import SegmentList, { SegmentListImperativeProps } from "../SegmentList";
import { Segment } from "@/api/generated/knowledge-client";
import { from, expand, EMPTY, scan, takeWhile, map } from "rxjs";
import { message, Modal, Switch } from "antd";
import { SegmentServiceApi } from "@/modules/knowledge/utils/request";
import { CARD_PAGE_SIZE } from "@/modules/knowledge/constants/common";

const MAX_SIZE = 100;

interface DocumentDetail {
  dataset_id?: string;
  document_id?: string;
}

interface SegmentTabProps {
  detail: DocumentDetail;
  names?: string[];
  editable?: boolean;
  type?: string;
  onGetItemInfo?: (info: Segment) => void;
}

const SegmentTab = (props: SegmentTabProps) => {
  const { t } = useTranslation();
  const { detail, names = [], editable = false, type, onGetItemInfo } = props;

  const [currentType, setCurrentType] = useState(type || names[0] || "");
  const [segments, setSegments] = useImmer<Segment[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [nextPageToken, setNextPageToken] = useState("");
  const [searchParams] = useSearchParams();
  const segmentListRef = useRef<SegmentListImperativeProps>(null);
  const [loading, setLoading] = useState(false);
  const [showSequence, setShowSequence] = useState(true);

  const canEdit = false;

  const segmentNumber = useMemo(() => {
    return searchParams.get("number")
      ? Number(searchParams.get("number")) + 2
      : CARD_PAGE_SIZE;
  }, [searchParams]);

  const segmentId = useMemo(() => {
    return searchParams.get("segement_id") || "";
  }, [searchParams]);

  const fetchSegmentsData = useCallback(
    async (tp: string, limit: number, token = "", isLoading = true) => {
      try {
        if (isLoading) {
          setLoading(true);
        }
        const res = await SegmentServiceApi()
          .segmentServiceSearchSegments({
            dataset: detail.dataset_id || "",
            document: detail.document_id || "",
            searchSegmentsRequest: {
              parent: "",
              group: tp,
              page_size: limit,
              page_token: token,
            },
          })
          .finally(() => {
            setLoading(false);
          });
        return {
          segments: res.data.segments || [],
          nextPageToken: res.data.next_page_token || "",
        };
      } catch (error) {
        setLoading(false);
        console.error("Error fetching segments:", error);
        throw error;
      }
    },
    [detail],
  );

  const loadSegmentsData = useCallback(
    (targetNumber: number, targetType: string) => {
      if (targetNumber > CARD_PAGE_SIZE) {
        const initialLimit = Math.min(targetNumber, MAX_SIZE);
        let lastResponse: {
          segments: Segment[];
          nextPageToken: string;
        } | null = null;
        let accumulatedCount = 0;

        from(fetchSegmentsData(targetType, initialLimit))
          .pipe(
            expand((res) => {
              const result = res as {
                segments: Segment[];
                nextPageToken: string;
              };
              lastResponse = result;
              accumulatedCount += result.segments.length;

              const remaining = targetNumber - accumulatedCount;
              if (
                !result.nextPageToken ||
                remaining <= 0 ||
                result.segments.length === 0
              ) {
                return EMPTY;
              }

              const nextLimit = Math.min(MAX_SIZE, remaining);
              return from(
                fetchSegmentsData(targetType, nextLimit, result.nextPageToken),
              );
            }),
            scan((acc, res) => {
              const result = res as {
                segments: Segment[];
                nextPageToken: string;
              };
              lastResponse = result;
              return [...acc, ...result.segments];
            }, [] as Segment[]),
            takeWhile((all) => (all as Segment[]).length < targetNumber, true),
            map((all) => {
              const result = (all as Segment[]).slice(0, targetNumber);
              return result;
            }),
          )
          .subscribe({
            next: (data) => {
              setSegments(data);
              if (lastResponse) {
                setNextPageToken(lastResponse.nextPageToken);
                setHasMore(
                  !!lastResponse.nextPageToken && data.length === targetNumber,
                );
              }
            },
            error: (error) => {
              console.error("Error in batch data loading:", error);
            },
          });
      } else {
        from(fetchSegmentsData(targetType, CARD_PAGE_SIZE)).subscribe({
          next: (result) => {
            setSegments(result.segments);
            setNextPageToken(result.nextPageToken);
            setHasMore(!!result.nextPageToken);
          },
          error: (error) => {
            console.error("Error in page data loading:", error);
          },
        });
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [fetchSegmentsData],
  );

  const loadMoreSegments = useCallback(() => {
    if (!hasMore || !nextPageToken) {
      return;
    }

    from(
      fetchSegmentsData(currentType, CARD_PAGE_SIZE, nextPageToken, false),
    ).subscribe({
      next: (result) => {
        setSegments((prev) => [...prev, ...result.segments]);
        setNextPageToken(result.nextPageToken);
        setHasMore(!!result.nextPageToken);
      },
      error: (error) => {
        console.error("Error loading more segments:", error);
      },
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentType, nextPageToken, hasMore, fetchSegmentsData]);

  useEffect(() => {
    if (!detail?.dataset_id || !detail?.document_id) {
      return;
    }

    const newCurrentType = type || (names && names[0]) || "";
    setCurrentType(newCurrentType);

    setNextPageToken("");
    setHasMore(false);

    loadSegmentsData(segmentNumber, newCurrentType);
  }, [
    detail?.dataset_id,
    detail?.document_id,
    names,
    editable,
    type,
    segmentNumber,
    loadSegmentsData,
  ]);

  const isDifferentFromLocal = useCallback(
    (serverSegments: Segment[]) => {
      if (serverSegments.length !== segments.length) {
        return true;
      }

      return serverSegments.some((serverSeg, idx) => {
        const localSeg = segments[idx];
        return (
          serverSeg.segment_id !== localSeg?.segment_id ||
          serverSeg.is_active !== localSeg?.is_active ||
          serverSeg.content !== localSeg?.content
        );
      });
    },
    [segments],
  );

  const onDeleteSegment = useCallback(
    (segment: Segment) => {
      Modal.confirm({
        title: t("knowledge.deletePermissionTitle"),
        content: t("knowledge.segmentDeleteConfirm", { number: segment.number }),
        centered: true,
        okType: "danger",
        okText: t("common.confirm"),
        cancelText: t("common.cancel"),
        onOk() {
          setSegments((draft) => {
            const index = draft.findIndex(
              (s) => s.segment_id === segment.segment_id,
            );
            if (index > -1) {
              draft.splice(index, 1);
            }
          });

          SegmentServiceApi()
            .segmentServiceDeleteSegment({
              dataset: segment.dataset_id || "",
              group: currentType,
              document: segment.document_id || "",
              segment: segment.segment_id || "",
            })
            .then(() => {
              message.success(t("knowledge.segmentDeleteSuccess"));

              fetchSegmentsData(currentType, segments.length - 1).then(
                (result) => {
                  if (isDifferentFromLocal(result.segments)) {
                    setSegments(result.segments);
                    setNextPageToken(result.nextPageToken);
                    setHasMore(!!result.nextPageToken);
                  }
                },
              );
            });
        },
      });
    },
    [
      currentType,
      segments,
      setSegments,
      fetchSegmentsData,
      isDifferentFromLocal,
    ],
  );

  const onSplitTypeChanged = useCallback(
    (newType: string) => {
      setCurrentType(newType);
      setNextPageToken("");
      setHasMore(false);
      loadSegmentsData(segments.length || CARD_PAGE_SIZE, newType);
    },
    [segments.length, loadSegmentsData],
  );

  const onUpdateSegmentStatus = useCallback(
    (targetSegmentId: string, isActive: boolean, apiPromise: Promise<void>) => {
      setSegments((draft) => {
        const segment = draft.find((s) => s.segment_id === targetSegmentId);
        if (segment) {
          segment.is_active = isActive;
        }
      });

      apiPromise
        .then(() => {
          return fetchSegmentsData(currentType, segments.length, "", false);
        })
        .then((result) => {
          if (isDifferentFromLocal(result.segments)) {
            setSegments(result.segments);
            setNextPageToken(result.nextPageToken);
            setHasMore(!!result.nextPageToken);
          }
        })
        .catch((error) => {
          console.error("更新状态失败:", error);
          setSegments((draft) => {
            const segment = draft.find((s) => s.segment_id === targetSegmentId);
            if (segment) {
              segment.is_active = !isActive;
            }
          });
        });
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [currentType, segments.length, fetchSegmentsData, isDifferentFromLocal],
  );

  const handleFetchMore = useCallback(
    (isMore: boolean) => {
      if (isMore && hasMore) {
        loadMoreSegments();
      } else {
        loadSegmentsData(segments.length || CARD_PAGE_SIZE, currentType);
      }
    },
    [hasMore, loadMoreSegments, segments.length, currentType, loadSegmentsData],
  );

  return (
    <div className="flex flex-1 flex-col overflow-hidden" style={{ height: '100%' }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-end",
          gap: 8,
          marginBottom: 8,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexShrink: 0 }}>
          <span style={{ color: "var(--color-text-description)" }}>
            {t("knowledge.sequence")}
          </span>
          <Switch
            size="small"
            checked={showSequence}
            onChange={setShowSequence}
          />
        </div>
      </div>

      <SegmentList
        ref={segmentListRef}
        segments={segments}
        group={currentType}
        onDelete={onDeleteSegment}
        onRefresh={() => handleFetchMore(false)}
        onUpdateStatus={onUpdateSegmentStatus}
        editable={canEdit}
        fetchSegments={handleFetchMore}
        hasMoreSegment={hasMore}
        contentReadOnly={!canEdit}
        onGetItemInfo={onGetItemInfo}
        loading={loading}
        scrollToId={segmentId}
        showNumber={showSequence}
      />
    </div>
  );
};

export default SegmentTab;
