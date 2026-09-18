import { createHash } from "node:crypto";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Outlet } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryManagementContext } from "@/modules/memory/context";
import { deferred, desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), remove: vi.fn(), session: vi.fn() }));
vi.mock("@/layouts/MainLayout", () => ({ default: () => <Outlet context={{ isMenuCollapsed: false, toggleMenu: vi.fn() }} /> }));
vi.mock("@/modules/memory", () => ({ default: () => <MemoryManagementContext.Provider value={{ t, skillAssets: [], skillsInitialized: true, navigateToMemoryList: vi.fn(), refreshSkillAssets: vi.fn() }}><Outlet /></MemoryManagementContext.Provider> }));
vi.mock("@/modules/signin/pages/login", () => ({ default: () => null }));
vi.mock("@/modules/signin/pages/register", () => ({ default: () => null }));
vi.mock("@/modules/signin/pages/dashboard", () => ({ default: () => <Outlet /> }));
vi.mock("@/modules/signin/pages/loginTransition", () => ({ default: () => null }));
vi.mock("@/modules/chat/ChatApp", () => ({ default: () => <Outlet /> }));
vi.mock("@/modules/chat/pages/home", () => ({ default: () => <p>聊天主页</p> }));
vi.mock("@/modules/settings", () => ({ default: () => null }));
vi.mock("@/pages/UserAgreementPage", () => ({ default: () => null }));
vi.mock("@/runtime/features", () => ({ runtimeFeatures: { hideRegister: true, hideCloudAdmin: true, hideEvo: true } }));
vi.mock("@/runtime/mode", async (load) => ({ ...await load<object>(), isDesktopRuntime: () => true }));
vi.mock("@/runtime/localSession", () => ({ isLocalSessionEnabled: () => true }));
vi.mock("@/runtime/cloud/session", () => ({ getCloudSession: mocks.session, isCloudBusinessAvailable: (session: any) => session?.state === "signed_in" && session?.configured !== false && session?.reachability !== "unreachable", LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed" }));
vi.mock("react-i18next", async (load) => ({ ...await load<object>(), useTranslation: () => ({ t, i18n: { language: "zh-CN" } }) }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { defaults: {}, request: (options: { url: string }) => mocks.get(options.url, options), get: mocks.get, post: mocks.post, patch: mocks.patch, put: mocks.put, delete: mocks.remove }, getLocalizedErrorMessage: () => "读取失败", localizeErrorCode: () => "读取失败" }));
vi.mock("@/modules/workflow/components/StateGraphEditor", () => ({ default: ({ readonly, initialWorkflowYaml }: { readonly?: boolean; initialWorkflowYaml?: string }) => <section aria-label="工作流图" data-readonly={String(readonly)}>{initialWorkflowYaml}</section> }));
import AppRouter from "./index";

const bodies: Record<string, string> = {
  "SKILL.md": "---\nname: cloud-document\ndescription: fixture\n---\n# 云端文档标题\n\n云端独有的技能正文。\n",
  "workflow.yaml": "id: cloud-workflow\nname: 云端工作流\nsteps:\n  - id: write\n",
  "scenario/state.yml": "transitions:\n  __start__: [{to: write}]\n  write: [{to: __end__}]\n",
  "scenario/scenario.md": "# 云端工作流说明\n",
  "references/guide.md": "# 云端参考文档\n附加文档内容。\n",
  "unsafe.html": "<script>window.__cloudPreviewExecuted = true</script><h1>原始HTML</h1>",
};
const hash = (text: string) => createHash("sha256").update(text).digest("hex");
const files = Object.entries(bodies).map(([path, content]) => ({ path, size: Buffer.byteLength(content), sha256: hash(content), executable: false }));
const treeHash = hash([...files].sort((a, b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0).map((file) => `${file.path}\0${file.size}\0${file.sha256}\x000\n`).join(""));
const envelope = (data: unknown) => ({ data: { data }, headers: { etag: `"${treeHash}"` } });
const isCloudRead = (url: unknown) => /\/api\/core\/cloud\/(skills|workflows)\//.test(String(url));
function serve(url: string, options?: { params?: { path?: string } }) {
  if (!isCloudRead(url)) throw new Error(`unexpected local content request: ${url}`);
  const kind = url.includes("/workflows/") ? "workflow" : "skill";
  if (url.endsWith("/tree")) return envelope({ resource_id: "cloud-fixture", resource_type: kind, content_hash: treeHash, entrypoint: kind === "skill" ? "SKILL.md" : "workflow.yaml", files });
  if (url.includes("/content")) {
    const path = options?.params?.path ?? new URL(url, "https://desktop.test").searchParams.get("path") ?? "";
    if (!(path in bodies)) throw new Error("fixture file missing");
    return envelope({ path, content_hash: treeHash, sha256: hash(bodies[path]), size: Buffer.byteLength(bodies[path]), mime: path.endsWith(".md") ? "text/markdown" : "text/plain", binary: false, content: bodies[path], preview_status: "ready" });
  }
  return envelope({ resource_id: "cloud-fixture", resource_type: kind, resource_name: "Cloud fixture", content_hash: treeHash, content_size: files.reduce((sum, file) => sum + file.size, 0), format_schema: "lazymind.resource-manifest/v2", updated_at: "2026-09-07T00:00:00Z", local_exists: false, presence_status: "download_required" });
}
function mount(kind: "skill" | "workflow" = "skill") { return render(<MemoryRouter initialEntries={[`/memory-management/${kind === "skill" ? "skills" : "workflows"}/cloud/cloud-fixture`]}><AppRouter /></MemoryRouter>); }
function assertNoWrites() { for (const fn of [mocks.post, mocks.patch, mocks.put, mocks.remove]) expect(fn).not.toHaveBeenCalled(); }

beforeAll(installDesktopTestDOM);
beforeEach(() => { vi.clearAllMocks(); mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "account-a" }); mocks.get.mockImplementation(async (url, options) => serve(url, options)); });
afterEach(cleanup);

describe("Desktop cloud resource detail routes", () => {
  it("opens a Cloud-only Skill directly and reads its document without a local skill", async () => {
    mount(); expect(await screen.findByText("云端独有的技能正文。", { exact: true }, { timeout: 5000 })).toBeVisible();
    expect(screen.getByRole("heading", { name: "云端文档标题" })).toBeVisible();
    expect(mocks.get.mock.calls.every(([url]) => isCloudRead(url))).toBe(true); assertNoWrites();
    const read = mocks.get.mock.calls.find(([url]) => String(url).includes("/content"));
    expect(read?.[1]?.headers?.["If-Match"]).toBe(`"${treeHash}"`);
  });

  it("lets the user read a secondary document from the Cloud directory", async () => {
    mount(); await screen.findByText("云端独有的技能正文。", { exact: true });
    fireEvent.click(screen.getByText("guide.md", { exact: true }));
    expect(await screen.findByText("附加文档内容。", { exact: true })).toBeVisible(); assertNoWrites();
  });

  it("shows workflow definitions and a read-only graph without a draft", async () => {
    mount("workflow");
    const graph = await screen.findByRole("region", { name: "工作流图" });
    expect(graph).toHaveAttribute("data-readonly", "true"); expect(graph).toHaveTextContent("云端工作流");
    expect(mocks.get.mock.calls.every(([url]) => isCloudRead(url))).toBe(true); assertNoWrites();
  });

  it("renders HTML as text rather than executable content", async () => {
    mount(); await screen.findByText("云端独有的技能正文。", { exact: true });
    fireEvent.click(screen.getByText("unsafe.html", { exact: true }));
    expect(await screen.findByText(bodies["unsafe.html"], { exact: true })).toBeVisible();
    expect(document.querySelector("script")).toBeNull();
    expect((window as unknown as Record<string, unknown>).__cloudPreviewExecuted).toBeUndefined();
  });

  it("does not let a late file response overwrite the newly selected file", async () => {
    const pending = deferred<ReturnType<typeof envelope>>();
    mocks.get.mockImplementation(async (url: string, options?: { params?: { path?: string } }) => new URL(url, "https://desktop.test").searchParams.get("path") === "references/guide.md" ? pending.promise : serve(url, options));
    mount(); await screen.findByText("云端独有的技能正文。", { exact: true });
    fireEvent.click(screen.getByText("guide.md", { exact: true }));
    await waitFor(() => expect(mocks.get.mock.calls.some(([url]) => new URL(url, "https://desktop.test").searchParams.get("path") === "references/guide.md")).toBe(true));
    fireEvent.click(screen.getByText("SKILL.md", { exact: true }));
    await act(async () => pending.resolve(serve("/api/core/cloud/skills/cloud-fixture/content", { params: { path: "references/guide.md" } })));
    expect(screen.getByText("云端独有的技能正文。", { exact: true })).toBeVisible();
    expect(screen.queryByText("附加文档内容。", { exact: true })).not.toBeInTheDocument();
  });

  it("clears private document content when the Cloud account logs out", async () => {
    mount(); await screen.findByText("云端独有的技能正文。", { exact: true });
    mocks.session.mockResolvedValue({ state: "signed_out" });
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    await waitFor(() => expect(screen.queryByText("云端独有的技能正文。", { exact: true })).not.toBeInTheDocument()); assertNoWrites();
  });

  it("shows a query error for a deleted cloud resource and never substitutes local content", async () => {
    mocks.get.mockRejectedValue({ response: { status: 404, data: { code: 3040002 } } });
    mount(); expect(await screen.findByRole("alert")).toBeVisible();
    expect(screen.queryByText("云端独有的技能正文。", { exact: true })).not.toBeInTheDocument(); assertNoWrites();
  });

  it.each(["binary", "too_large"])("explains %s files without inventing empty document content", async (status) => {
    mocks.get.mockImplementation(async (url: string, options) => {
      const response = serve(url, options);
      if (!url.includes("/content")) return response;
      return envelope({ path: "SKILL.md", content_hash: treeHash, sha256: files[0].sha256, size: status === "too_large" ? (2 << 20) + 1 : 10, mime: "application/octet-stream", binary: status === "binary", content: "", preview_status: status });
    });
    mount(); expect(await screen.findByRole("alert")).toBeVisible();
    expect(screen.queryByText("云端独有的技能正文。", { exact: true })).not.toBeInTheDocument(); assertNoWrites();
  });

  it("cancels an outstanding document request on unmount", async () => {
    const pending = deferred<ReturnType<typeof envelope>>();
    let signal: AbortSignal | undefined;
    mocks.get.mockImplementation(async (url: string, options) => {
      if (url.includes("/content")) { signal = options?.signal; return pending.promise; }
      return serve(url, options);
    });
    const mounted = mount(); await waitFor(() => expect(mocks.get.mock.calls.some(([url]) => String(url).includes("/content"))).toBe(true));
    mounted.unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => pending.resolve(serve("/api/core/cloud/skills/cloud-fixture/content", { params: { path: "SKILL.md" } })));
    expect(screen.queryByText("云端独有的技能正文。", { exact: true })).not.toBeInTheDocument();
  });
});
