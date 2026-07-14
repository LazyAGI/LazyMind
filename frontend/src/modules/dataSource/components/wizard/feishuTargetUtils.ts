import type { ReactNode } from "react";
import type { TFunction } from "i18next";
import { getTreeSelectLabelText } from "./treeSelectUtils";
import { parseManualFeishuTargetValue } from "../../utils/feishuTarget";

export function getFeishuTargetDisplayText(
  value: unknown,
  label: ReactNode,
  _t: TFunction,
  titleMap?: Map<string, string>,
) {
  const normalizedValue = `${value || ""}`.trim();
  const cachedTitle = `${titleMap?.get(normalizedValue) || ""}`.trim();
  if (cachedTitle && cachedTitle !== normalizedValue) {
    return cachedTitle;
  }

  const labelText = getTreeSelectLabelText(label);
  // TreeSelect falls back to value as label once the matching tree node is gone.
  if (labelText && labelText !== normalizedValue) {
    return labelText;
  }

  if (cachedTitle) {
    return cachedTitle;
  }

  const parsed = parseManualFeishuTargetValue(normalizedValue);
  return parsed?.targetRef || labelText || normalizedValue;
}

export function getFeishuTargetValuePath(
  value: unknown,
  label: ReactNode,
  pathMap: Map<string, string>,
  t: TFunction,
  titleMap?: Map<string, string>,
) {
  const normalizedValue = `${value || ""}`.trim();
  const cachedPath = `${pathMap.get(normalizedValue) || ""}`.trim();
  if (cachedPath && cachedPath !== normalizedValue) {
    return cachedPath;
  }
  return (
    getFeishuTargetDisplayText(value, label, t, titleMap) ||
    cachedPath ||
    normalizedValue
  );
}
