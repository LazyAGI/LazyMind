import { useEffect, useRef, useState } from "react";
import { Alert, Button, Space, message } from "antd";
import { useTranslation } from "react-i18next";
import { axiosInstance, BASE_URL } from "@/components/request";

export interface ConfigurationAction {
  id: string;
  history_id: string;
  service: string;
  label: string;
  status: string;
  version: number;
}

export function configurationPath(service: string): string {
  if (service.startsWith("mcp:")) return "/settings?section=mcp";
  if (service.split("/")[0] === "mail") return "/cloud-documents/mail";
  if (service === "googledrive") return "/cloud-documents/google-drive";
  if (service === "feishu") return "/cloud-documents/feishu";
  if (service === "notion") return "/cloud-documents";
  return "/settings?section=system_tools";
}

export default function ToolConfigurationCard({ conversationId, historyId, active, onContinue }: {
  conversationId: string;
  historyId: string;
  active: boolean;
  onContinue?: () => void;
}) {
  const { t } = useTranslation();
  const [actions, setActions] = useState<ConfigurationAction[]>([]);
  const generation = useRef(0);
  const [opening, setOpening] = useState<string>();
  const url = `${BASE_URL}/api/core/conversations/${encodeURIComponent(conversationId)}/tool-configuration-actions`;

  const read = async () => {
    const response = await axiosInstance.get<{ data: { actions: ConfigurationAction[] } }>(url, {
      params: { history_id: historyId },
    });
    return response.data.data.actions;
  };

  useEffect(() => {
    generation.current += 1;
    setActions([]);
    if (!conversationId || !historyId) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const refresh = async () => {
      try {
        const next = await read();
        if (disposed) return;
        setActions((previous) => next.map((item) => {
          const old = previous.find((candidate) => candidate.id === item.id);
          return old && old.version > item.version ? old : item;
        }));
        if (active || next.some((item) => !["ready", "forbidden"].includes(item.status))) {
          timer = setTimeout(refresh, 5000);
        }
      } catch {
        // Do not turn an unavailable status endpoint into a false authorization prompt.
        if (!disposed && active) timer = setTimeout(refresh, 10000);
      }
    };
    const onUpdate = (event: Event) => {
      const detail = (event as CustomEvent).detail;
      if (detail?.conversationId === conversationId && detail?.historyId === historyId) {
        clearTimeout(timer);
        void refresh();
      }
    };
    window.addEventListener("tool-configuration-updated", onUpdate);
    void refresh();
    return () => {
      disposed = true;
      generation.current += 1;
      clearTimeout(timer);
      window.removeEventListener("tool-configuration-updated", onUpdate);
    };
  }, [conversationId, historyId, active]);

  const open = async (action: ConfigurationAction) => {
    const requestGeneration = generation.current;
    setOpening(action.id);
    // Reserve the tab during the user gesture; the status check may outlive popup activation.
    const destination = window.open("about:blank", "_blank");
    if (destination) destination.opener = null;
    try {
      const latest = await read();
      if (requestGeneration !== generation.current) { destination?.close(); return; }
      setActions(latest);
      const current = latest.find((item) => item.id === action.id);
      if (!current || ["ready", "forbidden", "unavailable"].includes(current.status)) {
        destination?.close();
        return;
      }
      const path = current.service.startsWith("mcp:") && current.status === "needs_authorization"
        ? `/oauth/mcp/connect?${new URLSearchParams({ server: current.service.slice(4), conversation: conversationId })}`
        : configurationPath(current.service);
      if (destination) destination.location.replace(path);
      else window.location.assign(path);
    } catch {
      destination?.close();
      message.error(t("toolConfiguration.unavailable"));
    } finally {
      if (requestGeneration === generation.current) setOpening(undefined);
    }
  };

  return <Space direction="vertical" style={{ width: "100%" }}>
    {actions.map((action) => <Alert key={action.id} showIcon
      type={action.status === "ready" ? "success" : "info"}
      message={action.label}
      description={t(`toolConfiguration.${action.status === "ready" ? "ready" : action.status === "forbidden" ? "forbidden" : action.status === "unavailable" ? "unavailable" : "pending"}`)}
      action={action.status === "ready" && !active && onContinue
        ? <Button size="small" onClick={onContinue}>{t("toolConfiguration.continue")}</Button>
        : !["ready", "forbidden", "unavailable"].includes(action.status) && <Button
        size="small" loading={opening === action.id} onClick={() => void open(action)}>
        {t(`toolConfiguration.${action.service.split("/")[0] === "mail" ? "connectMail" : action.status === "needs_authorization" ? "authorize" : action.status === "needs_tool_selection" ? "selectTools" : "configure"}`)}
      </Button>}
    />)}
  </Space>;
}
