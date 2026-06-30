import type { ReactNode } from "react";
import type { TFunction } from "i18next";
import { getTreeSelectLabelText } from "./treeSelectUtils";
import { FEISHU_MANUAL_TARGET_VALUE_PREFIX } from "../../utils/feishuTarget";

export { FEISHU_MANUAL_TARGET_VALUE_PREFIX };

function parseManualFeishuTargetValue(value: unknown) {
  const normalizedValue = `${value || ""}`.trim();
  if (!normalizedValue.startsWith(`${FEISHU_MANUAL_TARGET_VALUE_PREFIX}:`)) {
    return null;
  }

  const parts = normalizedValue.split(":");
  const kind = parts[1] || "";
  const encodedTargetRef = parts.slice(2).join(":");
  if (!["current", "wiki", "drive"].includes(kind)) {
    return null;
  }

  let targetRef = encodedTargetRef;
  try {
    targetRef = decodeURIComponent(encodedTargetRef);
  } catch {
  }

  const normalizedTargetRef = targetRef.trim();
  return normalizedTargetRef ? { kind, targetRef: normalizedTargetRef } : null;
}

function formatManualFeishuTargetLabel(value: unknown, t: TFunction) {
  const parsed = parseManualFeishuTargetValue(value);
  if (!parsed) {
    return null;
  }
  if (parsed.kind === "wiki") {
    return t("admin.dataSourceUseCurrentFeishuWikiInput", {
      value: parsed.targetRef,
    });
  }
  if (parsed.kind === "drive") {
    return t("admin.dataSourceUseCurrentFeishuDriveInput", {
      value: parsed.targetRef,
    });
  }
  return parsed.targetRef;
}

export function getFeishuTargetDisplayText(
  value: unknown,
  label: ReactNode,
  t: TFunction,
) {
  const labelText = getTreeSelectLabelText(label);
  if (labelText && !labelText.startsWith(FEISHU_MANUAL_TARGET_VALUE_PREFIX)) {
    return labelText;
  }
  return formatManualFeishuTargetLabel(value, t) || labelText;
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
