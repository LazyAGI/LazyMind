import { Tag } from "antd";
import { useTranslation } from "react-i18next";
import type { DatasetItemSource } from "../shared";
import { sourceColorMap, sourceLabelI18nKeys } from "../shared";

interface SourceTypeTagProps {
  source?: DatasetItemSource;
}

export default function SourceTypeTag({ source }: SourceTypeTagProps) {
  const { t } = useTranslation();

  if (!source) {
    return <Tag>-</Tag>;
  }
  return <Tag color={sourceColorMap[source]}>{t(sourceLabelI18nKeys[source])}</Tag>;
}
