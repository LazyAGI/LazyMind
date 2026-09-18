import { useCallback, useEffect, useRef, useState } from "react";
import { isDesktopRuntime } from "@/runtime/mode";
import { getCloudSession, isCloudBusinessAvailable, LAZYMIND_CLOUD_SESSION_CHANGED_EVENT } from "@/runtime/cloud/session";
import { listCloudResources, type CloudResourceItem, type CloudResourceType } from "../cloudResourceApi";

export function useCloudResources(kind: CloudResourceType, enabled = true, refreshKey = 0) {
  const [items, setItems] = useState<CloudResourceItem[]>([]);
  const [available, setAvailable] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const pending = useRef<AbortController>();
  const account = useRef("");
  const active = isDesktopRuntime() && enabled;

  const reload = useCallback(async () => {
    pending.current?.abort();
    const controller = new AbortController(); pending.current = controller;
    setError(false);
    setLoading(false);
    if (!active) { setItems([]); setAvailable(false); return }
    let businessAvailable = false;
    try {
      const session = await getCloudSession();
      if (controller.signal.aborted) return;
	  businessAvailable = isCloudBusinessAvailable(session);
	  setAvailable(businessAvailable);
	  if (!businessAvailable) { setItems([]); account.current = ""; return }
      setLoading(true);
      if (account.current !== session.account_id) setItems([]);
      account.current = session.account_id || "";
      const rows = await listCloudResources(kind, { signal: controller.signal });
      if (!controller.signal.aborted) setItems(rows);
    } catch {
      if (!controller.signal.aborted) {
        setItems([]);
        setAvailable(false);
        setError(businessAvailable);
      }
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [active, kind]);

  useEffect(() => {
    void reload();
    const changed = () => {
      pending.current?.abort();
      setItems([]);
      setAvailable(false);
      setLoading(false);
      setError(false);
      void reload();
    };
    const visible = () => { if (document.visibilityState === "visible") void reload() };
    window.addEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, changed);
    document.addEventListener("visibilitychange", visible);
    return () => {
      pending.current?.abort();
      window.removeEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, changed);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [reload, refreshKey]);
  return { items, available, loading, error, reload };
}
