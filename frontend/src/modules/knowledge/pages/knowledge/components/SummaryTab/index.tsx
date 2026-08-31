import { Doc } from '@/api/generated/core-client';
import { Segment } from '@/api/generated/knowledge-client';

import SegmentTab from '../SegmentTab';

interface SummaryTabProps {
  detail: Doc;
  type: string;
  onGetItemInfo?: (data: Segment) => void;
  onAskSegment?: (segment: Segment, selectedText?: string, group?: string) => void;
  showSequence?: boolean;
}

/** Document summary tab; delegates to SegmentTab for parity with split tabs. */
const SummaryTab = (props: SummaryTabProps) => {
  const { detail, type, onGetItemInfo, onAskSegment, showSequence } = props;

  return (
    <SegmentTab
      detail={detail}
      type={type}
      names={[type]}
      editable={false}
      onGetItemInfo={onGetItemInfo}
      onAskSegment={onAskSegment}
      showSequence={showSequence}
    />
  );
};

export default SummaryTab;
