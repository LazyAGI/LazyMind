import React from "react";
import { createRoot } from "react-dom/client";
import { ConfigProvider, Button, Space } from "antd";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import type { KnowledgeMarketTaskDetailOpenAPIResponse } from "../../../src/api/generated/core-client";
import { axiosInstance } from "../../../src/components/request";
import KnowledgePage from "../../../src/modules/knowledge/pages/list";
import i18n from "../../../src/i18n";
import "../../../src/index.scss";

// All requests in this standalone fixture stay in memory, including mutations.
// Open with ?latest=1 to check the disabled zero-update state.
let latest = new URLSearchParams(location.search).has("latest");
const tasks = new Map<string, KnowledgeMarketTaskDetailOpenAPIResponse>();
const catalog = ["law", "finance"].map((id) => ({
  id, name: id === "law" ? "中国法律法规知识库" : "金融监管与业务知识库",
  description: "回归测试模拟资料，不连接真实服务。", category: "industry", domain: "测试",
  version: "2", tags: [], icon: "", sort_order: 0, data_source: "官方",
  created_at: "2026-09-01", updated_at: "2026-09-28", online_access_url: "",
}));
const source = {
  source_id: "fixture-cloud", name: "cs_20260731115909_local__gyqp",
  dataset_id: "fixture-dataset", source_kind: "local", status: "active",
  created_at: "2026-09-01", updated_at: "2026-09-28",
};

function task(id: string, batch = false): KnowledgeMarketTaskDetailOpenAPIResponse {
  return {
    job_id: id, job_type: batch ? "knowledge_market_update_all" : "knowledge_market_update",
    job_status: "running", market_item_id: batch ? "" : id,
    name: catalog.find((item) => item.id === id)?.name || "批量检查",
    created_at: "2026-09-28T08:00:00Z", dataset_id: batch ? "" : `fixture-${id}`,
    error_message: "", icon: "", install_state: "vectorizing", attempt_count: 1, max_attempts: 3,
    stage: "parsing", overall_percent: 30, progress: { current: 1, total: 2 },
    parse: { total: 2, done: 0, failed: 0, pending: 0, parsing: 2, state: "parsing" }, payload: {},
  };
}

axiosInstance.defaults.adapter = async (config) => {
  const url = new URL(config.url || "", location.origin);
  const path = url.pathname;
  let data: unknown;
  if (path.endsWith("/knowledge-market:update-all")) {
    tasks.set("batch", task("batch", true));
    data = { job_id: "batch", state: "pending" };
  } else if (path.endsWith(":update")) {
    const id = path.split("/").at(-1)!.replace(":update", "");
    tasks.set(id, task(id));
    data = { job_id: id, state: "pending" };
  } else if (path.endsWith("/knowledge-market/tasks")) {
    const items = [...tasks.values()].filter((item) => item.job_type === url.searchParams.get("job_type"));
    data = { items, total: items.length, page: 1, page_size: 100 };
  } else if (path.includes("/knowledge-market/tasks/")) {
    data = tasks.get(path.split("/").at(-1)!);
  } else if (path.endsWith("/knowledge-market/installs")) {
    data = { total: catalog.length, items: catalog.map((item) => ({
      market_item_id: item.id, name: item.name, active: false, dataset_id: `fixture-${item.id}`,
      domain: item.domain, icon: "", install_state: "done", installed_version: latest ? "2" : "1", updated_at: "2026-09-28",
    })) };
  } else if (path.endsWith("/knowledge-market/domains")) data = { domains: {} };
  else if (path.endsWith("/knowledge-market")) data = { items: catalog, total: catalog.length };
  else if (path.endsWith("/api/scan/sources")) data = { items: [source], total: 1 };
  else if (path.includes("/api/scan/sources/")) data = { source, bindings: [] };
  else if (path.endsWith("/datasets")) data = { datasets: [], total: 0 };
  else if (path.endsWith("/dataset/tags")) data = { tags: ["回归"] };
  else if (path.endsWith("/dataset/algos")) data = { algos: [{ algo_id: "fixture-algo" }] };
  else if (path.endsWith("/learning/catalog")) data = { data: { local_available: true, profiles: [], capabilities: [], question_types: [] } };
  else if (path.endsWith("/learning/profiles")) data = { data: { builtin: [], custom: [] } };
  else data = { ready: true, data: {}, items: [], total: 0 };
  return { config, data, status: 200, statusText: "OK", headers: {} };
};

createRoot(document.getElementById("root")!).render(
  <ConfigProvider>
    <Space style={{ padding: 16 }}>
      <strong>知识库回归 · 模拟数据</strong>
      <Button onClick={() => {
        const batch = tasks.get("batch");
        if (batch) tasks.set("batch", { ...batch, job_status: "succeeded" });
        catalog.forEach((item) => tasks.set(item.id, task(item.id)));
      }}>完成批量检查</Button>
      <Button onClick={() => {
        tasks.forEach((item, id) => tasks.set(id, { ...item, job_status: "succeeded", stage: "done", overall_percent: 100 }));
        latest = true;
      }}>完成模拟更新</Button>
      <Button onClick={() => void i18n.changeLanguage(i18n.language === "en-US" ? "zh-CN" : "en-US")}>中 / EN</Button>
    </Space>
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<KnowledgePage />} />
        <Route path="/lib/knowledge/detail/:id" element={<p>已进入对应资料库详情（模拟）</p>} />
      </Routes>
    </MemoryRouter>
  </ConfigProvider>,
);
