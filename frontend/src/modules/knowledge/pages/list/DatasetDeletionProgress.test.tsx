import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";
const mocks = vi.hoisted(() => ({ get: vi.fn(), user: { userId: "owner", tenantId: "default" } }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => mocks.user } }));
vi.mock("@/components/request", () => ({ axiosInstance: { get: mocks.get }, localizeErrorCode: (_code: string, fallback: string) => fallback }));
import { DatasetDeletionProgressList, useDatasetDeletions } from "./DatasetDeletionProgress";
const progress = { dataset_id: "ds", display_name: "手册", status: "cleaning", total: 5, completed: 3, failed: 0 };
const key = "knowledge-deletions:default:owner";
function Harness() {
  const { deletions, trackDeletion, dismissDeletion } = useDatasetDeletions();
  return <><button onClick={() => trackDeletion("ds", "手册")}>track</button><DatasetDeletionProgressList deletions={deletions} onDismiss={dismissDeletion} /></>;
}
beforeEach(() => { installDesktopTestDOM(); localStorage.clear(); vi.clearAllMocks(); mocks.user = { userId: "owner", tenantId: "default" }; });
afterEach(() => { cleanup(); vi.useRealTimers(); });
describe("knowledge base cleanup feedback", () => {
  it("restores progress after refresh and waits for backend completion", async () => {
    vi.useFakeTimers();
    localStorage.setItem(key, JSON.stringify([progress]));
    mocks.get.mockResolvedValueOnce({ data: progress }).mockResolvedValue({ data: { ...progress, status: "completed", completed: 5 } });
    await act(async () => { render(<Harness />); });
    expect(screen.getByText(t("knowledge.cleanupInProgress"))).toBeVisible();
    expect(screen.queryByText(t("knowledge.cleanupCompleted"))).not.toBeInTheDocument();
    expect(mocks.get).toHaveBeenCalledTimes(1);
    expect(mocks.get.mock.calls[0][0]).toContain("/datasets/ds/deletion");
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(screen.getByText(t("knowledge.cleanupCompleted"))).toBeVisible();
  });
  it("shows cleanup failure and never claims success", async () => {
    mocks.get.mockResolvedValue({ data: { ...progress, status: "failed", failed: 1 } });
    render(<Harness />); fireEvent.click(screen.getByText("track"));
    expect(await screen.findByText(t("knowledge.cleanupFailed"))).toBeVisible();
    expect(screen.queryByText(t("knowledge.cleanupCompleted"))).not.toBeInTheDocument();
    expect(screen.getByText("手册")).toBeVisible();
  });
  it("keeps tracking on query failure and isolates accounts", async () => {
    localStorage.setItem(key, JSON.stringify([progress]));
    mocks.get.mockRejectedValue(new Error("连接失败"));
    const hook = renderHook(() => useDatasetDeletions());
    await waitFor(() => expect(hook.result.current.deletions[0].queryError).toBe("连接失败"));
    expect(hook.result.current.deletions[0].status).toBe("cleaning");
    mocks.user = { userId: "other", tenantId: "default" };
    hook.rerender();
    await waitFor(() => expect(hook.result.current.deletions).toEqual([]));
    expect(JSON.parse(localStorage.getItem(key)!)[0].dataset_id).toBe("ds");
  });
});
