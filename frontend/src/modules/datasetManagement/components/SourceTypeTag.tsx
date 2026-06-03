import { Tag } from "antd";
import type { DatasetItemSource } from "../shared";
import { sourceColorMap, sourceLabelMap } from "../shared";

interface SourceTypeTagProps {
  source?: DatasetItemSource;
}

export default function SourceTypeTag({ source }: SourceTypeTagProps) {
  if (!source) {
    return <Tag>-</Tag>;
  }
  return <Tag color={sourceColorMap[source]}>{sourceLabelMap[source]}</Tag>;
}

