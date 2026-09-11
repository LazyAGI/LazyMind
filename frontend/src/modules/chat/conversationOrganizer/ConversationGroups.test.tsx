import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ConversationGroups from "./ConversationGroups";
import * as api from "./api";
const tr = (key: string, options?: { current?: number; total?: number }) => key.endsWith("preparationProgress") ? `${key} ${options?.current}/${options?.total}` : key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: tr }) }));
vi.mock("./SidebarGroups", () => ({ default: () => null }));
vi.mock("./api", () => ({
  CONVERSATION_GROUPS_CHANGED_EVENT: "groups-changed",
  listConversationGroups: vi.fn(async () => []), getLatestOrganizerState: vi.fn(), getOrganizerRun: vi.fn(), startOrganizerRun: vi.fn(), runAction: vi.fn(), getLatestSuccessfulOrganizerRun: vi.fn(), emitConversationGroupsChanged: vi.fn(), correctOrganizerItem: vi.fn(), createConversationGroup: vi.fn(), deleteConversationGroup: vi.fn(), updateConversationGroup: vi.fn(),
}));
const running: api.OrganizerRun = { created_at: "2026-09-09T00:00:00Z", updated_at: "2026-09-09T00:00:00Z", free_count: 2, organized_count: 0, skipped_count: 0, id: "r", status: "running", stage: "organizing", progress: { current: 0, total: 2, batch_current: 1, batch_total: 1 }, can_cancel: true, can_retry: false, can_undo: false, items: [] };
beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: null, latest_successful_run_id: null, free_conversation_count: 2 }); vi.mocked(api.getOrganizerRun).mockResolvedValue(running); });
describe("organizer entry", () => {
 it("shows preparation batches rather than conversation counts", async () => {
  const preparing: api.OrganizerRun = { ...running, stage: "preparing", progress: { current: 0, total: 41, preparation_current: 20, preparation_total: 41, preparation_batch_current: 2, preparation_batch_completed: 1, preparation_batch_total: 3 } };
  vi.mocked(api.getOrganizerRun).mockResolvedValue(preparing);
  vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: preparing, latest_successful_run_id: null, free_conversation_count: 41 });
  const { unmount } = render(<ConversationGroups mode="organizer" />);
  expect(await screen.findByRole("button", { name: /preparationProgress 2\/3/ })).toBeTruthy();
  unmount();
 });
 it("opens active progress without treating the entry click or close as cancellation", async () => {
  vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: running, latest_successful_run_id: null, free_conversation_count: 2 });
  const { unmount } = render(<ConversationGroups mode="organizer" />);
  await waitFor(() => expect(screen.getByRole("button", { name: /batchProgress/ })).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: /batchProgress/ }));
  expect(await screen.findByRole("dialog")).toBeTruthy();
  expect(screen.getByText("conversationOrganizer.runningSubtitle")).toBeTruthy();
  expect(screen.queryByText("conversationOrganizer.resultSubtitle")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /close/i }));
  expect(api.runAction).not.toHaveBeenCalled(); unmount();
 });
 it("disables empty-scope starts", async () => {
  vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: null, latest_successful_run_id: null, free_conversation_count: 0 });
  const { unmount } = render(<ConversationGroups mode="organizer" />);
  await waitFor(() => expect((screen.getByRole("button", { name: /conversationOrganizer.organize/ }) as HTMLButtonElement).disabled).toBe(true));
  expect(api.startOrganizerRun).not.toHaveBeenCalled(); unmount();
 });
 it("starts with a progress drawer and retains unconfirmed results behind the same entry", async () => {
  vi.mocked(api.startOrganizerRun).mockResolvedValue(running);
  const { unmount } = render(<ConversationGroups mode="organizer" />);
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: /conversationOrganizer.organize/ })); });
  expect(await screen.findByRole("dialog")).toBeTruthy();
  expect(api.startOrganizerRun).toHaveBeenCalledTimes(1);
  unmount();
  const result: api.OrganizerRun = { ...running, status: "succeeded", can_cancel: false, can_undo: true, items: [
    { conversation_id: "grouped", title: "Grouped conversation", summary: "", group_id: "g", state: "grouped", corrected: false },
    { conversation_id: "free", title: "Free conversation", summary: "", group_id: null, state: "free", corrected: false },
  ] };
  vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: result, latest_successful_run_id: "r", free_conversation_count: 2 });
  vi.mocked(api.getLatestSuccessfulOrganizerRun).mockResolvedValue(result);
  const second = render(<ConversationGroups mode="organizer" />);
  fireEvent.click(await screen.findByRole("button", { name: /viewResult/ }));
  expect(await screen.findByRole("button", { name: "conversationOrganizer.confirmResult" })).toBeTruthy();
  expect(screen.getByText("conversationOrganizer.resultSubtitle")).toBeTruthy();
  const summary = screen.getByLabelText("conversationOrganizer.resultSummaryLabel");
  expect(summary.textContent).toContain("2");
  expect(summary.textContent).toContain("conversationOrganizer.resultStats.included");
  expect(summary.textContent).toContain("conversationOrganizer.resultStats.assigned");
  expect(summary.textContent).toContain("conversationOrganizer.resultStats.free");
  expect(api.startOrganizerRun).toHaveBeenCalledTimes(1); second.unmount();
 });
 it("shows failure on the entry and opens the failure details without restarting", async () => {
  const failed: api.OrganizerRun = { ...running, status: "failed", can_cancel: false, can_retry: true };
  vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: failed, latest_successful_run_id: null, free_conversation_count: 2 });
  const { unmount } = render(<ConversationGroups mode="organizer" />);
  fireEvent.click(await screen.findByRole("button", { name: /failedEntry/ }));
  expect(await screen.findByRole("button", { name: "conversationOrganizer.retry" })).toBeTruthy();
  expect(api.startOrganizerRun).not.toHaveBeenCalled();
  expect(api.runAction).not.toHaveBeenCalled();
  unmount();
 });

});

it.each([
  { retry: false, restart: true, button: "restart", startsNew: true },
  { retry: true, restart: false, button: "retry", startsNew: false },
  { retry: true, restart: true, button: "restart", startsNew: true },
  { retry: true, restart: true, button: "retry", startsNew: false },
])("routes recovery $button with retry=$retry restart=$restart", async ({ retry, restart, button, startsNew }) => {
  const failed: api.OrganizerRun = { ...running, status: "failed", can_cancel: false, can_retry: retry, can_restart: restart };
  vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: failed, latest_successful_run_id: null, free_conversation_count: 2 });
  vi.mocked(api.startOrganizerRun).mockResolvedValue({ ...running, id: "new-run" });
  vi.mocked(api.runAction).mockResolvedValue(running);
  const { unmount } = render(<ConversationGroups mode="organizer" />);
  fireEvent.click(await screen.findByRole("button", { name: /failedEntry/ }));
  fireEvent.click(await screen.findByRole("button", { name: `conversationOrganizer.${button}` }));
  await waitFor(() => {
    if (startsNew) {
      expect(api.startOrganizerRun).toHaveBeenCalledTimes(1);
      expect(api.runAction).not.toHaveBeenCalled();
    } else {
      expect(api.runAction).toHaveBeenCalledWith("r", "retry");
      expect(api.startOrganizerRun).not.toHaveBeenCalled();
    }
  });
  unmount();
});

it("does not offer retry or restart for an unresolved error", async () => {
  const failed: api.OrganizerRun = { ...running, status: "failed", can_retry: false, can_restart: false };
  vi.mocked(api.getLatestOrganizerState).mockResolvedValue({ run: failed, latest_successful_run_id: null, free_conversation_count: 2 });
  const { unmount } = render(<ConversationGroups mode="organizer" />);
  fireEvent.click(await screen.findByRole("button", { name: /failedEntry/ }));
  expect(await screen.findByText("conversationOrganizer.blockedHint")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "conversationOrganizer.retry" })).toBeNull();
  expect(screen.queryByRole("button", { name: "conversationOrganizer.restart" })).toBeNull();
  unmount();
});
