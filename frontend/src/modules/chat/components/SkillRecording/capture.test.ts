import { afterEach, describe, expect, it, vi } from "vitest";
import { captureScreen } from "./capture";

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); vi.unstubAllGlobals(); });
function setup() {
  vi.useFakeTimers();
  const track = { stop: vi.fn(), onended: null as null | (() => void) };
  const video = { muted: false, srcObject: null, videoWidth: 1920, videoHeight: 1080, play: vi.fn().mockResolvedValue(undefined), pause: vi.fn() };
  const canvas = { width: 0, height: 0, getContext: () => ({ drawImage: vi.fn() }), toDataURL: () => "data:image/jpeg;base64,/9j/" };
  vi.spyOn(document, "createElement").mockImplementation(((name: string) => name === "video" ? video : canvas) as typeof document.createElement);
  vi.stubGlobal("navigator", { mediaDevices: { getDisplayMedia: vi.fn().mockResolvedValue({ getTracks: () => [track], getVideoTracks: () => [track] }) } });
  return { track, video };
}
describe("screen recording lifecycle", () => {
  it("stop releases tracks and emits ordered evidence once", async () => {
    const { track, video } = setup(); const complete = vi.fn();
    const recording = await captureScreen(complete, vi.fn());
    await vi.advanceTimersByTimeAsync(2100);
    recording.stop(); recording.stop();
    expect(complete).toHaveBeenCalledTimes(1);
    const frames = complete.mock.calls[0][0];
    expect(frames.length).toBeGreaterThanOrEqual(2);
    expect(frames[1].seconds).toBeGreaterThan(frames[0].seconds);
    expect(track.stop).toHaveBeenCalledTimes(1); expect(video.srcObject).toBeNull();
  });
  it("cancel releases capture without submitting evidence", async () => {
    const { track } = setup(); const complete = vi.fn();
    const recording = await captureScreen(complete, vi.fn()); recording.cancel();
    await vi.advanceTimersByTimeAsync(5000);
    expect(complete).not.toHaveBeenCalled(); expect(track.stop).toHaveBeenCalledOnce();
  });
  it("browser stop sharing completes the recording", async () => {
    const { track } = setup(); const complete = vi.fn();
    await captureScreen(complete, vi.fn()); await vi.advanceTimersByTimeAsync(2000);
    track.onended?.(); expect(complete).toHaveBeenCalledOnce(); expect(track.stop).toHaveBeenCalledOnce();
  });
  it("play failure still releases the media stream", async () => {
    const { track, video } = setup(); video.play.mockRejectedValue(new Error("play failed"));
    await expect(captureScreen(vi.fn(), vi.fn())).rejects.toThrow("play failed");
    expect(track.stop).toHaveBeenCalledOnce();
  });
});
