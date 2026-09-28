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

  const pendingRead = useRef<{ key: string; promise: Promise<ConfigurationAction[]> }>();
  const snapshot = useRef<{ key: string; actions: ConfigurationAction[] }>();
  const refreshRef = useRef<() => Promise<ConfigurationAction[] | undefined>>();

  useEffect(() => {
    generation.current += 1;
    const key = JSON.stringify([conversationId, historyId]);
    if (snapshot.current?.key !== key) {
      snapshot.current = { key, actions: [] };
      setActions([]);
    }
    setOpening(undefined);
    if (!conversationId || !historyId) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let refreshing: Promise<ConfigurationAction[] | undefined> | undefined;
    let unavailable = false;
    const schedule = () => {
      clearTimeout(timer);
      if (disposed || document.visibilityState === "hidden") return;
      if (active || unavailable || snapshot.current?.actions.some((item) => !["ready", "forbidden"].includes(item.status))) {
        timer = setTimeout(() => { void refresh().catch(() => {}); }, active ? 5000 : 30000);
      }
    };
    const refresh = (): Promise<ConfigurationAction[] | undefined> => {
      clearTimeout(timer);
      if (refreshing) return refreshing;
      refreshing = (async () => {
        // Keep a single network request even while the card changes history or run state.
        if (pendingRead.current && pendingRead.current.key !== key) {
          await pendingRead.current.promise.catch(() => {});
        }
        if (disposed) return;
        if (!pendingRead.current) {
          const promise = axiosInstance.get<{ data: { actions: ConfigurationAction[] } }>(url, {
            params: { history_id: historyId },
          }).then((response) => response.data.data.actions).finally(() => {
            if (pendingRead.current?.promise === promise) pendingRead.current = undefined;
          });
          pendingRead.current = { key, promise };
        }
        const next = await pendingRead.current.promise;
        if (disposed) return;
        const previous = snapshot.current?.actions ?? [];
        const merged = next.map((item) => {
          const old = previous.find((candidate) => candidate.id === item.id);
          return old && old.version > item.version ? old : item;
        });
        snapshot.current = { key, actions: merged };
        setActions(merged);
        unavailable = false;
        return merged;
      })().catch((error) => {
        // Do not turn an unavailable status endpoint into a false authorization prompt.
        unavailable = true;
        throw error;
      }).finally(() => {
        refreshing = undefined;
        schedule();
      });
      return refreshing;
    };
    refreshRef.current = refresh;
    const onUpdate = (event: Event) => {
      const detail = (event as CustomEvent).detail;
      if (detail?.conversationId === conversationId && detail?.historyId === historyId) {
        void refresh().catch(() => {});
      }
    };
    const onVisibility = () => {
      clearTimeout(timer);
      if (document.visibilityState !== "hidden") void refresh().catch(() => {});
    };
    window.addEventListener("tool-configuration-updated", onUpdate);
    document.addEventListener("visibilitychange", onVisibility);
    if (document.visibilityState !== "hidden") void refresh().catch(() => {});
    return () => {
      disposed = true;
      generation.current += 1;
      refreshRef.current = undefined;
      clearTimeout(timer);
      window.removeEventListener("tool-configuration-updated", onUpdate);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [conversationId, historyId, active, url]);

  const open = async (action: ConfigurationAction) => {
    const requestGeneration = generation.current;
    setOpening(action.id);
    // Reserve the tab during the user gesture; the status check may outlive popup activation.
    const destination = window.open("about:blank", "_blank");
    if (destination) destination.opener = null;
    try {
      const latest = await refreshRef.current?.();
      if (!latest || requestGeneration !== generation.current) { destination?.close(); return; }
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
      if (requestGeneration === generation.current) message.error(t("toolConfiguration.unavailable"));
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
