import { describe, expect, it, vi } from "vitest";
import { listKnowledgeMarketTasks } from "./knowledgeMarket";

const { listTasks } = vi.hoisted(() => ({ listTasks: vi.fn() }));
vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  KnowledgeMarketApiFactory: () => ({ apiCoreKnowledgeMarketTasksGet: listTasks }),
}));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));

describe("listKnowledgeMarketTasks", () => {
  it("includes tasks on later pages so running counts do not omit older jobs", async () => {
    const firstPage = Array.from({ length: 100 }, (_, index) => ({ job_id: `job-${index}` }));
    listTasks
      .mockResolvedValueOnce({ data: { data: { items: firstPage, total: 101, page: 1, page_size: 100 } } })
      .mockResolvedValueOnce({ data: { items: [{ job_id: "older-running-job" }], total: 101, page: 2, page_size: 100 } });
    const options = { signal: new AbortController().signal, silentError: true };
    const result = await listKnowledgeMarketTasks("knowledge_market_install", options);
    expect(result.items).toHaveLength(101);
    expect(result.items?.[100]?.job_id).toBe("older-running-job");
    expect(listTasks).toHaveBeenNthCalledWith(2,
      { page: 2, pageSize: 100, jobType: "knowledge_market_install" }, options,
    );
  });
});
