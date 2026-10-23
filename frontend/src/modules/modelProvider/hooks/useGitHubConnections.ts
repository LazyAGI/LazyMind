import { useEffect, useRef, useState } from "react";
import { v4 as uuid } from "uuid";
import type { RawAxiosRequestConfig } from "axios";
import type { CloudConnectionResponse, GitHubCLISessionCreateBody, GitHubCLISessionResponse } from "@/api/generated/auth-client";
import { dataSourceCloudOauthApi as api } from "@/modules/dataSource/api/clients";
import { getCloudConnectionItems } from "@/modules/dataSource/mappers/dataSourceConnection";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";
import { GITHUB_DEVICE_URL, openGitHubCLIAuthorization } from "@/modules/dataSource/oauth/openManagedAuthorization";

const options: RawAxiosRequestConfig & { silentError: boolean } = { silentError: true, timeout: 15_000 };
const activeStates = new Set(["PROBING", "WAITING_USER", "VERIFYING"]);
const errorKeys: Record<string, string> = {
  "1001210": "unavailable", "1001205": "busy", "1001203": "invalidToken", "1001206": "scopeMissing",
  "1001207": "scopeMissing", "1001208": "network", "1001209": "forbidden",
  GITHUB_TOKEN_INVALID: "invalidToken", GITHUB_SCOPE_MISSING: "scopeMissing", GITHUB_SCOPE_UNVERIFIED: "scopeMissing",
  GITHUB_NETWORK_UNAVAILABLE: "network", GITHUB_ACCESS_FORBIDDEN: "forbidden",
  GITHUB_ACCOUNT_SELECTION_REQUIRED: "selectionRequired", GITHUB_CREDENTIAL_UNREADABLE: "unreadable",
  GITHUB_CLI_BUSY: "busy", GITHUB_CLI_PLATFORM_UNSUPPORTED: "unavailable",
  GITHUB_CLI_CLEANUP_FAILED: "cleanupFailed", GITHUB_CLI_RECOVERY_REQUIRED: "cleanupFailed",
  GITHUB_CLI_INTEGRITY_MISMATCH: "unavailable", GITHUB_CLI_UNAVAILABLE: "unavailable",
};
function requestError(error: unknown) {
  const response = (error as { response?: { status?: number; data?: { code?: string | number } } })?.response;
  return { status: response?.status, known: String(response?.data?.code) in errorKeys, key: errorKeys[String(response?.data?.code)] ||
    (response?.status === 403 ? "forbidden" : response?.status === 401 ? "loginRequired" : "failed") };
}

type Attempt = { body: GitHubCLISessionCreateBody; sessionId?: string; deadline: number };

export function useGitHubConnections(onChanged: (items: CloudConnectionResponse[]) => void) {
  const [accounts, setAccounts] = useState<CloudConnectionResponse[]>([]);
  const accountsRef = useRef(accounts);
  accountsRef.current = accounts;
  const [session, setSession] = useState<GitHubCLISessionResponse | null>(null);
  const [notice, setNotice] = useState("");
  const [retryCreate, setRetryCreate] = useState(false);
  const [browserBlocked, setBrowserBlocked] = useState(false);
  const [updating, setUpdating] = useState<string[]>([]);
  const onChange = useRef(onChanged);
  onChange.current = onChanged;
  const mounted = useRef(true);
  const generation = useRef(0);
  const listGeneration = useRef(0);
  const attempt = useRef<Attempt | null>(null);
  const busy = useRef(false);
  const mutations = useRef(new Set<string>());
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const opened = useRef("");

  function stopPolling() { clearTimeout(timer.current); }
  function current(version: number) { return mounted.current && generation.current === version; }
  function publish(items: CloudConnectionResponse[]) { accountsRef.current = items; setAccounts(items); onChange.current(items); }

  async function refresh() {
    const version = ++listGeneration.current;
    try {
      const response = await api.listConnectionsApiAuthserviceV1CloudConnectionsGet({ provider: "github", status: null }, options);
      const items = getCloudConnectionItems(response.data).filter(item => item.provider === "github" && item.status !== "REVOKED");
      if (mounted.current && version === listGeneration.current) publish(items);
      return items;
    } catch {
      if (mounted.current && version === listGeneration.current) setNotice("refreshFailed");
      return [];
    }
  }

  function finish(status: string, key = "") {
    stopPolling(); busy.current = false; attempt.current = null;
    setSession({ session_id: "", status }); setNotice(key); setBrowserBlocked(false); setRetryCreate(false);
  }

  async function openBrowser() {
    const version = generation.current;
    const result = await openGitHubCLIAuthorization(GITHUB_DEVICE_URL);
    if (current(version) && busy.current) setBrowserBlocked(!result.ok);
  }

  async function receive(value: GitHubCLISessionResponse, version: number) {
    if (!current(version) || !attempt.current) return;
    if (!value || !value.session_id || (attempt.current.sessionId && value.session_id !== attempt.current.sessionId)) {
      finish("FAILED", "failed"); return;
    }
    attempt.current.sessionId = value.session_id;
    if (value.status === "COMPLETED") {
      if (!value.connection_id) { finish("FAILED", "failed"); return; }
      finish("COMPLETED");
      const items = await refresh();
      if (current(version) && !items.some(item => item.connection_id === value.connection_id && item.status === "ACTIVE")) {
        setNotice("refreshFailed");
      }
      return;
    }
    if (["CANCELLED", "EXPIRED", "FAILED"].includes(value.status)) {
      finish(value.status, value.status === "CANCELLED" ? "cancelled" : value.status === "EXPIRED" ? "expired" : errorKeys[value.error_code || ""] || "failed");
      return;
    }
    if (!activeStates.has(value.status)) { finish("FAILED", "failed"); return; }
    const expiry = Date.parse(value.expires_at || "");
    if (Number.isFinite(expiry)) attempt.current.deadline = Math.min(attempt.current.deadline, expiry);
    if (Date.now() >= attempt.current.deadline) { finish("EXPIRED", "expired"); return; }
    if (value.status === "WAITING_USER" &&
      (value.verification_uri !== GITHUB_DEVICE_URL || !/^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(value.user_code || ""))) {
      finish("FAILED", "failed"); return;
    }
    // Whitelist presentation fields: never retain unexpected credentials from a response.
    setSession({ session_id: value.session_id, status: value.status,
      ...(value.status === "WAITING_USER" ? { user_code: value.user_code, verification_uri: GITHUB_DEVICE_URL } : {}) });
    setNotice(""); setRetryCreate(false);
    if (value.status === "WAITING_USER" && opened.current !== value.session_id) {
      opened.current = value.session_id;
      void openBrowser();
    }
    timer.current = setTimeout(() => { void poll(version); }, 1000);
  }

  async function poll(version: number) {
    if (!current(version) || !attempt.current?.sessionId) return;
    if (Date.now() >= attempt.current.deadline) { finish("EXPIRED", "expired"); return; }
    try {
      const response = await api.getGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdGet({ sessionId: attempt.current.sessionId }, options);
      await receive(unwrapApiData<GitHubCLISessionResponse>(response.data), version);
    } catch (error) {
      if (!current(version)) return;
      const { status, key } = requestError(error);
      if (status && status >= 400 && status < 500 && status !== 429) finish("FAILED", status === 404 ? "expired" : key);
      else {
        setNotice("network");
        timer.current = setTimeout(() => { void poll(version); }, 2000);
      }
    }
  }

  async function create(version: number) {
    if (!attempt.current) return;
    busy.current = true; setRetryCreate(false); setNotice("");
    setSession({ session_id: "", status: "PROBING" });
    try {
      const response = await api.startGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsPost({ gitHubCLISessionCreateBody: attempt.current.body }, options);
      await receive(unwrapApiData<GitHubCLISessionResponse>(response.data), version);
    } catch (error) {
      if (!current(version)) return;
      const { status, key, known } = requestError(error);
      if (known || (status && status >= 400 && status < 500)) { finish("FAILED", key); return; }
      // The server may have accepted the request. Explicit retry must retain its attempt ID.
      busy.current = false; setSession(null); setNotice(key); setRetryCreate(true);
    }
  }

  function start(reauthorizeConnectionId = "", forceWeb = false) {
    if (busy.current || retryCreate) return;
    const version = ++generation.current;
    stopPolling(); opened.current = ""; setBrowserBlocked(false);
    attempt.current = { body: { attempt_id: uuid(), reauthorize_connection_id: reauthorizeConnectionId, force_web: forceWeb }, deadline: Date.now() + 900_000 };
    return create(version);
  }
  function retry() {
    if (!attempt.current || busy.current) return;
    if (Date.now() >= attempt.current.deadline) { finish("EXPIRED", "expired"); return; }
    return create(++generation.current);
  }
  async function cancel() {
    if (!attempt.current?.sessionId || !busy.current) return;
    stopPolling(); const version = ++generation.current;
    try {
      const response = await api.cancelGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdDelete({ sessionId: attempt.current.sessionId }, options);
      await receive(unwrapApiData<GitHubCLISessionResponse>(response.data), version);
    } catch {
      if (current(version)) { setNotice("cancelFailed"); timer.current = setTimeout(() => { void poll(version); }, 1000); }
    }
  }

  async function changeAccount(connection: CloudConnectionResponse, enabled?: boolean) {
    const id = connection.connection_id;
    if (mutations.current.has(id)) return;
    mutations.current.add(id); setUpdating([...mutations.current]); setNotice("");
    try {
      if (enabled === undefined) {
        const response = await api.deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete({ connectionId: id }, options);
        const result = unwrapApiData<{ connection_id: string; status: string }>(response.data);
        if (result.connection_id !== id || result.status !== "REVOKED") throw new Error("Invalid disconnect response");
        if (!mounted.current) return;
        if (attempt.current && (!attempt.current.body.reauthorize_connection_id || attempt.current.body.reauthorize_connection_id === id)) {
          ++generation.current; finish("CANCELLED", "cancelled");
        }
        ++listGeneration.current;
        publish(accountsRef.current.filter(item => item.connection_id !== id));
      } else {
        const response = await api.patchConnectionApiAuthserviceV1CloudConnectionsConnectionIdPatch({ connectionId: id,
          cloudConnectionUpdateBody: { chat_enabled: enabled, chatEnabled: enabled } }, options);
        const result = unwrapApiData<CloudConnectionResponse>(response.data);
        if (result.connection_id !== id || result.provider !== "github") throw new Error("Invalid connection response");
        if (mounted.current) {
          ++listGeneration.current;
          publish(accountsRef.current.map(item => item.connection_id === id ? result : item));
        }
      }
    } catch (error) { if (mounted.current) setNotice(requestError(error).key); }
    finally { mutations.current.delete(id); if (mounted.current) setUpdating([...mutations.current]); }
  }

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; ++generation.current; ++listGeneration.current; stopPolling(); };
  }, []);

  return { accounts, refresh, session, notice, retryCreate, browserBlocked, updating, start, retry, cancel, openBrowser, changeAccount,
    active: Boolean(session && activeStates.has(session.status)) };
}
export type GitHubConnectionsVm = ReturnType<typeof useGitHubConnections>;
