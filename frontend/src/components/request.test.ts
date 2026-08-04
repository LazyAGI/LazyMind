import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const getUserInfoMock = vi.hoisted(() => vi.fn());
const getAuthHeadersMock = vi.hoisted(() => vi.fn());
const isLoggedInMock = vi.hoisted(() => vi.fn());
const logoutMock = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
const refreshAccessTokenMock = vi.hoisted(() => vi.fn());
const messageErrorMock = vi.hoisted(() => vi.fn());
const messageWarningMock = vi.hoisted(() => vi.fn());
const isLocalSessionEnabledMock = vi.hoisted(() => vi.fn(() => false));
const ensureLocalSessionMock = vi.hoisted(() => vi.fn());
const restoreLocalSessionAndGetTokenMock = vi.hoisted(() => vi.fn());

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: getUserInfoMock,
    getAuthHeaders: getAuthHeadersMock,
    isLoggedIn: isLoggedInMock,
    logout: logoutMock,
    refreshAccessToken: refreshAccessTokenMock,
  },
}));

vi.mock("antd", () => ({
  message: {
    error: messageErrorMock,
    warning: messageWarningMock,
  },
}));

vi.mock("@/runtime/localSession", () => ({
  ensureLocalSession: ensureLocalSessionMock,
  isLocalSessionEnabled: isLocalSessionEnabledMock,
  localSessionInitialized: false,
  restoreLocalSessionAndGetToken: restoreLocalSessionAndGetTokenMock,
}));

import {
  BASE_URL,
  extractErrorCode,
  getLocalizedErrorMessage,
  handleError,
  localizeErrorCode,
} from "./request";

describe("BASE_URL", () => {
  it("resolves to a non-null string derived from the api base resolver", () => {
    expect(typeof BASE_URL).toBe("string");
  });
});

describe("extractErrorCode", () => {
  it("returns undefined when there is no recognizable error code", () => {
    expect(extractErrorCode({})).toBeUndefined();
    expect(extractErrorCode({ response: { data: {} } })).toBeUndefined();
  });

  it("reads the error code from the top-level response payload", () => {
    expect(
      extractErrorCode({ response: { data: { code: "2000509" } } }),
    ).toBe("2000509");
  });

  it("reads the error code from a nested data.error_code field", () => {
    expect(
      extractErrorCode({
        response: { data: { data: { error_code: "2001102" } } },
      }),
    ).toBe("2001102");
  });

  it("falls back to the raw error object when there is no response", () => {
    expect(extractErrorCode({ code: "2000104" })).toBe("2000104");
  });
});

describe("localizeErrorCode", () => {
  it("returns the fallback when the code does not exist in the catalog", () => {
    expect(localizeErrorCode("9999999", "fallback text")).toBe("fallback text");
  });

  it("returns an empty string fallback by default for unknown codes", () => {
    expect(localizeErrorCode("9999999")).toBe("");
  });

  it("resolves a known error code to its translated message", () => {
    // 2000509 is used throughout the codebase as the generic request error code.
    expect(localizeErrorCode("2000509")).not.toBe("");
  });
});

describe("getLocalizedErrorMessage", () => {
  it("prefers the known error-code translation when present", () => {
    const message = getLocalizedErrorMessage({
      response: { data: { code: "2000509" }, status: 500 },
    });
    expect(message).toBe(localizeErrorCode("2000509"));
  });

  it("maps a plain HTTP status to the generic request error catalog when no code is present", () => {
    const message = getLocalizedErrorMessage({ response: { data: {}, status: 404 } });
    expect(message).toBe(localizeErrorCode("2000106"));
  });

  it("falls back to the generic request error for network-style errors without a response", () => {
    const message = getLocalizedErrorMessage({ isAxiosError: true, code: "ERR_NETWORK" });
    expect(message).toBe(localizeErrorCode("2000509"));
  });
});

describe("handleError", () => {
  beforeEach(() => {
    getUserInfoMock.mockReturnValue(null);
    getAuthHeadersMock.mockReturnValue({});
    isLoggedInMock.mockReturnValue(false);
    isLocalSessionEnabledMock.mockReturnValue(false);
    messageErrorMock.mockClear();
    messageWarningMock.mockClear();
    logoutMock.mockClear();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("re-rejects canceled requests without showing any message", async () => {
    const error = { code: "ERR_CANCELED", config: {} };
    await expect(handleError(error as never)).rejects.toBe(error);
    expect(messageErrorMock).not.toHaveBeenCalled();
  });

  it("shows an error message for a generic non-401/403 failure response", async () => {
    const error = {
      response: { status: 500, data: {} },
      config: {},
    };
    await expect(handleError(error as never)).rejects.toBe(error);
    expect(messageErrorMock).toHaveBeenCalledTimes(1);
  });

  it("suppresses the error message when the request is marked silentError", async () => {
    const error = {
      response: { status: 500, data: {} },
      config: { silentError: true },
    };
    await expect(handleError(error as never)).rejects.toBe(error);
    expect(messageErrorMock).not.toHaveBeenCalled();
  });

  it("logs out and rejects on 403 with a disabled-user error code", async () => {
    const error = {
      response: { status: 403, data: { code: "1000106" } },
      config: {},
    };
    await expect(handleError(error as never)).rejects.toBe(error);
    expect(logoutMock).toHaveBeenCalledTimes(1);
  });

  it("shows a network error message when there is no response at all", async () => {
    const error = { request: {}, config: {} };
    await expect(handleError(error as never)).rejects.toBe(error);
    expect(messageErrorMock).toHaveBeenCalledTimes(1);
  });
});
