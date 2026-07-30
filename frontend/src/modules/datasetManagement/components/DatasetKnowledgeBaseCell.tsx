import { Tag, Tooltip, Typography } from "antd";
import type { KnowledgeBaseOption } from "../shared";

const { Text } = Typography;

interface DatasetKnowledgeBaseCellProps {
  knowledgeBases?: KnowledgeBaseOption[];
}

function formatKnowledgeBaseLabel(item: KnowledgeBaseOption) {
  return item.name?.trim() || item.id;
}

export default function DatasetKnowledgeBaseCell({
  knowledgeBases,
}: DatasetKnowledgeBaseCellProps) {
  const items = (knowledgeBases || []).filter(
    (item) => item?.id || item?.name,
  );

  if (items.length === 0) {
    return <Text type="secondary">-</Text>;
  }

  const first = items[0];
  const firstLabel = formatKnowledgeBaseLabel(first);
  const restCount = items.length - 1;
  const fullListTitle = (
    <div className="dataset-kb-tooltip-list">
      {items.map((item) => (
        <Tag key={item.id || item.name} className="dataset-kb-tooltip-tag">
          {formatKnowledgeBaseLabel(item)}
        </Tag>
      ))}
    </div>
  );

  return (
    <div className="dataset-kb-cell">
      <Tooltip title={fullListTitle}>
        <span className="dataset-kb-tag-wrap">
          <Tag className="dataset-kb-tag">
            <span className="dataset-kb-tag-text">{firstLabel}</span>
          </Tag>
        </span>
      </Tooltip>
      {restCount > 0 ? (
        <Tooltip title={fullListTitle}>
          <span className="dataset-kb-more-wrap">
            <Tag className="dataset-kb-more-tag">+{restCount}</Tag>
          </span>
        </Tooltip>
      ) : null}
    </div>
  );
}
