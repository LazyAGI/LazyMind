import type { RecordingFrame } from "./api";

// Capture time-ordered evidence without retaining a raw video or capturing audio.
export async function captureScreen(onEnded: (frames: RecordingFrame[]) => void, onTick: (seconds: number) => void) {
  if (!navigator.mediaDevices?.getDisplayMedia) throw new Error("unsupported");
  const stream = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: 5 }, audio: false });
  const video = document.createElement("video");
  video.muted = true;
  video.srcObject = stream;
  const frames: RecordingFrame[] = [];
  let timer: ReturnType<typeof setInterval> | undefined;
  let finished = false;
  let totalBytes = 0;
  const start = performance.now();
  const startedAt = Date.now();
  const canvas = document.createElement("canvas");
  const cleanup = () => {
    clearInterval(timer);
    stream.getTracks().forEach((track) => { track.onended = null; track.stop(); });
    video.pause(); video.srcObject = null;
    canvas.width = canvas.height = 0;
  };
  const sample = () => {
    if (!video.videoWidth || !video.videoHeight) return;
    const ratio = Math.min(1, 1280 / video.videoWidth, 1280 / video.videoHeight);
    canvas.width = Math.round(video.videoWidth * ratio);
    canvas.height = Math.round(video.videoHeight * ratio);
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("capture failed");
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
    const seconds = (performance.now() - start) / 1000;
    if (!frames.length || seconds > frames[frames.length - 1].seconds) {
      const image = canvas.toDataURL("image/jpeg", 0.65);
      if (image.length > 400000) throw new Error("frame too large");
      frames.push({ image, seconds });
      totalBytes += image.length;
    }
  };
  const finish = (cancel = false) => {
    if (finished) return;
    finished = true;
    try { if (!cancel && frames.length < 120) sample(); }
    catch { /* Submit the valid frames captured before the surface disappeared. */ }
    finally { cleanup(); }
    if (!cancel) onEnded(frames);
  };
  try {
    await video.play();
    sample();
    stream.getVideoTracks()[0].onended = () => finish();
    timer = setInterval(() => {
      try {
        sample();
        onTick(Math.floor((performance.now() - start) / 1000));
        if (frames.length >= 120 || totalBytes > 20 * 1024 * 1024 || (performance.now() - start) >= 120000) finish();
      } catch { finish(); }
    }, 1000);
  } catch (error) { cleanup(); throw error; }
  return { stop: () => finish(), cancel: () => finish(true), startedAt };
}
