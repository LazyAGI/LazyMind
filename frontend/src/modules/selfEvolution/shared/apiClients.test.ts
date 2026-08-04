import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: vi.fn(), post: vi.fn() },
  extractErrorCode: vi.fn(() => "2000509"),
  localizeErrorCode: vi.fn((code: string) => `localized:${code}`),
}));

vi.mock("@/api/generated/core-client", () => ({
  AgentApi: class {
    basePathForTest: string;
    constructor(_config: unknown, basePath: string) {
      this.basePathForTest = basePath;
    }
  },
  Configuration: class {
    basePath?: string;
    constructor(options: { basePath?: string }) {
      this.basePath = options.basePath;
    }
  },
  DefaultApi: class {
    basePathForTest: string;
    constructor(_config: unknown, basePath: string) {
      this.basePathForTest = basePath;
    }
  },
  EvalSetsApi: class {
    basePathForTest: string;
    constructor(_config: unknown, basePath: string) {
      this.basePathForTest = basePath;
    }
  },
}));

import { t } from "./i18n";
import {
  createCoreAgentApiClient,
  createCoreAgentGeneratedApiClient,
  createCoreEvalSetsApiClient,
  getCatalogApiErrorMessage,
  getKnowledgeBaseName,
  getSelfEvolutionWorkflowImageSrc,
  isCanceledRequest,
} from "./apiClients";

describe("getSelfEvolutionWorkflowImageSrc", () => {
  it("returns the English image for an en-* language", () => {
    expect(getSelfEvolutionWorkflowImageSrc("en-US")).toBe("/Lazy-e.png");
  });

  it("returns the Chinese image for other/undefined languages", () => {
    expect(getSelfEvolutionWorkflowImageSrc("zh-CN")).toBe("/Lazy-c.png");
    expect(getSelfEvolutionWorkflowImageSrc(undefined)).toBe("/Lazy-c.png");
  });
});

describe("createCoreAgentApiClient / createCoreAgentGeneratedApiClient / createCoreEvalSetsApiClient", () => {
  it("builds each API client bound to the shared BASE_URL", () => {
    expect((createCoreAgentApiClient() as unknown as { basePathForTest: string }).basePathForTest).toBe("https://example.com");
    expect((createCoreAgentGeneratedApiClient() as unknown as { basePathForTest: string }).basePathForTest).toBe("https://example.com");
    expect((createCoreEvalSetsApiClient() as unknown as { basePathForTest: string }).basePathForTest).toBe("https://example.com");
  });
});

describe("getKnowledgeBaseName", () => {
  it("prefers display_name, then name, then dataset_id", () => {
    expect(getKnowledgeBaseName({ display_name: "展示名", name: "n", dataset_id: "d" })).toBe("展示名");
    expect(getKnowledgeBaseName({ name: "n", dataset_id: "d" })).toBe("n");
    expect(getKnowledgeBaseName({ dataset_id: "d" })).toBe("d");
  });

  it("falls back to the unnamed label when all fields are missing", () => {
    expect(getKnowledgeBaseName({})).toBe(t("selfEvolutionRun.unnamedKnowledgeBase"));
  });
});

describe("isCanceledRequest", () => {
  it("detects a canceled axios error by code/name/signal/message", () => {
    expect(isCanceledRequest({ code: "ERR_CANCELED" })).toBe(true);
    expect(isCanceledRequest({ name: "CanceledError" })).toBe(true);
    expect(isCanceledRequest({ config: { signal: { aborted: true } } })).toBe(true);
    expect(isCanceledRequest({ message: "Request aborted" })).toBe(true);
  });

  it("returns false for an unrelated error", () => {
    expect(isCanceledRequest({ message: "network error" })).toBe(false);
  });
});

describe("getCatalogApiErrorMessage", () => {
  it("localizes the extracted error code with a generic fallback", () => {
    expect(getCatalogApiErrorMessage(new Error("boom"))).toBe("localized:2000509");
  });
});
