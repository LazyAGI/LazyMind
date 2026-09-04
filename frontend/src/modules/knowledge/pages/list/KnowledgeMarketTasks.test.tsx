import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeMarketTaskDetailOpenAPIResponse } from "@/api/generated/core-client";

import KnowledgePage from "./index";
import * as marketApi from "@/modules/knowledge/api/knowledgeMarket";

vi.mock("react-i18next", async () => {
  const { default: messages } = await import("@/i18n/locales/zh-CN");
  const t = (key: string, values: Record<string, unknown> = {}) => {
    const value = key.split(".").reduce<any>((result, part) => result?.[part], messages);
    return typeof value === "string"
      ? value.replace(/{{(\w+)}}/g, (_, name) => String(values[name] ?? ""))
      : key;
  };
  return { useTranslation: () => ({ t }) };
});
vi.mock("@/modules/knowledge/api/knowledgeMarket", () => ({
  getKnowledgeMarketItem: vi.fn(),
  getKnowledgeMarketTask: vi.fn(),
  installKnowledgeMarketItem: vi.fn(),
  listKnowledgeMarket: vi.fn(),
  listKnowledgeMarketDomains: vi.fn(),
  listKnowledgeMarketInstalls: vi.fn(),
  listKnowledgeMarketTasks: vi.fn(),
  updateAllKnowledgeMarketItems: vi.fn(),
  updateKnowledgeMarketItem: vi.fn(),
}));
vi.mock("@/modules/knowledge/components/SyncKnowledgeBaseCreationFlow", () => ({
  default: () => null,
  useSyncKnowledgeBaseCreation: () => ({}),
}));
vi.mock("@/components/ui/TypedConfirmModal", async () => ({
  default: (await import("react")).forwardRef(() => null),
}));
vi.mock("@/modules/knowledge/components/UpdateModal", async () => ({
  default: (await import("react")).forwardRef(() => null),
}));
vi.mock("@/modules/knowledge/components/CreateKnowledgeBaseModal", async () => ({
  default: (await import("react")).forwardRef(() => null),
}));
vi.mock("@/components/ui", () => ({ ListPageTable: () => null }));
vi.mock("@/modules/knowledge/components/KnowledgeTag", () => ({ default: () => null }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({}) } }));
vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: { get: vi.fn().mockResolvedValue({ data: { ready: true } }) },
}));
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: vi.fn().mockResolvedValue({ data: { datasets: [] } }),
  }),
}));
vi.mock("@/modules/dataSource/api/clients", () => ({ dataSourceScanApi: {} }));
vi.mock("@/hooks/useModelFeatures", () => ({
  fetchModelFeatures: vi.fn().mockResolvedValue({}),
  isImageEmbedRequired: () => false,
  MODEL_FEATURES_CHANGED_EVENT: "features-changed",
}));

const jobs = new Map<string, KnowledgeMarketTaskDetailOpenAPIResponse>();

function task(id: string, overrides: Partial<KnowledgeMarketTaskDetailOpenAPIResponse> = {}) {
  return {
    job_id: id,
    job_type: "knowledge_market_install",
    job_status: "running",
    market_item_id: id,
    name: `知识库 ${id}`,
    created_at: "2026-09-03T08:00:00Z",
    dataset_id: "",
    error_message: "",
    icon: "",
    install_state: "vectorizing",
    attempt_count: 1,
    max_attempts: 3,
    stage: "parsing",
    overall_percent: 65,
    progress: { current: 2, total: 2 },
    parse: { total: 8, done: 1, failed: 0, pending: 0, parsing: 7, state: "parsing" },
    payload: { market_item_id: id },
    ...overrides,
  };
}

function taskEntry(count: number) {
  return screen.getByRole("button", { name: `后台任务，${count} 个任务进行中` });
}

async function mountPage() {
  await act(async () => {
    render(
      <ConfigProvider button={{ autoInsertSpace: false }} theme={{ token: { motion: false } }}>
        <MemoryRouter><KnowledgePage /></MemoryRouter>
      </ConfigProvider>,
    );
  });
}

async function click(element: HTMLElement) {
  await act(async () => { fireEvent.click(element); });
}

beforeAll(() => {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: false, media: query, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
    dispatchEvent: () => false,
  }));
  globalThis.ResizeObserver = class {
    observe() {} unobserve() {} disconnect() {}
  };
});

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  jobs.clear();
  vi.mocked(marketApi.listKnowledgeMarket).mockResolvedValue(
    ["install", "update"].map((id) => ({
      id, name: `知识库 ${id}`, category: "industry", domain: "测试",
      description: "测试知识库", tags: [], icon: "", data_source: "官方",
      online_access_url: "", sort_order: 0, version: "2",
      created_at: "2026-09-01", updated_at: "2026-09-03",
    })),
  );
  vi.mocked(marketApi.listKnowledgeMarketDomains).mockResolvedValue({ domains: {} });
  vi.mocked(marketApi.listKnowledgeMarketInstalls).mockResolvedValue({
    total: 1,
    items: [{
      market_item_id: "update", name: "知识库 update", active: false,
      dataset_id: "dataset", domain: "测试", icon: "", install_state: "done",
      installed_version: "1", updated_at: "2026-09-01",
    }],
  });
  vi.mocked(marketApi.listKnowledgeMarketTasks).mockImplementation(async (jobType) => ({
    items: [...jobs.values()].filter((job) => job.job_type === jobType),
    total: jobs.size, page: 1, page_size: 100,
  }));
  vi.mocked(marketApi.getKnowledgeMarketTask).mockImplementation(async (jobId) => jobs.get(jobId)!);
  vi.mocked(marketApi.installKnowledgeMarketItem).mockImplementation(async (id) => {
    jobs.set(id, task(id));
    return { job_id: id, state: "pending" };
  });
  vi.mocked(marketApi.updateKnowledgeMarketItem).mockImplementation(async (id) => {
    jobs.set(id, task(id, { job_type: "knowledge_market_update" }));
    return { job_id: id, state: "pending" };
  });
  vi.mocked(marketApi.updateAllKnowledgeMarketItems).mockImplementation(async () => {
    jobs.set("batch", task("batch", { job_type: "knowledge_market_update_all", market_item_id: "" }));
    return { job_id: "batch", state: "pending" };
  });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("knowledge market background tasks", () => {
  it("tracks install and update counts, shows task details, and announces completion at bottom right", async () => {
    await mountPage();
    expect(taskEntry(0).querySelector(".ant-badge-count")).toBeNull();
    await click(screen.getByRole("tab", { name: /知识广场/ }));
    await click(screen.getByRole("button", { name: "安装" }));
    expect(taskEntry(1).querySelector(".ant-badge")).toHaveTextContent("1");
    expect(screen.getByText("已加入后台任务").closest(".ant-notification-bottomRight")).not.toBeNull();
    expect(document.querySelector(".ant-message-notice")).toBeNull();

    await click(screen.getByRole("button", { name: "检查更新" }));
    expect(taskEntry(2).querySelector(".ant-badge")).toHaveTextContent("2");
    await click(taskEntry(2));
    const dialog = screen.getByRole("dialog");
    for (const label of ["任务名称", "任务类型", "状态", "进度", "创建时间"]) {
      expect(within(dialog).getAllByText(label).length).toBeGreaterThan(0);
    }
    expect(within(dialog).getByText("知识库 install")).toBeInTheDocument();
    expect(within(dialog).getByText("知识库 update")).toBeInTheDocument();
    expect(within(dialog).getAllByText("65%")).toHaveLength(2);
    expect(within(dialog).getAllByText(new Date("2026-09-03T08:00:00Z").toLocaleString())).toHaveLength(2);
    await click(within(dialog).getByRole("button", { name: "Close" }));

    jobs.set("install", task("install", { job_status: "succeeded", stage: "done", overall_percent: 86 }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(2)).toBeInTheDocument();
    expect(screen.queryByText("已完成任务")).toBeNull();

    jobs.set("install", task("install", { job_status: "succeeded", stage: "done", overall_percent: 100 }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(1)).toBeInTheDocument();
    expect(screen.getByText("已完成任务").closest(".ant-notification-bottomRight")).not.toBeNull();

    jobs.set("update", task("update", { job_type: "knowledge_market_update", job_status: "succeeded", stage: "done", overall_percent: 100 }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(0)).toBeInTheDocument();
    expect(screen.getAllByText("已完成任务")).toHaveLength(2);
    expect(document.querySelector(".ant-message-notice")).toBeNull();
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(taskEntry(0).querySelector(".ant-badge-count")).toBeNull();
  });

  it("restores active tasks without announcing historical completions, and recovers from a polling failure", async () => {
    jobs.set("running", task("running"));
    jobs.set("finished", task("finished", { job_status: "succeeded", stage: "done", overall_percent: 100 }));
    await mountPage();
    expect(taskEntry(1)).toBeInTheDocument();
    expect(screen.queryByText("已完成任务")).toBeNull();
    expect(screen.queryByText("已加入后台任务")).toBeNull();
    vi.mocked(marketApi.getKnowledgeMarketTask).mockRejectedValueOnce(new Error("network"));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(1)).toBeInTheDocument();
    jobs.set("running", task("running", { job_status: "succeeded", stage: "done", overall_percent: 100 }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(0)).toBeInTheDocument();
    expect(screen.getAllByText("已完成任务")).toHaveLength(1);
  });

  it("counts a batch update and removes failed tasks without a success notification", async () => {
    await mountPage();
    await click(screen.getByRole("tab", { name: "已安装的官方知识库" }));
    await click(screen.getByRole("button", { name: "一键更新" }));
    expect(taskEntry(1)).toBeInTheDocument();
    expect(screen.getByText("已加入后台任务")).toBeInTheDocument();
    jobs.set("batch", task("batch", { job_type: "knowledge_market_update_all", job_status: "failed", stage: "failed" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(0)).toBeInTheDocument();
    expect(screen.queryByText("已完成任务")).toBeNull();
    expect(screen.getByText(/批量检查 处理失败/).closest(".ant-notification-bottomRight")).not.toBeNull();
  });

  it("does not count historical installs again while the same knowledge base is updating", async () => {
    jobs.set("old-install", task("old-install", {
      market_item_id: "same-item", job_status: "succeeded", created_at: "2026-09-01T08:00:00Z",
    }));
    jobs.set("new-update", task("new-update", {
      market_item_id: "same-item", job_type: "knowledge_market_update",
    }));
    await mountPage();
    expect(taskEntry(1)).toBeInTheDocument();
    await click(taskEntry(1));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("知识库 new-update")).toBeInTheDocument();
    expect(within(dialog).queryByText("知识库 old-install")).toBeNull();
  });

  it("keeps counting updates spawned by a completed batch check", async () => {
    await mountPage();
    await click(screen.getByRole("tab", { name: "已安装的官方知识库" }));
    await click(screen.getByRole("button", { name: "一键更新" }));
    jobs.set("batch", task("batch", {
      market_item_id: "", job_type: "knowledge_market_update_all", job_status: "succeeded",
    }));
    jobs.set("child", task("child", { job_type: "knowledge_market_update" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(1)).toBeInTheDocument();
    jobs.set("child", task("child", {
      job_type: "knowledge_market_update", job_status: "succeeded", stage: "done", overall_percent: 100,
    }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(taskEntry(0)).toBeInTheDocument();
    expect(screen.getAllByText("已完成任务")).toHaveLength(2);
  });

  it("does not add a task or show a submitted notification when submission fails", async () => {
    vi.mocked(marketApi.installKnowledgeMarketItem).mockRejectedValueOnce(new Error("failed"));
    await mountPage();
    await click(screen.getByRole("tab", { name: /知识广场/ }));
    await click(screen.getByRole("button", { name: "安装" }));
    expect(taskEntry(0)).toBeInTheDocument();
    expect(screen.queryByText("已加入后台任务")).toBeNull();
  });
});
