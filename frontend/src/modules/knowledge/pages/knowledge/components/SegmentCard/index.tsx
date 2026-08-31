import { Button, Switch } from "antd";
import { CommentOutlined, DeleteOutlined } from "@ant-design/icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Segment } from "@/api/generated/knowledge-client";
import { SegmentServiceApi } from "@/modules/knowledge/utils/request";

import SegmentContent from "@/modules/knowledge/pages/knowledge/components/SegmentContent";
import "./index.scss";

interface IProps {
  segment: Segment;
  group: string;
  editable: boolean;
  onDelete: () => void;
  onOpenDetail: () => void;
  onRefresh: () => void;
  onUpdateStatus?: (
    segmentId: string,
    isActive: boolean,
    apiPromise: Promise<void>,
  ) => void;
  contentReadOnly: boolean;
  showNumber?: boolean;
  onAskSegment?: (segment: Segment, selectedText?: string, group?: string) => void;
}

const SegmentCard = (props: IProps) => {
  const {
    segment,
    group,
    editable,
    onDelete,
    onOpenDetail,
    onRefresh,
    onUpdateStatus,
    contentReadOnly = false,
    showNumber = true,
    onAskSegment,
  } = props;
  const { t } = useTranslation();
  const [selectedText, setSelectedText] = useState("");

  function captureSelection(event: React.MouseEvent<HTMLDivElement>) {
    const selection = window.getSelection();
    const text = selection?.toString().trim() || "";
    if (!selection || selection.rangeCount === 0 || !text) {
      setSelectedText("");
      return;
    }
    const range = selection.getRangeAt(0);
    if (event.currentTarget.contains(range.commonAncestorContainer)) {
      setSelectedText(text);
    }
  }

  function openDetailUnlessSelecting() {
    if (!window.getSelection()?.toString().trim()) {
      onOpenDetail();
    }
  }

  function onChange(checked: boolean) {
    if (onUpdateStatus) {
      const apiPromise = SegmentServiceApi()
        .segmentServiceModifyStatus({
          dataset: segment.dataset_id || "",
          document: segment.document_id || "",
          segment: segment.segment_id || "",
          modifyStatusRequest: { is_active: checked, name: "", group: group },
        })
        .then(() => {
        });

      onUpdateStatus(segment.segment_id || "", checked, apiPromise);
    } else {
      SegmentServiceApi()
        .segmentServiceModifyStatus({
          dataset: segment.dataset_id || "",
          document: segment.document_id || "",
          segment: segment.segment_id || "",
          modifyStatusRequest: { is_active: checked, name: "", group: group },
        })
        .then(() => {
          onRefresh();
        });
    }
  }

  return (
    <div
      className={`segmentCard ${showNumber ? "" : "segmentCard-no-number"}`}
      id={segment.segment_id}
      key={segment.segment_id}
    >
      {showNumber && (
        <div className="segment-number" onClick={onOpenDetail}>
          #{segment.number}
        </div>
      )}
      <div
        className="content"
        onClick={openDetailUnlessSelecting}
        onMouseUp={captureSelection}
      >
        <div
          className={`contentInner ${contentReadOnly ? "contentReadOnly" : ""} ${showNumber ? "contentWithNumber" : ""}`}
        >
          <SegmentContent
            segment={segment}
            group={group}
            editable={!contentReadOnly}
          />
        </div>
      </div>
      <div className="footer">
        {onAskSegment ? (
          <Button
            type="link"
            size="small"
            className="segment-chat-action"
            icon={<CommentOutlined />}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => onAskSegment(segment, selectedText || undefined, group)}
          >
            {t(selectedText
              ? "knowledge.askSelectedSegmentText"
              : "knowledge.askWholeSegment")}
          </Button>
        ) : <span style={{ flex: 1 }} />}
        {editable ? (
          <>
            <Switch
              defaultChecked
              onChange={onChange}
              style={{ marginRight: "5px" }}
              checked={segment.is_active}
            />
            <DeleteOutlined className="delete-icon" onClick={onDelete} />
          </>
        ) : (
          <></>
        )}
      </div>
    </div>
  );
};

export default SegmentCard;
