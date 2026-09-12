import { beforeEach, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import { selectLocalWorkspace } from "@/runtime/desktopBridge";
import { decideWorkspaceApproval, listWorkspaceApprovals, listWorkspaces, selectWorkspaceCandidate, workspaceReason } from "./localWorkspace";
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { post: vi.fn(), get: vi.fn(), put: vi.fn() } }));
vi.mock("@/runtime/desktopBridge", () => ({ selectLocalWorkspace: vi.fn(), reauthorizeLocalWorkspace: vi.fn(), authorizeLocalWorkspace: vi.fn() }));
beforeEach(() => vi.clearAllMocks());
it("does not call HTTP when Desktop selection is canceled", async () => {
  vi.mocked(selectLocalWorkspace).mockResolvedValue(null);
  await expect(selectWorkspaceCandidate("desktop")).resolves.toEqual({ canceled: true });
  expect(axiosInstance.post).not.toHaveBeenCalled();
});
it("uses the Local Proxy selection route", async () => {
  vi.mocked(axiosInstance.post).mockResolvedValue({ data: { selection_token: "token", canceled: false } });
  await expect(selectWorkspaceCandidate("local")).resolves.toMatchObject({ selection_token: "token" });
  expect(axiosInstance.post).toHaveBeenCalledWith("/_local/workspaces:select", {}, { timeout: 0 });
});
it("reads Core reason details", () => {
  expect(workspaceReason({ response: { data: { data: { detail: { reason: "revoked" } } } } })).toBe("revoked");
  expect(workspaceReason({ response: { data: { detail: { reason: "path_unavailable" } } } })).toBe("path_unavailable");
});
it.each([
  ["LOCAL_WORKSPACE_MODE_FORBIDDEN", "mode_forbidden"],
  ["LOCAL_WORKSPACE_SELECTION_FORBIDDEN", "selection_forbidden"],
  ["LOCAL_WORKSPACE_SELECTION_EXPIRED", "selection_expired"],
  ["LOCAL_WORKSPACE_SELECTION_INVALID", "invalid_selection"],
  ["LOCAL_WORKSPACE_PATH_INVALID", "path_invalid"],
])("normalizes Local Proxy error %s", (code, reason) => {
  expect(workspaceReason({ response: { data: { code } } })).toBe(reason);
});
it("passes search and inactive filters to Core", async () => {
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [] } } });
  await listWorkspaces({ query: " project ", includeInactive: true });
  expect(axiosInstance.get).toHaveBeenCalledWith("/api/core/local-workspaces", {
    params: { query: "project", include_inactive: true },
  });
});

it("loads bounded Core approval summaries through the encoded conversation route", async () => {
  const signal = new AbortController().signal;
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{ operation_id: "op-1", status: "preparing", path: "notes.txt" }] } } });
  await expect(listWorkspaceApprovals("conversation/a", signal)).resolves.toEqual([{ operation_id: "op-1", status: "preparing", path: "notes.txt" }]);
  expect(axiosInstance.get).toHaveBeenCalledWith("/api/core/conversations/conversation%2Fa:workspace-approvals", { signal });
});
it.each(["allow_once", "reject"] as const)("submits only %s and returns the Core decision state", async (action) => {
  vi.mocked(axiosInstance.post).mockResolvedValue({ data: { data: { status: action === "reject" ? "rejected" : "allowed" } } });
  await expect(decideWorkspaceApproval("conversation/a", "operation/b", action)).resolves.toEqual({ status: action === "reject" ? "rejected" : "allowed" });
  expect(axiosInstance.post).toHaveBeenCalledWith("/api/core/conversations/conversation%2Fa/workspace-approvals/operation%2Fb:decide", { action });
});
it.each(["approval_capacity", "operation_uncertain", "execution_inactive", "unsupported_file", "search_limit"])("preserves Core approval reason %s", (reason) => {
  expect(workspaceReason({ response: { data: { data: { detail: { reason } } } } })).toBe(reason);
});
