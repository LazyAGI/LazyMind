import type { ReactNode } from "react";
import type { TFunction } from "i18next";
import { getTreeSelectLabelText } from "./treeSelectUtils";
import { parseManualFeishuTargetValue } from "../../utils/feishuTarget";

export function getFeishuTargetDisplayText(
  value: unknown,
  label: ReactNode,
  _t: TFunction,
) {
  const labelText = getTreeSelectLabelText(label);
  if (labelText) {
    return labelText;
  }
  const parsed = parseManualFeishuTargetValue(`${value || ""}`);
  return parsed?.targetRef || `${value || ""}`.trim();
}

export function getFeishuTargetValuePath(
  value: unknown,
  label: ReactNode,
  pathMap: Map<string, string>,
  t: TFunction,
) {
  const normalizedValue = `${value || ""}`.trim();
  return (
    pathMap.get(normalizedValue) ||
    getFeishuTargetDisplayText(value, label, t) ||
    normalizedValue
  );
}
