import { beforeEach, afterEach, expect, it, vi } from "vitest";
const get = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({ axiosInstance: { get }, BASE_URL: "http://local" }));
beforeEach(() => { vi.resetModules(); get.mockReset(); });
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); });
it("desktop deduplicates authenticated loads and retries a failed download", async () => {
  vi.stubEnv("VITE_LAZYMIND_MODE", "desktop");
  get.mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ data: new Uint8Array([65,66]).buffer });
  const { getCjkFont } = await import("./pdfFont");
  await expect(getCjkFont()).rejects.toThrow("检查网络后重试");
  expect(await Promise.all([getCjkFont(), getCjkFont()])).toEqual(["AB", "AB"]);
  expect(get).toHaveBeenCalledTimes(2);
  expect(get).toHaveBeenLastCalledWith("http://local/api/core/system-dependencies/pdf-font", expect.objectContaining({ responseType: "arraybuffer" }));
});
it("web keeps the static font and does not call the desktop API", async () => {
  vi.stubEnv("VITE_LAZYMIND_MODE", "web");
  const fetcher = vi.fn().mockResolvedValue({ ok:true, arrayBuffer:async()=>new Uint8Array([67]).buffer });
  vi.stubGlobal("fetch",fetcher);
  const { getCjkFont } = await import("./pdfFont");
  expect(await getCjkFont()).toBe("C");
  expect(fetcher).toHaveBeenCalledWith("/fonts/NotoSansSC-wght.ttf");
  expect(get).not.toHaveBeenCalled();
});
