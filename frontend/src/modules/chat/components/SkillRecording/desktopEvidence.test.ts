import { afterEach, describe, expect, it, vi } from "vitest";
import { collectDesktopEvidence, desktopInputAvailable, prepareDesktopInput } from "./desktopEvidence";

afterEach(() => { delete window.lazymindDesktop; vi.restoreAllMocks(); });
function bridge() {
  const value = {
    recordingInputPermission: vi.fn().mockResolvedValue({ granted: true }),
    recordingInputStart: vi.fn().mockResolvedValue({ session_id: "session" }),
    recordingInputStop: vi.fn().mockResolvedValue({ events: [{ seconds: 2, kind: "mouseup" }, { seconds: 1, kind: "mousedown" }, { seconds: 4, kind: "keydown" }], limitations: ["desktop_keyboard_is_global_no_dom"] }),
    recordingInputCancel: vi.fn().mockResolvedValue(undefined),
  };
  window.lazymindDesktop = value;
  return value;
}
describe("desktop operation evidence", () => {
  it("does not claim native input availability in an ordinary browser", async () => {
    expect(desktopInputAvailable()).toBe(false);
    await prepareDesktopInput();
  });
  it("requires explicit system permission", async () => {
    const api = bridge(); api.recordingInputPermission.mockResolvedValue({ granted: false });
    await expect(prepareDesktopInput()).rejects.toThrow("recording-input-permission");
    expect(api.recordingInputStart).not.toHaveBeenCalled();
  });
  it("aligns events to stop time and releases the native session", async () => {
    const api = bridge(); vi.spyOn(Date, "now").mockReturnValue(4000);
    const recorder = await collectDesktopEvidence(1000);
    const result = await recorder.finish();
    expect(result.events.map((event) => event.seconds)).toEqual([1, 2]);
    expect(api.recordingInputCancel).toHaveBeenCalledWith("session");
    await recorder.cancel();
    expect(api.recordingInputCancel).toHaveBeenCalledTimes(1);
  });
  it("does not return incomplete evidence as success after native capture fails", async () => {
    const api = bridge(); api.recordingInputStop.mockRejectedValue(new Error("expired"));
    const recorder = await collectDesktopEvidence(1000);
    await expect(recorder.finish()).rejects.toThrow("expired");
    expect(api.recordingInputCancel).toHaveBeenCalledWith("session");
  });
});
