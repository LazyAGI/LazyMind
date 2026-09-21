import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cloudResource, deferred, desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";

const mocks = vi.hoisted(() => ({ mode: "desktop", session: vi.fn(), cloud: vi.fn(), drafts: vi.fn(), builtins: vi.fn(), settings: vi.fn(), download: vi.fn() }));
vi.mock("@/runtime/mode", async (load) => ({ ...await load<object>(), isDesktopRuntime: () => mocks.mode === "desktop" }));
vi.mock("@/runtime/cloud/session", async (load) => ({ ...await load<object>(), getCloudSession: mocks.session }));
vi.mock("../../cloudResourceApi", async (load) => ({ ...await load<object>(), listCloudResources: mocks.cloud, downloadCloudResource: mocks.download }));
vi.mock("@/modules/workflow/workflowDraftApi", async (load) => ({ ...await load<object>(), listWorkflowDrafts: mocks.drafts, listBuiltinWorkflows: mocks.builtins, listUserWorkflowSettings: mocks.settings }));
import WorkflowInstalledView from "./WorkflowInstalledView";

function Location() { return <output aria-label="当前位置">{useLocation().pathname}</output>; }
function mount() { return render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/memory-management/skills?skillView=workflows"]}><WorkflowInstalledView t={t} onNewWorkflow={vi.fn()} /><Location /></MemoryRouter></ConfigProvider>); }
const draft = (index: number) => ({ id: `draft-${index}`, name: `local-workflow-${index}`, workflow_yaml_content: `id: workflow-${index}\nname: local-workflow-${index}`, generate_status: "done", source_type: "blank", updated_at: "2026-09-07T00:00:00Z", published_workflow_ref: `user:owner:workflow-${index}`, current_revision_id: `revision-${index}`, published: true });

beforeAll(installDesktopTestDOM);
beforeEach(() => {
  vi.clearAllMocks(); mocks.mode = "desktop";
  mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "cloud-a" });
  mocks.cloud.mockResolvedValue([cloudResource("cloud-only-workflow", "workflow")]);
  mocks.drafts.mockResolvedValue({ records: [draft(1)], total: 1 });
  mocks.builtins.mockResolvedValue([]); mocks.settings.mockResolvedValue([]);
});
afterEach(cleanup);

describe("Desktop 我的工作流 combined catalog", () => {
  it.each(["desktop", "cloud", "local"].flatMap((mode) =>
    ["signed_in", "signed_out"].map((state) => ({ mode, state })),
  ))("isolates workflow navigation and Cloud access in $mode / $state", async ({ mode, state }) => {
    mocks.mode = mode;
    mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state, account_id: "cloud-a" });
    mount();
    expect(await screen.findByText("local-workflow-1", { exact: true })).toBeVisible();
    expect(screen.queryByRole("radio", { name: t("admin.memoryWorkflowSourceCloud") })).not.toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: t("admin.memoryWorkflowSourceLocal") })).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText(t("admin.memoryWorkflowSearchPlaceholder"))).toBeVisible();
    expect(screen.getByRole("radio", { name: t("admin.memoryWorkflowFilterBuiltin") })).toBeEnabled();
    expect(screen.getByText(t("admin.memoryWorkflowFilterBuiltin"), { exact: true })).toBeVisible();
    if (mode === "desktop" && state === "signed_in") {
      expect(await screen.findByText("cloud-only-workflow", { exact: true })).toBeVisible();
    } else {
      expect(screen.queryByText("cloud-only-workflow", { exact: true })).not.toBeInTheDocument();
      expect(screen.queryByLabelText(t("admin.memoryCloudDownload"))).not.toBeInTheDocument();
      expect(mocks.cloud).not.toHaveBeenCalled();
    }
    if (mode !== "desktop") expect(mocks.session).not.toHaveBeenCalled();
  });

  it("updates Cloud workflows on login and logout while retaining local workflows", async () => {
    mocks.session.mockResolvedValue({ state: "signed_out" });
    mount();
    await screen.findByText("local-workflow-1", { exact: true });
    expect(mocks.cloud).not.toHaveBeenCalled();
    mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "cloud-a" });
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    await screen.findByText("cloud-only-workflow", { exact: true });
    mocks.session.mockResolvedValue({ state: "signed_out" });
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    await waitFor(() => expect(screen.queryByText("cloud-only-workflow", { exact: true })).not.toBeInTheDocument());
    expect(screen.getByText("local-workflow-1", { exact: true })).toBeVisible();
    expect(screen.queryByLabelText(t("admin.memoryCloudDownload"))).not.toBeInTheDocument();
  });

  it("displays cloud resources beside local workflows on initial entry", async () => {
    mount();
    expect(await screen.findByText("local-workflow-1", { exact: true })).toBeVisible();
    expect(await screen.findByText("cloud-only-workflow", { exact: true })).toBeVisible();
    expect(mocks.download).not.toHaveBeenCalled();
  });

  it.each(["present_current", "local_modified", "cloud_updated", "diverged", "incompatible"])("keeps the local authoring entry when Cloud reports %s", async (status) => {
    mocks.cloud.mockResolvedValue([cloudResource("remote-copy", "workflow", { local_exists: true, local_resource_id: "published-resource-id", local_resource_ref: "user:owner:workflow-1", presence_status: status }), cloudResource("cloud-only-workflow", "workflow")]);
    mount(); expect(await screen.findByText("cloud-only-workflow", { exact: true })).toBeVisible();
    expect(screen.queryByText("remote-copy", { exact: true })).not.toBeInTheDocument();
    expect(screen.getAllByText("local-workflow-1", { exact: true })).toHaveLength(1);
  });

  it("includes an imported published resource without inventing a draft or a builtin", async () => {
    mocks.settings.mockResolvedValue([{ workflow_ref: "user:owner:imported", workflow_id: "imported", name: "imported-local-workflow", source_type: "cloud", revision_id: "revision-imported", revision_no: 1, enabled: false, status: "published" }]);
    mocks.cloud.mockResolvedValue([cloudResource("cloud-imported", "workflow", { local_exists: true, local_resource_id: "resource-imported", local_resource_ref: "user:owner:imported" })]);
    mount();
    fireEvent.click(await screen.findByText("imported-local-workflow", { exact: true }));
    await waitFor(() => expect(screen.getByLabelText("当前位置").textContent).not.toBe("/memory-management/skills"));
    expect(screen.getByLabelText("当前位置").textContent).not.toBe("/memory-management/workflows/resource-imported");
    expect(screen.getByLabelText("当前位置").textContent).not.toContain("/builtin/");
    expect(screen.queryByText("cloud-imported")).not.toBeInTheDocument();
  });

  it("deduplicates a draft and its published local resource", async () => {
    mocks.settings.mockResolvedValue([{ workflow_ref: "user:owner:workflow-1", workflow_id: "workflow-1", name: "published-copy-of-draft", source_type: "user", revision_id: "revision-1", revision_no: 1, status: "published" }]);
    mount(); expect(await screen.findByText("cloud-only-workflow", { exact: true })).toBeVisible();
    expect(screen.queryByText("published-copy-of-draft")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("local-workflow-1", { exact: true }));
    expect(screen.getByLabelText("当前位置")).toHaveTextContent("/memory-management/workflows/draft-1");
  });

  it("loads all draft pages using a supported page size before filtering", async () => {
    const records = Array.from({ length: 121 }, (_, i) => draft(i));
    mocks.drafts.mockImplementation(async ({ page = 1, pageSize = 20 }) => {
      const size = pageSize <= 100 ? pageSize : 20;
      return { records: records.slice((page - 1) * size, page * size), total: records.length };
    });
    mount(); await screen.findByText("local-workflow-0", { exact: true });
    const search = screen.getByPlaceholderText(t("admin.memoryWorkflowSearchPlaceholder"));
    fireEvent.change(search, { target: { value: "local-workflow-120" } }); fireEvent.keyDown(search, { key: "Enter", code: "Enter" });
    expect(await screen.findByText("local-workflow-120", { exact: true })).toBeVisible();
    for (const [params] of mocks.drafts.mock.calls) { expect(params.pageSize).toBeLessThanOrEqual(100); }
  });

  it("opens cloud detail without downloading or passing a cloud ID as draft ID", async () => {
    mount(); fireEvent.click(await screen.findByText("cloud-only-workflow", { exact: true }));
    expect(screen.getByLabelText("当前位置")).toHaveTextContent("/memory-management/workflows/cloud/cloud-only-workflow");
    expect(mocks.download).not.toHaveBeenCalled();
  });

  it("retains local workflows if the cloud source fails", async () => {
    mocks.cloud.mockRejectedValue(new Error("fixture unavailable")); mount();
    expect(await screen.findByText("local-workflow-1", { exact: true })).toBeVisible();
    expect(await screen.findByRole("alert")).toBeVisible();
  });

  it("does not erase drafts because another local source failed", async () => {
    mocks.builtins.mockRejectedValue(new Error("fixture builtin catalog unavailable")); mount();
    expect(await screen.findByText("local-workflow-1", { exact: true })).toBeVisible();
  });

  it("does not publish an old Cloud account result after switching accounts", async () => {
    const old = deferred<ReturnType<typeof cloudResource>[]>(); mocks.cloud.mockReturnValueOnce(old.promise);
    mount(); await waitFor(() => expect(mocks.cloud).toHaveBeenCalled());
    mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "cloud-b" });
    mocks.cloud.mockResolvedValue([cloudResource("account-b-workflow", "workflow")]);
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    expect(await screen.findByText("account-b-workflow", { exact: true })).toBeVisible();
    await act(async () => old.resolve([cloudResource("account-a-late", "workflow")]));
    expect(screen.queryByText("account-a-late")).not.toBeInTheDocument();
  });

  it.each(["signed_out", "reauth_required", "offline"])("keeps local workflows with Cloud session %s", async (state) => {
    mocks.session.mockResolvedValue({ state }); mount(); await screen.findByText("local-workflow-1", { exact: true });
    expect(mocks.cloud).not.toHaveBeenCalled();
    expect(screen.queryByText(t("admin.memoryCloudLoading"))).not.toBeInTheDocument();
    expect(screen.queryByText(t("admin.memoryCloudLoadFailed"))).not.toBeInTheDocument();
  });

  it("keeps Cloud interaction invisible when the local session snapshot cannot be read", async () => {
    mocks.session.mockRejectedValue(new Error("session unavailable"));
    mount();
    await screen.findByText("local-workflow-1", { exact: true });
    expect(mocks.cloud).not.toHaveBeenCalled();
    expect(screen.queryByText(t("admin.memoryCloudLoading"))).not.toBeInTheDocument();
    expect(screen.queryByText(t("admin.memoryCloudLoadFailed"))).not.toBeInTheDocument();
  });

  it("keeps local workflows without a Cloud request when the configured address is unreachable", async () => {
    mocks.session.mockResolvedValue({
      configured: true,
      reachability: "unreachable",
      state: "signed_in",
    });
    mount();
    await screen.findByText("local-workflow-1", { exact: true });
    expect(mocks.cloud).not.toHaveBeenCalled();
  });

  it.each(["cloud", "local"])("does not activate the Desktop merge in %s runtime", async (mode) => {
    mocks.mode = mode; mount(); await screen.findByText("local-workflow-1", { exact: true });
    expect(mocks.cloud).not.toHaveBeenCalled();
  });
});
