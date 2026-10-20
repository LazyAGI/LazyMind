import axios from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import { listKnowledgeMarketTaskFiles, listKnowledgeMarketTaskPage, listKnowledgeMarketTasks } from "./knowledgeMarket";

vi.mock("@/components/request", async () => ({
  BASE_URL: "http://test.invalid", axiosInstance: (await import("axios")).default.create(),
}));
const urls: URL[] = [];
beforeEach(() => {
  urls.length = 0;
  axiosInstance.defaults.adapter = async (config) => {
    urls.push(new URL(config.url || "", "http://test.invalid"));
    return {data: {code: 0, data: {items: [], total: 201, page: Number(urls[urls.length - 1]?.searchParams.get("page")), page_size: 10}}, status: 200, statusText: "OK", config, headers: {}};
  };
});
describe("knowledge market task pagination requests", () => {
  it("requests one summary page across all task types", async () => {
    await listKnowledgeMarketTaskPage(2);
    expect(urls).toHaveLength(1);
    expect(Object.fromEntries(urls[0].searchParams)).toEqual({job_type: "all", view: "summary", page: "2", page_size: "10"});
  });
  it("keeps the all-pages helper used by the global task badge", async () => {
    await listKnowledgeMarketTasks("knowledge_market_install");
    expect(urls.map((url) => url.searchParams.get("page"))).toEqual(["1", "2", "3"]);
    expect(urls.every((url) => url.searchParams.get("page_size") === "100")).toBe(true);
  });
  it("encodes the job and requests a filtered file page", async () => {
    await listKnowledgeMarketTaskFiles("job/with spaces", "unfinished", 3);
    expect(urls[0].pathname).toBe("/api/core/knowledge-market/tasks/job%2Fwith%20spaces/files");
    expect(Object.fromEntries(urls[0].searchParams)).toEqual({status: "unfinished", page: "3", page_size: "10"});
  });
  it("passes cancellation to the generated client", async () => {
    const controller = new AbortController(); controller.abort();
    await expect(listKnowledgeMarketTaskPage(1, {signal: controller.signal})).rejects.toSatisfy(axios.isCancel);
    expect(urls).toHaveLength(0);
  });
});
