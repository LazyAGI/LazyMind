import { Doc, Segment } from '@/api/generated/knowledge-client';

import SegmentTab from '../SegmentTab';

interface SummaryTabProps {
  detail: Doc;
  type: string;
  onGetItemInfo?: (data: Segment) => void;
}

/** Document summary tab; delegates to SegmentTab for parity with split tabs. */
const SummaryTab = (props: SummaryTabProps) => {
  const { detail, type, onGetItemInfo } = props;

  return (
    <SegmentTab
      detail={detail}
      type={type}
      names={[type]}
      editable={false}
      onGetItemInfo={onGetItemInfo}
    />
  );
};

export default SummaryTab;
