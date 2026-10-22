import { AxiosError } from "axios";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { axiosInstance, getLocalizedErrorMessage } from "./request";

const requestUi = vi.hoisted(() => ({ error: vi.fn(), warning: vi.fn() }));
vi.mock("antd", () => ({ message: requestUi }));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: () => ({ userId: "local-owner" }),
    getAuthHeaders: () => ({}),
  },
}));
vi.mock("@/runtime/localSession", () => ({
  isLocalSessionEnabled: () => false,
  localSessionInitialized: true,
}));

const originalAdapter = axiosInstance.defaults.adapter;

beforeEach(() => vi.clearAllMocks());
afterEach(async () => {
  axiosInstance.defaults.adapter = originalAdapter;
  await i18n.changeLanguage("zh-CN");
});

it.each([
  {
    locale: "zh-CN", status: 503, code: "CHANNEL_GATEWAY_UNAVAILABLE",
    expected: "通知与渠道服务暂未就绪，请稍后重试；若持续失败，请重启本地服务。",
  },
  {
    locale: "zh-CN", status: 504, code: "CHANNEL_GATEWAY_TIMEOUT",
    expected: "通知与渠道服务响应超时，请稍后重试；若持续失败，请重启本地服务。",
  },
  {
    locale: "en-US", status: 503, code: "CHANNEL_GATEWAY_UNAVAILABLE",
    expected: "Notification and channel service is not ready. Please try again later; if the problem persists, restart the local service.",
  },
  {
    locale: "en-US", status: 504, code: "CHANNEL_GATEWAY_TIMEOUT",
    expected: "Notification and channel service timed out. Please try again later; if the problem persists, restart the local service.",
  },
])("shows actionable channel service errors in $locale for HTTP $status", async ({ locale, status, code, expected }) => {
  await i18n.changeLanguage(locale);
  let failure: AxiosError | undefined;
  axiosInstance.defaults.adapter = async config => {
    failure = new AxiosError("transport error", "ERR_BAD_RESPONSE", config, {}, {
      status,
      statusText: "Gateway error",
      headers: {},
      config,
      data: { code, error: "dial tcp private.internal:8085: connect: connection refused" },
    });
    throw failure;
  };

  await expect(axiosInstance.get("/api/channel-gateway/v1/channel-accounts")).rejects.toBeInstanceOf(AxiosError);

  expect(getLocalizedErrorMessage(failure)).toBe(expected);
  expect(requestUi.error).toHaveBeenCalledTimes(1);
  expect(requestUi.error).toHaveBeenCalledWith({ key: "api-request-error", content: expected });
  expect(requestUi.warning).not.toHaveBeenCalled();
});
