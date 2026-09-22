import { describe, expect, it, vi } from "vitest";

import { openFeishuCLIAuthorization } from "./openManagedAuthorization";

function popup() {
  return {
    closed: false,
    close: vi.fn(),
    location: { replace: vi.fn() },
  } as unknown as Window;
}

describe("Feishu CLI authorization navigation", () => {
  it.each([
    "https://open.feishu.cn/page/cli?user_code=fixture&from=cli",
    "https://open.larksuite.com/page/cli?user_code=fixture&from=cli",
    "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture",
    "https://accounts.larksuite.com/oauth/v1/device/verify?flow_id=fixture",
  ])("opens the exact official CLI route %s", async (url) => {
    const reserved = popup();

    await expect(openFeishuCLIAuthorization(url, reserved)).resolves.toEqual({
      ok: true,
    });
    expect(reserved.location.replace).toHaveBeenCalledWith(url);
  });

  it.each([
    "https://open.feishu.cn/app",
    "https://accounts.feishu.cn/page/cli",
    "https://accounts.feishu.cn:444/oauth/v1/device/verify",
    "https://accounts.feishu.cn.example.invalid/oauth/v1/device/verify",
  ])("rejects an untrusted CLI route %s", async (url) => {
    await expect(openFeishuCLIAuthorization(url, popup())).resolves.toEqual({
      ok: false,
      reason: "invalid",
    });
  });
});
