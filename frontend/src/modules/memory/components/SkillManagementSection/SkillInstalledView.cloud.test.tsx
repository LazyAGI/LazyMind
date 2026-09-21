import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cloudResource, deferred, installDesktopTestDOM, localSkill } from "@/test/desktopResourceFixtures";

const mocks = vi.hoisted(() => ({
  mode: "desktop", session: vi.fn(), list: vi.fn(), local: vi.fn(), upload: vi.fn(), download: vi.fn(),
}));
vi.mock("@/runtime/mode", async (load) => ({ ...await load<object>(), isDesktopRuntime: () => mocks.mode === "desktop" }));
vi.mock("@/runtime/features", async (load) => {
  const actual = await load<typeof import("@/runtime/features")>();
  return { ...actual, get runtimeFeatures() { return actual.resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: mocks.mode }); } };
});
vi.mock("@/runtime/cloud/session", async (load) => ({ ...await load<object>(), getCloudSession: mocks.session, beginCloudLogin: vi.fn() }));
vi.mock("../../cloudResourceApi", async (load) => ({ ...await load<object>(), listCloudResources: mocks.list, uploadCloudSkill: mocks.upload, downloadCloudResource: mocks.download }));
vi.mock("../../skillApi", async (load) => ({
  ...await load<object>(), listSkillAssetsPage: mocks.local,
  listSkillCategories: vi.fn().mockResolvedValue(["internal"]), listSkillTags: vi.fn().mockResolvedValue([]),
  listIncomingSkillShares: vi.fn().mockResolvedValue([]), listOutgoingSkillShares: vi.fn().mockResolvedValue([]),
  getSkillReviewSummary: vi.fn().mockResolvedValue({ runningTask: null, pendingCount: 0 }),
  getRunningSkillOrganizeTask: vi.fn().mockResolvedValue(null),
}));
vi.mock("@/components/auth", () => ({ AUTH_USER_CHANGE_EVENT: "lazymind:user-change", AgentAppsAuth: { getUserInfo: () => ({ id: "local-user", role: "user" }), isLoggedIn: () => true } }));
vi.mock("react-i18next", async (load) => ({ ...await load<object>(), useTranslation: () => ({ t: translation, i18n: { language: "zh-CN" } }) }));
vi.mock("../../components/MemoryDraftModal", () => ({ default: () => null }));
vi.mock("../../components/GlossaryInboxModal", () => ({ default: () => null }));
vi.mock("../../components/ShareModal", () => ({ default: () => null }));
vi.mock("../../components/SkillShareCenterModal", () => ({ default: () => null }));
vi.mock("./SkillAdminPublishModal", () => ({ default: () => null }));
vi.mock("@/modules/workflow/components/NewWorkflowModal", () => ({ default: () => null }));

import { desktopTestTranslation as translation } from "@/test/desktopResourceFixtures";
import MemoryManagement from "../../index";

function Location() { return <output aria-label="当前位置">{useLocation().pathname}</output>; }
function mount(entry = "/memory-management/skills") {
  return render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={[entry]}>
    <MemoryManagement embeddedTab="skills" /><Location />
  </MemoryRouter></ConfigProvider>);
}

beforeAll(installDesktopTestDOM);
beforeEach(() => {
  vi.clearAllMocks(); mocks.mode = "desktop";
  mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "account-a" });
  mocks.list.mockResolvedValue([cloudResource("cloud-only")]);
  mocks.local.mockImplementation(async ({ page = 1, pageSize = 6 }) => ({ records: [localSkill("local-only")], total: 1, page, pageSize }));
});
afterEach(cleanup);

describe("Desktop 我的技能 local and Cloud integration", () => {
  it.each(["desktop", "cloud", "local"].flatMap((mode) =>
    ["signed_in", "signed_out"].map((state) => ({ mode, state })),
  ))("isolates the real navigation and Cloud access in $mode / $state", async ({ mode, state }) => {
    mocks.mode = mode;
    mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state, account_id: "account-a" });
    mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(screen.getAllByRole("tab")).toHaveLength(3);
    expect(screen.queryByRole("tab", { name: translation("admin.memorySkillViewCloud") })).not.toBeInTheDocument();
    if (mode === "desktop" && state === "signed_in") {
      expect(await screen.findByText("cloud-only", { exact: true })).toBeVisible();
    } else {
      expect(screen.queryByText("cloud-only", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByLabelText(translation("admin.memoryCloudUploadAction"))).not.toBeInTheDocument();
      expect(mocks.list).not.toHaveBeenCalled();
    }
    if (mode !== "desktop") expect(mocks.session).not.toHaveBeenCalled();
  });

  it.each(["desktop", "cloud", "local"].flatMap((mode) =>
    ["signed_in", "signed_out"].map((state) => ({ mode, state })),
  ))("opens legacy Cloud list links in 我的技能 in $mode / $state", async ({ mode, state }) => {
    mocks.mode = mode;
    mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state, account_id: "account-a" });
    mount("/memory-management/skills?skillView=cloud");
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(screen.getByRole("tab", { name: /我的技能/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByText(translation("admin.memoryCloudLoginRequired"))).not.toBeInTheDocument();
    if (mode !== "desktop") expect(mocks.session).not.toHaveBeenCalled();
  });

  it("updates Cloud skills on login and logout while retaining local skills", async () => {
    mocks.session.mockResolvedValue({ state: "signed_out" });
    mount();
    await screen.findByText("local-only", { exact: true });
    expect(mocks.list).not.toHaveBeenCalled();
    mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "account-a" });
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    await screen.findByText("cloud-only", { exact: true });
    mocks.session.mockResolvedValue({ state: "signed_out" });
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    await waitFor(() => expect(screen.queryByText("cloud-only", { exact: true })).not.toBeInTheDocument());
    expect(screen.getByText("local-only", { exact: true })).toBeVisible();
    expect(screen.queryByLabelText(translation("admin.memoryCloudUploadAction"))).not.toBeInTheDocument();
  });

  it("shows both sources in 我的技能 without opening a separate cloud tab", async () => {
    mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(await screen.findByText("cloud-only", { exact: true })).toBeVisible();
    expect(screen.getByLabelText(translation("admin.memoryCloudUploadAction"))).toBeVisible();
    expect(screen.getByRole("tab", { name: /我的技能/ })).toHaveAttribute("aria-selected", "true");
    expect(mocks.upload).not.toHaveBeenCalled(); expect(mocks.download).not.toHaveBeenCalled();
  });

  it.each(["present_current", "local_modified", "cloud_updated", "diverged", "incompatible"])("keeps local identity when presence is %s", async (status) => {
    mocks.list.mockResolvedValue([cloudResource("remote-copy", "skill", { local_exists: true, local_resource_id: "local-only", presence_status: status }), cloudResource("cloud-only")]);
    mount();
    expect(await screen.findByText("cloud-only", { exact: true })).toBeVisible();
    expect(screen.getAllByText("local-only", { exact: true })).toHaveLength(1);
    expect(screen.queryByText("remote-copy", { exact: true })).not.toBeInTheDocument();
  });

  it("does not redisplay a cloud copy whose local counterpart is outside the loaded page", async () => {
    mocks.list.mockResolvedValue([cloudResource("must-stay-hidden", "skill", { local_exists: true, local_resource_id: "off-page-local", presence_status: "local_modified" }), cloudResource("cloud-only")]);
    mount();
    expect(await screen.findByText("cloud-only", { exact: true })).toBeVisible();
    expect(screen.queryByText("must-stay-hidden")).not.toBeInTheDocument();
  });

  it("does not deduplicate unrelated skills by name", async () => {
    mocks.list.mockResolvedValue([cloudResource("cloud-distinct", "skill", { resource_name: "local-only" })]);
    mount();
    await waitFor(() => expect(screen.getAllByText("local-only", { exact: true })).toHaveLength(2));
  });

  it("paginates local rows followed by cloud-only rows without appending the cloud list to every page", async () => {
    const locals = Array.from({ length: 8 }, (_, i) => localSkill(`local-page-${i}`));
    mocks.local.mockImplementation(async ({ page = 1, pageSize = 6 }) => ({ records: locals.slice((page - 1) * pageSize, page * pageSize), total: 8, page, pageSize }));
    mocks.list.mockResolvedValue([cloudResource("cloud-tail-a"), cloudResource("cloud-tail-b")]);
    mount(); await screen.findByText("local-page-0", { exact: true });
    await waitFor(() => expect(mocks.list).toHaveBeenCalled());
    expect(screen.queryByText("cloud-tail-a")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTitle("2"));
    expect(await screen.findByText("local-page-6", { exact: true })).toBeVisible();
    expect(await screen.findByText("cloud-tail-a", { exact: true })).toBeVisible();
    expect(screen.getByText("cloud-tail-b", { exact: true })).toBeVisible();
    expect(screen.queryByText("local-page-0", { exact: true })).not.toBeInTheDocument();
  });

  it("opens a cloud-only skill using its explicit cloud detail route", async () => {
    mount(); fireEvent.click(await screen.findByText("cloud-only", { exact: true }));
    await waitFor(() => expect(screen.getByLabelText("当前位置")).toHaveTextContent("/memory-management/skills/cloud/cloud-only"));
    expect(mocks.download).not.toHaveBeenCalled();
  });

  it.each(["signed_out", "reauth_required", "offline"])("preserves local skills when session is %s", async (state) => {
    mocks.session.mockResolvedValue({ state }); mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(mocks.list).not.toHaveBeenCalled();
    expect(screen.queryByLabelText(translation("admin.memoryCloudUploadAction"))).not.toBeInTheDocument();
    expect(screen.queryByText(translation("admin.memoryCloudLoading"))).not.toBeInTheDocument();
    expect(screen.queryByText(translation("admin.memoryCloudLoadFailed"))).not.toBeInTheDocument();
  });

  it("keeps Cloud interaction invisible when the local session snapshot cannot be read", async () => {
    mocks.session.mockRejectedValue(new Error("session unavailable"));
    mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(mocks.list).not.toHaveBeenCalled();
    expect(screen.queryByLabelText(translation("admin.memoryCloudUploadAction"))).not.toBeInTheDocument();
    expect(screen.queryByText(translation("admin.memoryCloudLoadFailed"))).not.toBeInTheDocument();
  });

  it("preserves local skills without a Cloud request when the configured address is unreachable", async () => {
    mocks.session.mockResolvedValue({
      configured: true,
      reachability: "unreachable",
      state: "signed_in",
    });
    mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(mocks.list).not.toHaveBeenCalled();
  });

  it.each(["cloud", "local"])("leaves the existing %s runtime list local", async (mode) => {
    mocks.mode = mode; mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(mocks.list).not.toHaveBeenCalled();
  });

  it("preserves the local result and offers a retry after a Cloud failure", async () => {
    mocks.list.mockRejectedValue(new Error("fixture network unavailable")); mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(await screen.findByRole("alert")).toBeVisible();
    mocks.list.mockResolvedValue([cloudResource("cloud-recovered")]);
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("cloud-recovered", { exact: true })).toBeVisible();
  });

  it("discards an old account response after logout", async () => {
    const pending = deferred<ReturnType<typeof cloudResource>[]>(); mocks.list.mockReturnValueOnce(pending.promise);
    mount(); await waitFor(() => expect(mocks.list).toHaveBeenCalled());
    mocks.session.mockResolvedValue({ state: "signed_out" });
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    await act(async () => pending.resolve([cloudResource("old-account-secret-row")]));
    expect(screen.queryByText("old-account-secret-row")).not.toBeInTheDocument();
    expect(screen.getByText("local-only", { exact: true })).toBeVisible();
  });
});
