import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cloudResource, deferred, installDesktopTestDOM, localSkill } from "@/test/desktopResourceFixtures";

const mocks = vi.hoisted(() => ({
  mode: "desktop", session: vi.fn(), list: vi.fn(), local: vi.fn(), patch: vi.fn(), upload: vi.fn(), download: vi.fn(),
}));
vi.mock("@/runtime/mode", async (load) => ({ ...await load<object>(), isDesktopRuntime: () => mocks.mode === "desktop" }));
vi.mock("@/runtime/features", () => ({ runtimeFeatures: { hideUserGroupSurfaces: true, hideLocalUserControls: true } }));
vi.mock("@/runtime/cloud/session", () => ({ getCloudSession: mocks.session, isCloudBusinessAvailable: (session: any) => session?.state === "signed_in" && session?.configured !== false && session?.reachability !== "unreachable", beginCloudLogin: vi.fn(), LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed" }));
vi.mock("../../cloudResourceApi", async (load) => ({ ...await load<object>(), listCloudResources: mocks.list, uploadCloudSkill: mocks.upload, downloadCloudResource: mocks.download }));
vi.mock("../../skillApi", async (load) => ({
  ...await load<object>(), listSkillAssetsPage: mocks.local, patchSkillAsset: mocks.patch,
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
// Keep the data integration focused on real catalog rows and pagination.
vi.mock("./SkillManagementToolbar", () => ({ default: ({ skillView }: { skillView: string }) => (
  <nav><button role="tab" aria-selected={skillView === "installed"}>我的技能</button></nav>
) }));
vi.mock("@/modules/workflow/components/NewWorkflowModal", () => ({ default: () => null }));

import { desktopTestTranslation as translation } from "@/test/desktopResourceFixtures";
import MemoryManagement from "../../index";

function Location() { return <output aria-label="当前位置">{useLocation().pathname}</output>; }
function mount() {
  return render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/memory-management/skills"]}>
    <MemoryManagement embeddedTab="skills" /><Location />
  </MemoryRouter></ConfigProvider>);
}

beforeAll(installDesktopTestDOM);
beforeEach(() => {
  vi.clearAllMocks(); mocks.mode = "desktop"; mocks.patch.mockResolvedValue({});
  mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "account-a" });
  mocks.list.mockResolvedValue([cloudResource("cloud-only")]);
  mocks.local.mockImplementation(async ({ page = 1, pageSize = 6 }) => ({ records: [localSkill("local-only")], total: 1, page, pageSize }));
});
afterEach(cleanup);

describe("Desktop 我的技能 local and Cloud integration", () => {
  it("shows builtin provenance before internal category while retaining unknown legacy sources", async () => {
    mocks.list.mockResolvedValue([]);
    mocks.local.mockResolvedValue({ records: [
      { ...localSkill("builtin-installed"), category: "internal", originBuiltinSkillUid: "builtin" },
      { ...localSkill("legacy-installed"), category: "learning" },
    ], total: 2, page: 1, pageSize: 20 });
    mount();
    const builtinRow = (await screen.findByText("builtin-installed", { exact: true })).closest("tr")!;
    expect(within(builtinRow).getByText(translation("admin.memorySkillOriginBuiltin"))).toBeVisible();
    expect(within(builtinRow).queryByText(translation("admin.memorySkillOriginInternal"))).not.toBeInTheDocument();
    const legacyRow = screen.getByText("legacy-installed", { exact: true }).closest("tr")!;
    expect(within(legacyRow).getByText(translation("admin.memorySkillOriginUnknown"))).toBeVisible();
    expect(within(legacyRow).getByText("learning")).toBeVisible();
  });
  it("shows both sources in 我的技能 without opening a separate cloud tab", async () => {
    mount();
    expect(await screen.findByText("local-only", { exact: true })).toBeVisible();
    expect(await screen.findByText("cloud-only", { exact: true })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: translation("admin.memorySkillMoreActions", { name: "local-only" }) }));
    await waitFor(() => expect(screen.getByRole("menuitem", { name: translation("admin.memoryCloudUploadAction") })).toBeVisible());
    expect(screen.getByRole("button", { name: "我的技能", exact: true })).toHaveAttribute("aria-current", "page");
    expect(mocks.upload).not.toHaveBeenCalled(); expect(mocks.download).not.toHaveBeenCalled();
  });

  it("changing auto-update cannot overwrite a call mode changed by a batch", async () => {
    mount();
    const name = await screen.findByText("local-only", { exact: true });
    const row = name.closest("tr")!;
    fireEvent.click(within(row).getByRole("switch"));
    await waitFor(() => expect(mocks.patch).toHaveBeenCalled());
    expect(mocks.patch.mock.calls[0][0]).toBe("local-only");
    expect(JSON.parse(JSON.stringify(mocks.patch.mock.calls[0][1]))).toEqual({ auto_evo: true });
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
    const locals = Array.from({ length: 22 }, (_, i) => localSkill(`local-page-${i}`));
    mocks.local.mockImplementation(async ({ page = 1, pageSize = 6 }) => ({ records: locals.slice((page - 1) * pageSize, page * pageSize), total: 22, page, pageSize }));
    mocks.list.mockResolvedValue([cloudResource("cloud-tail-a"), cloudResource("cloud-tail-b")]);
    mount(); await screen.findByText("local-page-0", { exact: true });
    await waitFor(() => expect(mocks.list).toHaveBeenCalled());
    expect(screen.queryByText("cloud-tail-a")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTitle("2"));
    expect(await screen.findByText("local-page-20", { exact: true })).toBeVisible();
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
