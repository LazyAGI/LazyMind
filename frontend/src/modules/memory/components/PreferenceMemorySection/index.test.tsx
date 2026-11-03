import type { ReactNode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import PreferenceMemorySection from ".";
import * as api from "../../currentMemoryApi";

const state = vi.hoisted(() => ({ drag: (_: unknown) => {} }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, args?: { name?: string }) => `${key}${args?.name ? ` ${args.name}` : ""}`, i18n: { language: "en-US" } }) }));
vi.mock("@/components/request", () => ({ getLocalizedErrorMessage: () => "Request failed" }));
vi.mock("../../currentMemoryApi", () => ({ listPreferenceMemories: vi.fn(), getPreferenceMemory: vi.fn(), deletePreferenceMemory: vi.fn(), reorderPreferenceMemories: vi.fn(), getLatestPreferenceOrganizer: vi.fn(), submitPreferenceOrganizer: vi.fn(), isPreferenceOrganizing: (error: { code?: string }) => error.code === "preference_organizing" }));
vi.mock("@dnd-kit/core", () => ({ DndContext: ({ children, onDragEnd }: { children: ReactNode; onDragEnd: typeof state.drag }) => { state.drag = onDragEnd; return children; }, KeyboardSensor: class {}, PointerSensor: class {}, closestCenter: vi.fn(), useSensor: vi.fn(), useSensors: vi.fn() }));
vi.mock("@dnd-kit/sortable", () => ({ SortableContext: ({ children }: { children: ReactNode }) => children, sortableKeyboardCoordinates: vi.fn(), verticalListSortingStrategy: vi.fn(), useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn() }) }));
const items = ["pref.a", "pref.b"].map((name) => ({ name, summary: `${name} summary`, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z" }));
const snapshot = { etag: "etag-1", max_chars: 5000, stored_items: 2, full_projection_chars: 120,
 projected_items: 2, projected_chars: 120, projection_truncated: false, updated_at: "1970-01-01T00:00:00.001Z" };
const task = (status: api.PreferenceOrganizerTask["status"]): api.PreferenceOrganizerTask => ({ task_id: "task-1", status, waiting_reason: "memory_review", created_at: "2026-09-03T00:00:00Z" });
beforeEach(() => {
 vi.resetAllMocks();
 vi.mocked(api.listPreferenceMemories).mockResolvedValue({ items, etag: "etag-1", totalSize: 2, updatedAt: 1, budget: { usedChars: 120, maxChars: 5000 } });
 vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue(null);
 vi.mocked(api.getPreferenceMemory).mockResolvedValue({ item: items[0], referenceStatus: "missing", reference: null });
});
describe("Preference Organizer entry", () => {
 it("upgrades a pending task and disables running edits while keeping details readable", async () => {
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue(task("pending"));
  vi.mocked(api.submitPreferenceOrganizer).mockResolvedValue(task("running"));
  const view = render(<PreferenceMemorySection />);
  const submit = await screen.findByRole("button", { name: /memoryPreferenceOrganizeWaitingReview/ });
  expect(screen.getByRole("button", { name: "admin.memoryPreferenceDelete pref.a" })).toBeEnabled();
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue(task("running"));
  fireEvent.click(submit);
  await screen.findByRole("button", { name: /memoryPreferenceOrganizeRunning/ });
  expect(api.submitPreferenceOrganizer).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "admin.memoryPreferenceDelete pref.a" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: /pref.a summary/ }));
  await waitFor(() => expect(api.getPreferenceMemory).toHaveBeenCalledWith("pref.a"));
  view.unmount();
 });
 it("rolls back optimistic sorting when Core starts organizing during the request", async () => {
  vi.mocked(api.reorderPreferenceMemories).mockRejectedValue({ code: "preference_organizing" });
  const view = render(<PreferenceMemorySection />);
  await screen.findByRole("button", { name: "admin.memoryPreferenceDelete pref.a" });
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue(task("running"));
  await act(async () => { state.drag({ active: { id: "pref.a" }, over: { id: "pref.b" } }); });
  expect(api.reorderPreferenceMemories).toHaveBeenCalledWith(["pref.b", "pref.a"], "etag-1");
  expect([...view.container.querySelectorAll(".memory-preference-item strong")].map((row) => row.textContent)).toEqual(["pref.a", "pref.b"]);
  expect(screen.queryByText("admin.memoryPreferenceConflictTitle")).not.toBeInTheDocument();
  view.unmount();
 });
 it("restores the latest no-change result after returning to the page", async () => {
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...task("done"), result_validity: "current", current_state: snapshot,
   result: { outcome: "no_safe_changes", target_reached: true, observed_state: snapshot, final_state: snapshot } });
  const view = render(<PreferenceMemorySection />);
  expect(await screen.findByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeNoChanges");
  expect(screen.getByRole("button", { name: /memoryPreferenceOrganize$/ })).toBeEnabled();
  view.unmount();
 });
 it("disables manual organization for an empty index", async () => {
  vi.mocked(api.listPreferenceMemories).mockResolvedValue({ items: [], etag: "empty", totalSize: 0, updatedAt: 1 });
  const view = render(<PreferenceMemorySection />);
  await screen.findByText("admin.memoryPreferenceEmpty");
  expect(screen.getByRole("button", { name: /memoryPreferenceOrganize$/ })).toBeDisabled();
  view.unmount();
 });

 it("restores historical change-budget results without disabling organization", async () => {
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...task("done"), result: { outcome: "budget_exhausted", total_changes: 50, target_reached: false } });
  const view = render(<PreferenceMemorySection />);
  expect(await screen.findByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeUnverified");
  expect(screen.getByRole("button", { name: /memoryPreferenceOrganize$/ })).toBeEnabled();
  view.unmount();
 });

 it("keeps the overflow warning when no safe changes were found", async () => {
  const overflow = { ...snapshot, max_chars: 1000, full_projection_chars: 1258 };
  vi.mocked(api.listPreferenceMemories).mockResolvedValue({ items, etag: "etag-1", totalSize: 2, updatedAt: 1, budget: { usedChars: 1258, maxChars: 1000 } });
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...task("done"), result_validity: "current", current_state: overflow,
   result: { outcome: "no_safe_changes", target_reached: false, observed_state: overflow, final_state: overflow } });
  const view = render(<PreferenceMemorySection />);
  expect(await screen.findByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeNoSafeChanges");
  expect(screen.getByText("1258 / 1000")).toBeInTheDocument();
  expect(screen.getByText("admin.memoryPreferenceOverLimitTitle")).toBeInTheDocument();
  expect(screen.queryByText("admin.memoryPreferenceOrganizeNoChanges")).not.toBeInTheDocument();
  view.unmount();
 });

 it.each(["content", "budget"])("rejects a completion that does not match the loaded %s", async (change) => {
  const old = { ...snapshot, ...(change === "content" ? { etag: "older" } : { max_chars: 1000 }) };
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...task("done"), result_validity: "current",
   result: { outcome: "no_safe_changes", target_reached: true, observed_state: old, final_state: old } });
  const view = render(<PreferenceMemorySection />);
  expect(await screen.findByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeStale");
  view.unmount();
 });

 it("refreshes usage when polling the same task detects external changes", async () => {
  const completed: api.PreferenceOrganizerTask = { ...task("done"), result_validity: "current", current_state: snapshot,
   result: { outcome: "no_safe_changes", target_reached: true, observed_state: snapshot, final_state: snapshot } };
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue(completed);
  const view = render(<PreferenceMemorySection />);
  expect(await screen.findByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeNoChanges");
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...completed, result_validity: "stale",
   current_state: { ...snapshot, etag: "etag-2", max_chars: 1000, full_projection_chars: 1258 } });
  vi.mocked(api.listPreferenceMemories).mockResolvedValue({ items, etag: "etag-2", totalSize: 2, updatedAt: 1,
   budget: { usedChars: 1258, maxChars: 1000 } });
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  await screen.findByText("1258 / 1000");
  expect(screen.getByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeStale");
  view.unmount();
 });

 it("reconciles a newer task snapshot that arrives during initial list loading", async () => {
  const current = { ...snapshot, etag: "etag-2", max_chars: 1000, full_projection_chars: 1258 };
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...task("done"), result_validity: "stale", current_state: current,
   result: { outcome: "no_safe_changes", target_reached: true, observed_state: snapshot, final_state: snapshot } });
  vi.mocked(api.listPreferenceMemories)
   .mockResolvedValueOnce({ items, etag: "etag-1", totalSize: 2, updatedAt: 1, budget: { usedChars: 120, maxChars: 5000 } })
   .mockResolvedValue({ items, etag: "etag-2", totalSize: 2, updatedAt: 1, budget: { usedChars: 1258, maxChars: 1000 } });
  const view = render(<PreferenceMemorySection />);
  await screen.findByText("1258 / 1000");
  expect(api.listPreferenceMemories).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeStale");
  view.unmount();
 });

 it("matches server millisecond timestamps with fractional ISO snapshots", async () => {
  const current = { ...snapshot, updated_at: "2026-10-27T09:10:52.316722Z" };
  vi.mocked(api.listPreferenceMemories).mockResolvedValue({ items, etag: "etag-1", totalSize: 2,
   updatedAt: 1793092252316, budget: { usedChars: 120, maxChars: 5000 } });
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...task("done"), result_validity: "current", current_state: current,
   result: { outcome: "organized", target_reached: true, observed_state: current, final_state: current } });
  const view = render(<PreferenceMemorySection />);
  expect(await screen.findByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeDone");
  expect(api.listPreferenceMemories).toHaveBeenCalledTimes(1);
  view.unmount();
 });

 it("describes retained preferences when organization stops above its target", async () => {
  vi.mocked(api.getLatestPreferenceOrganizer).mockResolvedValue({ ...task("done"), result_validity: "current",
   result: { outcome: "organized_with_remaining", target_reached: false, observed_state: snapshot, final_state: snapshot } });
  const view = render(<PreferenceMemorySection />);
  expect(await screen.findByRole("status")).toHaveTextContent("admin.memoryPreferenceOrganizeRemaining");
  expect(screen.queryByText("admin.memoryPreferenceOrganizeDone")).not.toBeInTheDocument();
  view.unmount();
 });

 it("shows only total and character budget, preserving overflow numbers", async () => {
  vi.mocked(api.listPreferenceMemories).mockResolvedValue({ items, etag: "etag-1", totalSize: 2, updatedAt: 1, budget: { usedChars: 6000, maxChars: 5000 } });
  const view = render(<PreferenceMemorySection />);
  await screen.findByText("6000 / 5000");
  expect(view.container.querySelector(".memory-preference-usage")).toHaveClass("is-error");
  expect(view.container.querySelector(".memory-preference-residency-label")).toBeNull();
  expect(view.container.querySelector(".ant-progress-bg")).toHaveStyle({ width: "100%" });
  view.unmount();
 });

 it("invalidates optimistic sorting statistics until the server responds", async () => {
  let resolve!: (value: api.PreferenceMemoryList) => void;
  vi.mocked(api.reorderPreferenceMemories).mockImplementation(() => new Promise((done) => { resolve = done; }));
  const view = render(<PreferenceMemorySection />);
  await screen.findByText("120 / 5000");
  act(() => state.drag({ active: { id: "pref.a" }, over: { id: "pref.b" } }));
  expect(screen.queryByText("120 / 5000")).not.toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("admin.memoryPreferenceBudgetStale");
  await act(async () => { resolve({ items: [...items].reverse(), etag: "etag-2", totalSize: 2, updatedAt: 2, budget: { usedChars: 130, maxChars: 4000 } }); });
  expect(screen.getByText("130 / 4000")).toBeInTheDocument();
  view.unmount();
 });

 it("keeps deleted items removed and budget stale after refresh failure, then recovers", async () => {
  vi.mocked(api.deletePreferenceMemory).mockResolvedValue(undefined);
  const view = render(<PreferenceMemorySection />);
  await screen.findByText("120 / 5000");
  vi.mocked(api.listPreferenceMemories).mockRejectedValueOnce(new Error("offline"));
  fireEvent.click(screen.getByRole("button", { name: "admin.memoryPreferenceDelete pref.a" }));
  fireEvent.click(await screen.findByRole("button", { name: "common.delete" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "admin.memoryPreferenceDelete pref.a" })).not.toBeInTheDocument());
  expect(screen.queryByText("120 / 5000")).not.toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("admin.memoryPreferenceBudgetStale");
  vi.mocked(api.listPreferenceMemories).mockResolvedValue({ items: items.slice(1), etag: "etag-2", totalSize: 1, updatedAt: 2, budget: { usedChars: 60, maxChars: 5000 } });
  fireEvent.click(screen.getByRole("button", { name: "common.retry" }));
  await screen.findByText("60 / 5000");
  view.unmount();
 });
});
