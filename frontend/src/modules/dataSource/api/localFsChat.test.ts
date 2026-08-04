import { beforeEach, describe, expect, it, vi } from "vitest";

const getMock = vi.fn();
const putMock = vi.fn();

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: (...args: unknown[]) => getMock(...args), put: (...args: unknown[]) => putMock(...args) },
}));

import { getLocalFSChatSetting, updateLocalFSChatSetting } from "./localFsChat";

describe("localFsChat api", () => {
  beforeEach(() => {
    getMock.mockReset();
    putMock.mockReset();
  });

  it("fetches and unwraps the local fs chat setting", async () => {
    getMock.mockResolvedValue({ data: { data: { enabled: true } } });
    const result = await getLocalFSChatSetting();
    expect(getMock).toHaveBeenCalledWith(
      "https://example.com/api/core/data-sources/local-fs-chat-setting",
    );
    expect(result).toEqual({ enabled: true });
  });

  it("handles a non-enveloped response", async () => {
    getMock.mockResolvedValue({ data: { enabled: false } });
    const result = await getLocalFSChatSetting();
    expect(result).toEqual({ enabled: false });
  });

  it("sends the enabled flag when updating the setting", async () => {
    putMock.mockResolvedValue({ data: { data: { enabled: true } } });
    const result = await updateLocalFSChatSetting(true);
    expect(putMock).toHaveBeenCalledWith(
      "https://example.com/api/core/data-sources/local-fs-chat-setting",
      { enabled: true },
    );
    expect(result).toEqual({ enabled: true });
  });

  it("propagates errors from the underlying request", async () => {
    const error = new Error("network down");
    getMock.mockRejectedValue(error);
    await expect(getLocalFSChatSetting()).rejects.toThrow("network down");
  });
});
