import { Switch } from "antd";
import { DeleteOutlined } from "@ant-design/icons";
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
  } = props;

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
      <div className="content" onClick={onOpenDetail}>
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
        <span style={{ flex: 1 }} />
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
