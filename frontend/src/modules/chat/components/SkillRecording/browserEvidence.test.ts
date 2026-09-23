import { afterEach, expect, it, vi } from "vitest";
import { collectBrowserEvidence } from "./browserEvidence";
import { recordingBrowser } from "./api";
vi.mock("./api");
afterEach(() => { vi.useRealTimers(); vi.resetAllMocks(); });
const target = { device_id: "device", tab_id: "1", title: "Page", origin: "https://example.com" };
it("combines timestamped events and preserves navigation gaps", async () => {
  vi.useFakeTimers();
  vi.mocked(recordingBrowser).mockImplementation(async (action) => {
    if (action === "start") return { session_id: "r" };
    if (action === "read") return { events: [{ kind: "click", seconds: 2 }], gaps: true };
    return { events: [{ kind: "dom", seconds: 1 }, { kind: "input", seconds: 99, value: "after stop" }] };
  });
  const controller = await collectBrowserEvidence(target, Date.now() - 3000);
  await vi.advanceTimersByTimeAsync(500);
  const evidence = await controller.finish();
  expect(evidence.events.map((event) => event.seconds)).toEqual([1, 2]);
  expect(evidence.limitations).toContain("events_or_dom_missing_during_navigation");
  expect(recordingBrowser).toHaveBeenCalledWith("stop", { device_id: "device", session_id: "r" });
});
it("cancel stops polling and cancels only its own browser session", async () => {
  vi.useFakeTimers();
  vi.mocked(recordingBrowser).mockResolvedValue({ session_id: "r" });
  const controller = await collectBrowserEvidence(target, Date.now() - 3000);
  await controller.cancel();
  await vi.advanceTimersByTimeAsync(2000);
  expect(recordingBrowser).toHaveBeenCalledTimes(2);
  expect(recordingBrowser).toHaveBeenLastCalledWith("cancel", { device_id: "device", session_id: "r" });
});
