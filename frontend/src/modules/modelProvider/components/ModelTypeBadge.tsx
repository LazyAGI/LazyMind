import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import {
  getModelTaskDescriptionKey,
  getModelTaskLabelKey,
  type ModelTaskType,
} from "../modelTaskTypes";
import "./ModelTypeBadge.scss";

interface ModelTypeIconProps {
  type: ModelTaskType;
  size?: number;
}

/** The nearby task label provides the accessible name; the icon is decorative. */
export function ModelTypeIcon({ type, size = 18 }: ModelTypeIconProps) {
  return (
    <img
      className="model-type-icon"
      src={`/model-type-icons/${type}.svg`}
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      draggable={false}
    />
  );
}

interface ModelTypeBadgeProps {
  type: ModelTaskType;
  compact?: boolean;
}

export function ModelTypeBadge({ type, compact = false }: ModelTypeBadgeProps) {
  const { t } = useTranslation();
  return (
    <Tooltip title={t(getModelTaskDescriptionKey(type))}>
      <span
        className={`model-type-badge${compact ? " is-compact" : ""}`}
        data-model-type={type}
      >
        <ModelTypeIcon type={type} size={compact ? 16 : 18} />
        <span className="model-type-badge-label">
          {t(getModelTaskLabelKey(type))}
        </span>
      </span>
    </Tooltip>
  );
}
