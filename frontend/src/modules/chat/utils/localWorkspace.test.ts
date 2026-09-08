import { beforeEach, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import { selectLocalWorkspace } from "@/runtime/desktopBridge";
import { selectWorkspaceCandidate, workspaceReason } from "./localWorkspace";
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
});
