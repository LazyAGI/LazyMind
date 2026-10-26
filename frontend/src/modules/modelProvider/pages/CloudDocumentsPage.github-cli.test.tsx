import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider, message, Modal } from "antd";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudConnectionResponse, GitHubCLISessionResponse } from "@/api/generated/auth-client";
import i18n from "@/i18n";
import CloudDocumentsPage from "./CloudDocumentsPage";
import GitHubAccountPage from "./GitHubAccountPage";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), detail: vi.fn(), credentials: vi.fn(), saveCredentials: vi.fn(), authorize: vi.fn(),
  start: vi.fn(), poll: vi.fn(), cancel: vi.fn(), patch: vi.fn(), remove: vi.fn(), open: vi.fn(),
}));
vi.mock("@/runtime/cloud/session", () => ({
  getCloudSession: async () => null, isCloudBusinessAvailable: () => false,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "fixture-cloud-session-change",
}));
vi.mock("../hooks/useLocalDataSourceSettings", () => ({
  useLocalDataSourceSettings: () => ({ loading: false, canCreateLocalSource: false, localSourceCount: 0 }),
}));
vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.list,
    getConnectionApiAuthserviceV1CloudConnectionsConnectionIdGet: mocks.detail,
    getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet: mocks.credentials,
    saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut: mocks.saveCredentials,
    oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost: mocks.authorize,
    startGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsPost: mocks.start,
    getGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdGet: mocks.poll,
    cancelGithubCliSessionApiAuthserviceV1CloudGithubCliSessionsSessionIdDelete: mocks.cancel,
    patchConnectionApiAuthserviceV1CloudConnectionsConnectionIdPatch: mocks.patch,
    deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete: mocks.remove,
  },
  dataSourceProviderConnectionsApi: {},
}));

const CODE = "ABCD-EFGH"; // Deliberately fake; no live authorization in these tests.
const URL = "https://github.com/login/device";
const manual: CloudConnectionResponse = {
  connection_id: "fixture-manual", provider: "github", provider_account_id: "10001", display_name: "Fixture manual",
  auth_mode: "oauth_user", tenant_id: "", status: "ACTIVE", connection_method: "legacy_byo", token_source: "",
  created_at: "2026-01-01T00:00:00Z", can_use_chat: false, provider_options: { chat_enabled: false },
};
const cli: CloudConnectionResponse = {
  ...manual, connection_id: "fixture-cli", display_name: "Fixture CLI", token_source: "github_cli",
};
let connections: CloudConnectionResponse[];
const session = (status: string, extra: Partial<GitHubCLISessionResponse> = {}): GitHubCLISessionResponse => ({
  session_id: "fixture-session", status, expires_at: new Date(Date.now() + 900_000).toISOString(), ...extra,
});
const waiting = () => session("WAITING_USER", { user_code: CODE, verification_uri: URL });
// jsdom does not run CSS keyframes; real animations are checked in the packaged UI acceptance.
const page = (query = "") => render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[`/cloud-documents${query}`]}><Routes><Route path="/cloud-documents" element={<CloudDocumentsPage />} /><Route path="/cloud-documents/github" element={<GitHubAccountPage />} /></Routes></MemoryRouter></ConfigProvider>);

async function openConnectionDialog() {
  fireEvent.click(await screen.findByRole("button", { name: /^(连接 GitHub|添加账号)$/ }));
  const dialog = await screen.findByRole("dialog", { name: "连接 GitHub" });
  await waitFor(() => expect(dialog).toBeVisible());
  return dialog;
}
async function connect(timed = false) {
  const dialog = await openConnectionDialog();
  const button = within(dialog).getByRole("button", { name: "开始连接" });
  if (timed) vi.useFakeTimers();
  await act(async () => { fireEvent.click(button); });
  await vi.waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
  return button;
}
async function tick(milliseconds = 5000) {
  await act(async () => { await vi.advanceTimersByTimeAsync(milliseconds); });
}
function storageValues() {
  return [localStorage, sessionStorage].flatMap(storage => Array.from({ length: storage.length }, (_, i) => storage.getItem(storage.key(i)!) || "")).join("\n");
}

beforeEach(async () => {
  vi.resetAllMocks();
  const confirm = Modal.confirm;
  // Static confirmation dialogs do not inherit the page ConfigProvider in jsdom.
  vi.spyOn(Modal, "confirm").mockImplementation(options => confirm({ ...options, transitionName: "", maskTransitionName: "" }));
  vi.stubEnv("VITE_LAZYMIND_MODE", "desktop");
  localStorage.clear(); sessionStorage.clear();
  localStorage.setItem("lazymind.cloud-documents.onboarding.v2", "seen");
  await i18n.changeLanguage("zh-CN");
  connections = [];
  mocks.list.mockImplementation(async ({ provider }: { provider: string }) => ({ data: { items: provider === "github" ? connections : [] } }));
  mocks.detail.mockImplementation(async ({ connectionId }: { connectionId: string }) => ({ data: connections.find(item => item.connection_id === connectionId) }));
  mocks.credentials.mockResolvedValue({ data: { app_id: "", secret_configured: false } });
  mocks.saveCredentials.mockResolvedValue({ data: { provider: "github", app_id: "fixture-client", secret_configured: true } });
  mocks.authorize.mockResolvedValue({ data: { authorize_url: "https://github.com/login/oauth/authorize?client_id=fixture-client",
    connection_id: "fixture-manual-pending", state: "fixture-oauth-state" } });
  mocks.start.mockResolvedValue({ data: waiting() });
  mocks.poll.mockResolvedValue({ data: waiting() });
  mocks.cancel.mockResolvedValue({ data: session("CANCELLED") });
  mocks.patch.mockResolvedValue({ data: cli });
  mocks.remove.mockResolvedValue({ data: { connection_id: cli.connection_id, status: "REVOKED", deleted: true } });
  mocks.open.mockResolvedValue({ opened: true });
  Object.defineProperty(window, "lazymindDesktop", { configurable: true, value: { openGitHubCLIAuthorization: mocks.open } });
  // Any accidental use of fetch would cross the mocked generated-client boundary.
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("Unexpected network request in UI fixture"); }));
});
afterEach(() => {
  cleanup();
  act(() => { message.destroy(); Modal.destroyAll(); });
  vi.useRealTimers(); vi.unstubAllEnvs(); vi.unstubAllGlobals(); vi.restoreAllMocks();
  Reflect.deleteProperty(window, "lazymindDesktop");
});

describe("GitHub CLI from the real cloud-documents page", () => {
  it("opens two connection modes without starting authorization on open or tab changes", async () => {
    page();
    const dialog = await openConnectionDialog();
    expect(within(dialog).getByRole("tab", { name: "快捷授权" })).toHaveAttribute("aria-selected", "true");
    fireEvent.click(within(dialog).getByRole("tab", { name: /手动.*OAuth/ }));
    expect(await screen.findByLabelText("App Secret")).toBeVisible();
    fireEvent.click(within(dialog).getByRole("tab", { name: "快捷授权" }));
    expect(within(dialog).getByRole("button", { name: "开始连接" })).toBeVisible();
    expect(mocks.start).not.toHaveBeenCalled();
    expect(mocks.authorize).not.toHaveBeenCalled();
    expect(mocks.saveCredentials).not.toHaveBeenCalled();
    expect(mocks.open).not.toHaveBeenCalled();
  });

  it("keeps the hub compact and opens a separate account page with a return path", async () => {
    connections = [manual, cli];
    page();
    expect(await screen.findByRole("button", { name: "管理账号" })).toBeVisible();
    expect(screen.queryByRole("group", { name: /Fixture CLI/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: /聊天/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /手动.*OAuth|网页授权.*GitHub/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "管理账号" }));
    expect(await screen.findByRole("heading", { name: "GitHub 账号管理" })).toBeVisible();
    expect(await screen.findByRole("group", { name: /Fixture CLI/ })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /返回.*云文档|返回.*服务/ }));
    expect(await screen.findByRole("heading", { name: "云文档", level: 1 })).toBeVisible();
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it("keeps an expired unnamed account manageable without using its connection ID as its name", async () => {
    connections = [{ ...manual, display_name: "", provider_account_id: "", status: "EXPIRED" }];
    page();
    fireEvent.click(await screen.findByRole("button", { name: "管理账号" }));
    const account = await screen.findByRole("group", { name: "未命名 GitHub 账号" });
    expect(within(account).getByText(manual.connection_id)).toBeVisible();
    expect(within(account).getByRole("switch", { name: /聊天/ })).toBeDisabled();
    expect(within(account).getByRole("button", { name: /断开/ })).toBeEnabled();
  });

  it("prevents switching authentication mode while a CLI session is active", async () => {
    page(); await connect();
    await waitFor(() => expect(screen.getByText(CODE)).toBeVisible());
    expect(screen.getByRole("tab", { name: /手动.*OAuth/ })).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(screen.getByRole("tab", { name: /手动.*OAuth/ }));
    expect(screen.queryByLabelText("App Secret")).not.toBeInTheDocument();
    expect(mocks.start).toHaveBeenCalledTimes(1);
    expect(mocks.authorize).not.toHaveBeenCalled();
  });

  it("starts without OAuth App credentials or Cloud login; double click uses one attempt", async () => {
    page();
    const dialog = await openConnectionDialog();
    const button = within(dialog).getByRole("button", { name: "开始连接" });
    await act(async () => { fireEvent.click(button); fireEvent.click(button); });
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
    expect(mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody).toMatchObject({ attempt_id: expect.any(String) });
    expect(mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody.attempt_id.length).toBeGreaterThan(0);
    expect(mocks.authorize).not.toHaveBeenCalled();
    expect(mocks.saveCredentials).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("App Secret")).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByText(CODE)).toBeVisible());
    expect(mocks.open).toHaveBeenCalledWith(URL);
  });

  it("keeps the manual OAuth App form reachable from an explicitly named advanced action", async () => {
    page();
    await openConnectionDialog();
    fireEvent.click(screen.getByRole("tab", { name: /手动.*OAuth/ }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "连接 GitHub" })).toBeVisible());
    expect(screen.getByLabelText("App ID")).toBeVisible();
    expect(screen.getByLabelText("App Secret")).toBeVisible();
    expect(screen.getByRole("button", { name: "保存并授权" })).toBeEnabled();
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it("recovers a blocked system-browser open without another authorization request", async () => {
    mocks.open.mockRejectedValueOnce(new Error("fixture browser unavailable"));
    page(); await connect();
    await waitFor(() => expect(screen.getByText(CODE)).toBeVisible());
    fireEvent.click(await screen.findByRole("button", { name: /重新打开|重试打开/ }));
    await waitFor(() => expect(mocks.open).toHaveBeenCalledTimes(2));
    expect(mocks.start).toHaveBeenCalledTimes(1);
  });

  it("uses the same idempotency attempt when the create response is lost and the user retries", async () => {
    mocks.start.mockRejectedValueOnce({ code: "ECONNABORTED" });
    page(); await connect();
    fireEvent.click(await screen.findByRole("button", { name: /重试/ }));
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(2));
    expect(mocks.start.mock.calls[1][0]).toEqual(mocks.start.mock.calls[0][0]);
  });

  it("shows only the completed connection returned by AuthService and does not auto-enable chat", async () => {
    connections = [manual];
    mocks.start.mockImplementation(async () => { connections = [manual, cli]; return { data: session("COMPLETED", { connection_id: cli.connection_id }) }; });
    page("/github"); await connect();
    expect(await screen.findByText("Fixture CLI", { exact: false })).toBeVisible();
    expect(screen.getByText("Fixture manual", { exact: false })).toBeVisible();
    expect(screen.queryByText(CODE)).not.toBeInTheDocument();
    expect(mocks.open).not.toHaveBeenCalled();
    expect(mocks.patch).not.toHaveBeenCalled();
  });

  it("does not claim success when COMPLETED has no connection_id", async () => {
    mocks.start.mockResolvedValue({ data: session("COMPLETED") });
    page(); await connect();
    expect(await screen.findByRole("alert")).toHaveTextContent(/失败|未完成|无法/);
    expect(mocks.patch).not.toHaveBeenCalled();
  });

  it.each(["CANCELLED", "EXPIRED", "FAILED"])("clears the device code and stops polling at %s", async status => {
    page(); await connect(true);
    expect(screen.getByText(CODE)).toBeVisible();
    mocks.poll.mockResolvedValue({ data: session(status, { error_code: status === "FAILED" ? "GITHUB_AUTH_FAILED" : undefined }) });
    await tick();
    expect(screen.queryByText(CODE)).not.toBeInTheDocument();
    const calls = mocks.poll.mock.calls.length;
    await tick(10_000);
    expect(mocks.poll).toHaveBeenCalledTimes(calls);
    expect(mocks.start).toHaveBeenCalledTimes(1);
  });

  it("cancels the owned session once and ignores a delayed waiting response", async () => {
    let finish!: (value: unknown) => void;
    mocks.poll.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    page(); await connect(true);
    await tick();
    fireEvent.click(screen.getByRole("button", { name: /取消授权/ }));
    await act(async () => { await Promise.resolve(); });
    expect(mocks.cancel).toHaveBeenCalledTimes(1);
    expect(mocks.cancel.mock.calls[0][0]).toEqual({ sessionId: "fixture-session" });
    await act(async () => finish({ data: waiting() }));
    expect(screen.queryByText(CODE)).not.toBeInTheDocument();
    const calls = mocks.poll.mock.calls.length;
    await tick(10_000);
    expect(mocks.poll).toHaveBeenCalledTimes(calls);
  });

  it("honors a completed-save response to cancellation instead of reporting cancelled", async () => {
    mocks.cancel.mockImplementation(async () => { connections = [cli]; return { data: session("COMPLETED", { connection_id: cli.connection_id }) }; });
    page(); await connect();
    fireEvent.click(await screen.findByRole("button", { name: /取消授权/ }));
    expect(await screen.findByText("Fixture CLI", { exact: false })).toBeVisible();
    expect(screen.queryByText(CODE)).not.toBeInTheDocument();
  });

  it.each([401, 403, 404])("stops on poll HTTP %s without silently creating another session", async status => {
    page(); await connect(true);
    mocks.poll.mockRejectedValue({ response: { status, data: { code: 1001202 } } });
    await tick();
    expect(screen.queryByText(CODE)).not.toBeInTheDocument();
    const calls = mocks.poll.mock.calls.length;
    await tick(10_000);
    expect(mocks.poll).toHaveBeenCalledTimes(calls);
    expect(mocks.start).toHaveBeenCalledTimes(1);
  });

  it("never persists device codes or echoes raw error bodies containing credentials", async () => {
    const logs = (["log", "warn", "error", "debug"] as const).map(level => vi.spyOn(console, level).mockImplementation(() => {}));
    const writes = [vi.spyOn(localStorage, "setItem"), vi.spyOn(sessionStorage, "setItem")];
    page(); await connect(true);
    expect(screen.getByText(CODE)).toBeVisible();
    expect(storageValues()).not.toContain(CODE);
    mocks.poll.mockRejectedValue({ response: { status: 403, data: { message: "fixture-private-token " + CODE } } });
    await tick();
    expect(document.body.textContent).not.toContain("fixture-private-token");
    for (const serialized of [storageValues(), JSON.stringify(logs.map(spy => spy.mock.calls)), JSON.stringify(writes.map(spy => spy.mock.calls))]) {
      expect(serialized).not.toContain(CODE);
      expect(serialized).not.toContain("fixture-private-token");
    }
  });

  it("unmounts without further polling and reloads without starting a new authorization", async () => {
    const mounted = page(); await connect(true);
    mounted.unmount();
    const polls = mocks.poll.mock.calls.length;
    await tick(10_000);
    expect(mocks.poll).toHaveBeenCalledTimes(polls);
    vi.useRealTimers(); page();
    await screen.findByRole("heading", { name: "GitHub" });
    expect(mocks.start).toHaveBeenCalledTimes(1);
  });

  it.each([1001210, 1001205])("shows unavailable/busy feedback for stable backend code %s", async code => {
    mocks.start.mockRejectedValue({ response: { status: code === 1001210 ? 503 : 409, data: { code } } });
    page(); await connect();
    expect(await screen.findByRole("alert")).toHaveTextContent(/不可用|不支持|稍后|进行中/);
    expect(mocks.open).not.toHaveBeenCalled();
    expect(mocks.authorize).not.toHaveBeenCalled();
  });

  it("does not merge same-UID sources and changes chat preference only for the selected CLI ID", async () => {
    connections = [manual, cli];
    page("/github");
    const row = await screen.findByRole("group", { name: /Fixture CLI/ });
    const legacy = screen.getByRole("group", { name: /Fixture manual/ });
    expect(within(row).getByText(/GitHub CLI/)).toBeVisible();
    expect(within(legacy).getByText(/OAuth/)).toBeVisible();
    fireEvent.click(within(row).getByRole("switch", { name: /聊天/ }));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
    expect(mocks.patch.mock.calls[0][0]).toMatchObject({ connectionId: cli.connection_id, cloudConnectionUpdateBody: { chat_enabled: true } });
  });

  it("reauthorizes the selected CLI connection with its own connection ID", async () => {
    connections = [manual, cli];
    page("/github");
    const row = await screen.findByRole("group", { name: /Fixture CLI/ });
    fireEvent.click(within(row).getByRole("button", { name: /重新连接|重新授权/ }));
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
    expect(mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody).toMatchObject({ reauthorize_connection_id: cli.connection_id });
    expect(mocks.authorize).not.toHaveBeenCalled();
  });

  it("disconnects the selected CLI connection without removing the same-UID manual account", async () => {
    connections = [manual, cli];
    page("/github");
    const row = await screen.findByRole("group", { name: /Fixture CLI/ });
    fireEvent.click(within(row).getByRole("button", { name: /断开/ }));
    await waitFor(() => expect(screen.queryByRole("dialog") || mocks.remove.mock.calls.length).toBeTruthy());
    const confirmation = screen.queryByRole("dialog");
    if (confirmation) fireEvent.click(within(confirmation).getByRole("button", { name: /确认|确定|断开/ }));
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledTimes(1));
    expect(mocks.remove.mock.calls[0][0]).toEqual({ connectionId: cli.connection_id });
    expect(screen.getByText("Fixture manual", { exact: false })).toBeVisible();
  });


  it("submits the manual OAuth form through the original save/authorize endpoints", async () => {
    vi.spyOn(window, "open").mockReturnValue({ closed: false, close: vi.fn(), focus: vi.fn() } as unknown as Window);
    page();
    await openConnectionDialog();
    fireEvent.click(screen.getByRole("tab", { name: /手动.*OAuth/ }));
    fireEvent.change(await screen.findByLabelText("App ID"), { target: { value: "fixture-client" } });
    fireEvent.change(screen.getByLabelText("App Secret"), { target: { value: "fixture-manual-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "保存并授权" }));
    await waitFor(() => expect(mocks.authorize).toHaveBeenCalledTimes(1));
    expect(mocks.saveCredentials.mock.calls[0][0]).toMatchObject({ provider: "github" });
    expect(mocks.authorize.mock.calls[0][0]).toMatchObject({ provider: "github" });
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it("keeps a manual account on the original reauthorization path", async () => {
    vi.spyOn(window, "open").mockReturnValue({ closed: false, close: vi.fn(), focus: vi.fn() } as unknown as Window);
    connections = [manual, cli];
    page("/github");
    const row = await screen.findByRole("group", { name: /Fixture manual/ });
    fireEvent.click(within(row).getByRole("button", { name: /重新授权|重新连接/ }));
    await waitFor(() => expect(mocks.authorize).toHaveBeenCalledTimes(1));
    expect(mocks.authorize.mock.calls[0][0]).toMatchObject({ provider: "github",
      cloudOAuthAuthorizeURLBody: { reauthorize_connection_id: manual.connection_id } });
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it("starts a fresh web flow only after the user explicitly chooses it", async () => {
    connections = [manual]; page("/github");
    await openConnectionDialog();
    fireEvent.click(screen.getByRole("button", { name: /网页授权.*GitHub|GitHub.*网页授权/ }));
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
    expect(mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody).toMatchObject({ force_web: true });
  });

  it("moves a polled completion to the confirmed account and stops displaying the device code", async () => {
    page(); await connect(true);
    connections = [cli];
    mocks.poll.mockResolvedValue({ data: session("COMPLETED", { connection_id: cli.connection_id }) });
    await tick();
    expect(screen.getByText("Fixture CLI", { exact: false })).toBeVisible();
    expect(screen.queryByText(CODE)).not.toBeInTheDocument();
    const calls = mocks.poll.mock.calls.length;
    await tick(10_000);
    expect(mocks.poll).toHaveBeenCalledTimes(calls);
  });

  it("keeps the existing session through a transient polling failure", async () => {
    page(); await connect(true);
    mocks.poll.mockRejectedValueOnce({ response: { status: 503 } }).mockResolvedValue({ data: waiting() });
    await tick(10_000);
    expect(mocks.poll.mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText(CODE)).toBeVisible();
    expect(mocks.start).toHaveBeenCalledTimes(1);
    expect(mocks.open).toHaveBeenCalledTimes(1);
  });


  it("rejects an untrusted device URL returned to the page before invoking the Desktop bridge", async () => {
    mocks.start.mockResolvedValue({ data: session("WAITING_USER", { user_code: CODE,
      verification_uri: "https://github.com.evil.test/login/device" }) });
    page(); await connect();
    expect(await screen.findByRole("alert")).toHaveTextContent(/失败|无效|无法/);
    expect(mocks.open).not.toHaveBeenCalled();
  });

  it("provides English labels for the new connect and cancel controls", async () => {
    await i18n.changeLanguage("en-US"); page();
    fireEvent.click(await screen.findByRole("button", { name: /^Connect GitHub$/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Start connecting" }));
    await waitFor(() => expect(screen.getByText(CODE)).toBeVisible());
    expect(screen.getByRole("button", { name: /Cancel authorization/i })).toBeEnabled();
    expect(document.body.textContent).not.toMatch(/modelProvider\.cloudDocuments\.github/);
  });

  it("uses CLI reauthorization for a direct recovery link instead of the manual callback path", async () => {
    connections = [{ ...cli, status: "EXPIRED" }];
    page("?provider=github&connection_id=fixture-cli");
    fireEvent.click(await screen.findByRole("button", { name: /重新授权|重新连接/ }));
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
    expect(mocks.start.mock.calls[0][0].gitHubCLISessionCreateBody).toMatchObject({ reauthorize_connection_id: cli.connection_id });
    expect(mocks.authorize).not.toHaveBeenCalled();
  });
});
