import type { TFunction } from "i18next";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

export function workbuddyErrorMessage(status: DesktopAgentIntegrationStatus | undefined, t: TFunction): string {
  const code = status?.issue?.code;
  const key = code && ["unsupported_version", "version_unreadable", "capability_unavailable", "host_unavailable"].includes(code) ? code : "operation_failed";
  return t(`agentIntegration.workbuddyErrors.${key}`);
}
