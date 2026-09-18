import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import {
  beginCloudLogin,
  getCloudSession,
  isCloudBusinessAvailable,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT,
} from "@/runtime/cloud/session";
import {
  closeCloudLoginPopup,
  openCloudLogin,
  reserveCloudLoginPopup,
} from "@/runtime/desktopBridge";
import {
  fetchCloudTokenPlan,
  type CloudTokenPlan,
} from "../cloudUsageApi";

type PageState = "loading" | "signed_out" | "inactive" | "active" | "error";

export function useCloudUsageSettings() {
  const { i18n } = useTranslation();
  const [state, setState] = useState<PageState>("loading");
  const [plan, setPlan] = useState<CloudTokenPlan | null>(null);
  const [loginLoading, setLoginLoading] = useState(false);
  const requestID = useRef(0);
  const requestAbort = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    const currentRequest = ++requestID.current;
    requestAbort.current?.abort();
    const controller = new AbortController();
    requestAbort.current = controller;
    setPlan(null);
    setState("loading");
    try {
      const session = await getCloudSession();
      if (currentRequest !== requestID.current || controller.signal.aborted) return;
      if (!isCloudBusinessAvailable(session)) {
        if (["authorizing", "exchanging", "restoring", "refreshing"].includes(session.state)) {
          setState("loading");
        } else if (session.configured === true && ["unknown", "checking"].includes(session.reachability || "unknown")) {
          setState("loading");
        } else if (session.configured === true && session.reachability === "unreachable") {
          setState("error");
        } else {
          setState(session.state === "offline" ? "error" : "signed_out");
        }
        return;
      }
      const nextPlan = await fetchCloudTokenPlan(controller.signal);
      if (currentRequest !== requestID.current || controller.signal.aborted) return;
      setPlan(nextPlan);
      setState(nextPlan.status);
    } catch (error) {
      if (currentRequest === requestID.current && !controller.signal.aborted) {
        setState(readHTTPStatus(error) === 401 ? "signed_out" : "error");
      }
    }
  }, []);

  useEffect(() => {
    void load();
    const refresh = () => void load();
    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    window.addEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refresh);
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refreshWhenVisible);
    return () => {
      requestID.current += 1;
      requestAbort.current?.abort();
      window.removeEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refresh);
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, [load]);

  const startLogin = async () => {
    const popup = reserveCloudLoginPopup();
    if (popup === null) {
      setState("error");
      return;
    }
    setLoginLoading(true);
    try {
      const login = await beginCloudLogin();
      const result = await openCloudLogin(login.authorization_url, popup);
      if (!result.ok) throw result.error || new Error(result.reason);
    } catch {
      closeCloudLoginPopup(popup);
      setState("error");
    } finally {
      setLoginLoading(false);
    }
  };

  const locale = i18n.language === "zh-CN" ? "zh-CN" : "en-US";
  const numberFormat = new Intl.NumberFormat(locale);

  const usageByModel = new Map(plan?.usage.map((item) => [item.publicModelKey, item]));
  const hasMissingUsage = plan?.usage.some((item) => item.missingUsageCount > 0) ?? false;
  const rows = plan?.modelQuotas.map((quota) => ({ ...quota, usage: usageByModel.get(quota.publicModelKey) })) ?? [];
  return { state, plan, loginLoading, load, startLogin, numberFormat, hasMissingUsage, rows };
}

function readHTTPStatus(error: unknown) {
  if (!error || typeof error !== "object" || !("response" in error)) return null;
  const response = (error as { response?: unknown }).response;
  if (!response || typeof response !== "object" || !("status" in response)) return null;
  const status = (response as { status?: unknown }).status;
  return typeof status === "number" ? status : null;
}
