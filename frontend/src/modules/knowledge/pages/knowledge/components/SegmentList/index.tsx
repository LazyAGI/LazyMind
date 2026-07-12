import { Empty } from "antd";
import { forwardRef, useImperativeHandle, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Segment } from "@/api/generated/knowledge-client";
import { Virtuoso } from "react-virtuoso";

import SegmentCard from "../SegmentCard";
import SegmentDetailModal, {
  ISegmentDetailModalRef,
} from "../SegmentDetailModal";
import Rendering from "@/modules/knowledge/components/Rendering";
import "./index.scss";

export interface SegmentListImperativeProps {
  openDetail: (data: Segment, group: string) => void;
}

interface IProps {
  segments: Segment[];
  group: string;
  editable: boolean;
  hasMoreSegment: boolean;
  onDelete?: (segment: Segment) => void;
  onRefresh: () => void;
  onUpdateStatus?: (
    segmentId: string,
    isActive: boolean,
    apiPromise: Promise<void>,
  ) => void;
  fetchSegments: (isMore: boolean) => void;
  contentReadOnly: boolean;
  onGetItemInfo?: (segment: Segment) => void;
  loading?: boolean;
  scrollToId?: string;
  showNumber?: boolean;
}

const SegmentList = forwardRef<SegmentListImperativeProps, IProps>(
  (props, ref) => {
    const { t } = useTranslation();
    const {
      segments,
      group,
      editable,
      onDelete,
      onRefresh,
      onUpdateStatus,
      fetchSegments,
      hasMoreSegment,
      contentReadOnly = false,
      onGetItemInfo,
      loading = false,
      scrollToId,
      showNumber = true,
    } = props;
    const segmentDetailRef = useRef<ISegmentDetailModalRef>(null);
    useImperativeHandle(ref, () => ({
      openDetail,
    }));

    function openDetail(data: Segment, name: string) {
      if (onGetItemInfo) {
        onGetItemInfo(data);
      } else if (contentReadOnly) {
        segmentDetailRef.current?.handleOpen(data, name);
      }
    }

    if (loading) {
      return <Rendering text={t("common.loading")} />;
    }

    if (!segments || segments.length < 1) {
      return (
        <Empty
          description={t("knowledge.noContent")}
          style={{ marginTop: 80 }}
        />
      );
    }

    return (
      <div
        className="segmentList"
        id={`scrollableDiv-${group}`}
        style={{
          height: editable ? "calc(100% - 40px)" : "100%",
        }}
      >
        <Virtuoso
          style={{ height: "100%", width: "100%" }}
          totalCount={segments.length}
          className="segmentList-virtuoso scroll-container"
          initialTopMostItemIndex={
            scrollToId
              ? Math.max(
                  0,
                  segments.findIndex((item) => item.segment_id === scrollToId),
                )
              : 0
          }
          endReached={() => {
            if (hasMoreSegment) {
              fetchSegments(true);
            }
          }}
          data={segments}
          defaultItemHeight={50}
          itemContent={(index) => {
            const segment = segments[index];
            return (
              <SegmentCard
                key={segment.segment_id}
                segment={segment}
                group={group}
                onDelete={() => onDelete?.(segment)}
                onOpenDetail={() => openDetail(segment, group)}
                onRefresh={onRefresh}
                onUpdateStatus={onUpdateStatus}
                editable={editable}
                contentReadOnly={contentReadOnly}
                showNumber={showNumber}
              />
            );
          }}
        />
        <SegmentDetailModal
          ref={segmentDetailRef}
          onClose={onRefresh}
          editable={editable}
        />
      </div>
    );
  },
);

SegmentList.displayName = "SegmentList";

export default SegmentList;
