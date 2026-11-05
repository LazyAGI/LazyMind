import { useEffect, useRef, useState } from "react";
import { v4 as uuid } from "uuid";
import type { RawAxiosRequestConfig } from "axios";
import type { CloudConnectionResponse, GitHubCLISessionCreateBody, GitHubCLISessionResponse } from "@/api/generated/auth-client";
import { dataSourceCloudOauthApi as api } from "@/modules/dataSource/api/clients";
import { getCloudConnectionItems } from "@/modules/dataSource/mappers/dataSourceConnection";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";
import { GITHUB_DEVICE_URL, openGitHubCLIAuthorization } from "@/modules/dataSource/oauth/openManagedAuthorization";
import { AgentAppsAuth, AUTH_LOGOUT_EVENT, AUTH_USER_CHANGE_EVENT } from "@/components/auth";

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
    (response?.status === 403 ? "forbidden" : response?.status === 401 ? "loginRequired" :
      [502, 503, 504].includes(response?.status || 0) ? "serviceUnavailable" : !response ? "requestUncertain" : "failed") };
}

type Attempt = { body: GitHubCLISessionCreateBody; sessionId?: string; deadline: number };
const recoveryKey = "lazymind.github-cli.attempt.v1";

async function identityDigest(identity: string): Promise<string | null> {
  try {
    if (!AgentAppsAuth.getUserInfo()?.userId) return null;
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(identity));
    return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
  } catch { return null; }
}

function clearRecovery(attemptId?: string) {
  try {
    if (attemptId && JSON.parse(sessionStorage.getItem(recoveryKey) || "null")?.body?.attempt_id !== attemptId) return;
    sessionStorage.removeItem(recoveryKey);
  } catch { /* Storage is optional; authorization remains usable in this page. */ }
}

function readRecovery(): { identity: string; body: GitHubCLISessionCreateBody; deadline: number } | null {
  try {
    const stored = JSON.parse(sessionStorage.getItem(recoveryKey) || "null");
    if (!stored) return null;
    const body = stored.body;
    if (!/^[a-f0-9]{64}$/.test(stored.identity) || typeof body?.attempt_id !== "string" ||
      !body.attempt_id.length || body.attempt_id.length > 128 || typeof body.reauthorize_connection_id !== "string" ||
      body.reauthorize_connection_id.length > 64 || typeof body.force_web !== "boolean" ||
      !Number.isFinite(stored.deadline) || stored.deadline > Date.now() + 900_000) {
      clearRecovery(); return null;
    }
    return { identity: stored.identity, deadline: stored.deadline, body: {
      attempt_id: body.attempt_id, reauthorize_connection_id: body.reauthorize_connection_id, force_web: body.force_web,
    } };
  } catch { clearRecovery(); return null; }
}

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
  const identity = useRef(AgentAppsAuth.getSessionIdentity());

  function stopPolling() { clearTimeout(timer.current); }
  function sameIdentity() { return identity.current === AgentAppsAuth.getSessionIdentity(); }
  function current(version: number) { return mounted.current && generation.current === version && sameIdentity(); }
  function publish(items: CloudConnectionResponse[]) { accountsRef.current = items; setAccounts(items); onChange.current(items); }

  async function refresh() {
    const version = ++listGeneration.current;
    const owner = identity.current;
    try {
      const response = await api.listConnectionsApiAuthserviceV1CloudConnectionsGet({ provider: "github", status: null }, options);
      const items = getCloudConnectionItems(response.data).filter(item => item.provider === "github" && item.status !== "REVOKED");
      if (mounted.current && owner === identity.current && sameIdentity() && version === listGeneration.current) publish(items);
      return items;
    } catch {
      if (mounted.current && owner === identity.current && sameIdentity() && version === listGeneration.current) setNotice("refreshFailed");
      return [];
    }
  }

  function finish(status: string, key = "") {
    clearRecovery(attempt.current?.body.attempt_id);
    stopPolling(); busy.current = false; attempt.current = null;
    setSession({ session_id: "", status }); setNotice(key); setBrowserBlocked(false); setRetryCreate(false);
  }

  async function persist(version: number) {
    const pending = attempt.current;
    const digest = await identityDigest(identity.current);
    if (!digest || !pending || !current(version) || attempt.current !== pending) return;
    try {
      sessionStorage.setItem(recoveryKey, JSON.stringify({ identity: digest, body: pending.body, deadline: pending.deadline }));
    } catch { /* The active flow does not depend on browser storage. */ }
  }

  async function restore(version: number) {
    const stored = readRecovery();
    if (!stored) return;
    const digest = await identityDigest(identity.current);
    if (!current(version) || attempt.current) return;
    if (!digest || stored.identity !== digest) { clearRecovery(); return; }
    if (stored.deadline <= Date.now()) { clearRecovery(); setNotice("expired"); return; }
    attempt.current = { body: stored.body, deadline: stored.deadline };
    busy.current = true;
    setSession({ session_id: "", status: "PROBING" });
    try {
      const response = await api.getGithubCliAttemptApiAuthserviceV1CloudGithubCliAttemptsAttemptIdGet(
        { attemptId: stored.body.attempt_id }, options);
      if (!current(version)) return;
      const recovered = unwrapApiData<GitHubCLISessionResponse>(response.data);
      // Returning to the page restores progress; opening a browser again is explicit.
      opened.current = recovered?.session_id || "";
      await receive(recovered, version);
    } catch (error) {
      if (!current(version)) return;
      const { status, key } = requestError(error);
      if (status && status >= 400 && status < 500 && status !== 404 && status !== 429) { finish("FAILED", key); return; }
      busy.current = false; setSession(null); setRetryCreate(true);
      setNotice(status === 404 ? "attemptMissing" : key);
    }
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
        setNotice(status ? key : "statusUnavailable");
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
    identity.current = AgentAppsAuth.getSessionIdentity();
    stopPolling(); opened.current = ""; setBrowserBlocked(false);
    attempt.current = { body: { attempt_id: uuid(), reauthorize_connection_id: reauthorizeConnectionId, force_web: forceWeb }, deadline: Date.now() + 900_000 };
    return Promise.all([create(version), persist(version)]).then(() => undefined);
  }
  function retry() {
    if (!attempt.current || busy.current) return;
    if (Date.now() >= attempt.current.deadline) { finish("EXPIRED", "expired"); return; }
    return create(++generation.current);
  }
  async function cancel() {
    if (!attempt.current) return;
    stopPolling(); const version = ++generation.current;
    const pending = attempt.current;
    busy.current = true;
    try {
      const response = pending.sessionId
        ? await api.cancelGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdDelete({ sessionId: pending.sessionId }, options)
        : await api.cancelGithubCliAttemptApiAuthserviceV1CloudGithubCliAttemptsAttemptIdDelete({ attemptId: pending.body.attempt_id }, options);
      await receive(unwrapApiData<GitHubCLISessionResponse>(response.data), version);
    } catch {
      if (current(version)) {
        setNotice("cancelFailed");
        if (pending.sessionId) timer.current = setTimeout(() => { void poll(version); }, 1000);
        else { busy.current = false; setSession(null); setRetryCreate(true); }
      }
    }
  }

  async function changeAccount(connection: CloudConnectionResponse, enabled?: boolean) {
    const owner = identity.current;
    const id = connection.connection_id;
    if (mutations.current.has(id)) return;
    mutations.current.add(id); setUpdating([...mutations.current]); setNotice("");
    try {
      if (enabled === undefined) {
        const response = await api.deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete({ connectionId: id }, options);
        const result = unwrapApiData<{ connection_id: string; status: string }>(response.data);
        if (result.connection_id !== id || result.status !== "REVOKED") throw new Error("Invalid disconnect response");
        if (!mounted.current || owner !== identity.current || !sameIdentity()) return;
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
        if (mounted.current && owner === identity.current && sameIdentity()) {
          ++listGeneration.current;
          publish(accountsRef.current.map(item => item.connection_id === id ? result : item));
        }
      }
    } catch (error) { if (mounted.current && owner === identity.current && sameIdentity()) setNotice(requestError(error).key); }
    finally { mutations.current.delete(id); if (mounted.current) setUpdating([...mutations.current]); }
  }

  useEffect(() => {
    mounted.current = true;
    void restore(generation.current);
    const changed = (event: Event) => {
      if (event.type !== AUTH_LOGOUT_EVENT && sameIdentity()) return;
      ++generation.current; ++listGeneration.current; stopPolling(); clearRecovery();
      identity.current = AgentAppsAuth.getSessionIdentity();
      attempt.current = null; busy.current = false;
      setSession(null); setRetryCreate(false); setBrowserBlocked(false); setNotice(""); publish([]);
    };
    window.addEventListener(AUTH_USER_CHANGE_EVENT, changed);
    window.addEventListener(AUTH_LOGOUT_EVENT, changed);
    const storageChanged = (event: StorageEvent) => {
      if (event.storageArea === localStorage && (event.key === "lazymind:user" || event.key === null)) changed(event);
    };
    window.addEventListener("storage", storageChanged);
    return () => {
      mounted.current = false; ++generation.current; ++listGeneration.current; stopPolling();
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, changed);
      window.removeEventListener(AUTH_LOGOUT_EVENT, changed);
      window.removeEventListener("storage", storageChanged);
    };
  }, []);

  return { accounts, refresh, session, notice, retryCreate, browserBlocked, updating, start, retry, cancel, openBrowser, changeAccount,
    active: Boolean(session && activeStates.has(session.status)) };
}
export type GitHubConnectionsVm = ReturnType<typeof useGitHubConnections>;
