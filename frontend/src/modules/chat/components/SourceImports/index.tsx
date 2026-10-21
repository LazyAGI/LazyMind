import { useTranslation } from "react-i18next";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, Button, Form, Input, Modal, Popover, Radio, Select, Space, message } from "antd";
import { CloseOutlined, DatabaseOutlined, PlusOutlined } from "@ant-design/icons";
import axios from "axios";
import { v4 as uuid } from "uuid";
import { type ChatSource, type ChatSourceCollection, openSource } from "../../utils/sourceAdapter";
import type { KnowledgeOperation } from "../../utils/knowledgeOperations";
import KnowledgeOperationCards, { importStatusLabel } from "../KnowledgeOperationCards";
import { getKnowledgeOptions, type KnowledgeOptions } from "@/modules/knowledge/utils/request";
import { KNOWLEDGE_BASE_NAME_MAX_LENGTH, KNOWLEDGE_BASE_NAME_PATTERN } from "@/modules/knowledge/constants/validation";
import TagSelect from "@/modules/knowledge/components/TagSelect";
import * as api from "./api";
import "./index.scss";

interface ImportContext {
  inspections: api.InspectedSource[];
  inspectError: boolean;
  items: api.ImportItem[];
  queryError: boolean;
  choose: (sources: api.InspectedSource[]) => void;
  refresh: () => Promise<void>;
  submittedBatch: string;
  opened: boolean;
  setOpened: (open: boolean) => void;
}
const Context = createContext<ImportContext | null>(null);
export const useSourceImports = () => useContext(Context);
export function findInspectedSource(rows: api.InspectedSource[], source: ChatSource) {
  // Persisted IDs are preferred; URL fallback covers public Work projection IDs.
  const id = String(source.citation_id ?? source.index ?? "");
  return rows.find(row => row.source_id === id && row.url === source.url)
    ?? rows.find(row => Boolean(source.url) && row.url === source.url);
}
export function uniquePapers(rows: api.InspectedSource[]) {
  return [...new Map(rows.filter(row => row.eligible && row.paper_key).map(row => [row.paper_key, row])).values()];
}
function errorText(error: unknown) {
  return axios.isAxiosError(error) ? error.response?.data?.message || error.message : error instanceof Error ? error.message : "操作失败，请重试";
}

export function SourceImportProvider({ context, sources, children }: { context: api.SourceContext; sources?: ChatSourceCollection; children: ReactNode }) {
  const key = JSON.stringify(context);
  const stableContext = useMemo(() => context, [key]);
  const enabled = Boolean(context.workflow_session_id || context.task_id || (context.conversation_id && context.history_id));
  const [inspections, setInspections] = useState<api.InspectedSource[]>([]);
  const [inspectError, setInspectError] = useState(false);
  const [items, setItems] = useState<api.ImportItem[]>([]);
  const [queryError, setQueryError] = useState(false);
  const [opened, setOpened] = useState(false);
  const [submittedBatch, setSubmittedBatch] = useState("");
  const [selection, setSelection] = useState<api.InspectedSource[] | null>(null);
  const identity = useRef(key); identity.current = key;
  const refresh = useCallback(async () => {
    if (!enabled) return;
    try {
      const rows = await api.listSourceImports(stableContext);
      if (identity.current !== key) return;
      setItems(previous => rows.map(row => row.status === "unknown" ? { ...row, ...previous.find(old => old.item_id === row.item_id), state_unavailable: true } : row));
      setQueryError(false);
    } catch { if (identity.current === key) setQueryError(true); }
  }, [enabled, key, stableContext]);
  useEffect(() => { setItems([]); setInspections([]); setSelection(null); setOpened(false); setSubmittedBatch(""); }, [key]);
  const sourceKey = JSON.stringify(sources ?? []);
  useEffect(() => {
    if (!enabled) return;
    let disposed = false;
    if (sources && Object.keys(sources).length) void api.inspectSources(stableContext).then(rows => { if (!disposed) { setInspections(rows); setInspectError(false); } }).catch(() => { if (!disposed) setInspectError(true); });
    void refresh();
    return () => { disposed = true; };
  }, [key, sourceKey, enabled, stableContext, refresh]);
  useEffect(() => {
    if (!opened) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (document.hidden || stopped) return;
      await refresh();
      if (!stopped && items.some(row => row.state_unavailable || ["pending", "running", "unknown"].includes(row.status))) timer = setTimeout(tick, 3000);
    };
    const visibility = () => { clearTimeout(timer); if (!document.hidden) void tick(); };
    void tick(); document.addEventListener("visibilitychange", visibility);
    return () => { stopped = true; clearTimeout(timer); document.removeEventListener("visibilitychange", visibility); };
  }, [opened, refresh, items.map(row => `${row.item_id}:${row.status}:${row.state_unavailable}`).join("|")]);
  return <Context.Provider value={{ inspections, inspectError, items, queryError, choose: rows => setSelection(uniquePapers(rows)), refresh, submittedBatch, opened, setOpened }}>
    {children}
    <SourceImportResults />
    {selection && <TargetModal rows={selection} context={stableContext} onCancel={() => setSelection(null)} onSubmitted={batch => { setSelection(null); setSubmittedBatch(batch); setOpened(true); void refresh(); }} />}
  </Context.Provider>;
}

function TargetModal({ rows, context, onCancel, onSubmitted }: { rows: api.InspectedSource[]; context: api.SourceContext; onCancel: () => void; onSubmitted: (batch: string) => void }) {
  const { t } = useTranslation();
  const [targets, setTargets] = useState<api.SourceTarget[]>([]);
  const [options, setOptions] = useState<KnowledgeOptions>();
  const [mode, setMode] = useState("existing");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [createdTarget, setCreatedTarget] = useState("");
  const [form] = Form.useForm();
  const request = useRef<{ signature: string; value: api.SourceImportRequest }>();
  useEffect(() => {
    let active = true;
    Promise.all([api.sourceTargets(), getKnowledgeOptions()]).then(([rows, config]) => {
      if (!active) return;
      setTargets(rows); setOptions(config);
      form.setFieldsValue({ scene: "academic_papers", processing_level: config.default_processing_level, algo_id: config.default_algo_id, tags: ["论文"] });
    }).catch(e => { if (active) setError(errorText(e)); });
    return () => { active = false; };
  }, [form]);
  const submit = async () => {
    if (busy) return;
    try {
      const values = await form.validateFields();
      const body = { source_context: context, source_ids: rows.map(row => row.source_id), ...(mode === "existing" ? { target_dataset_id: values.target } : { new_knowledge_base: { name: values.name, tags: values.tags, description: values.description, scene: values.scene, processing_level: values.processing_level, algo_id: values.algo_id } }) };
      const signature = JSON.stringify(body);
      if (!request.current || (signature !== request.current.signature && !createdTarget)) request.current = { signature, value: { ...body, idempotency_key: uuid() } };
      setBusy(true); setError("");
      const result = await api.submitSourceImport(request.current.value);
      onSubmitted(result.batch_id);
    } catch (e) {
      if (e && typeof e === "object" && "errorFields" in e) return;
      setError(errorText(e));
      if (axios.isAxiosError(e) && e.response?.data?.target_dataset_id && mode === "new") setCreatedTarget(e.response.data.target_dataset_id);
    } finally { setBusy(false); }
  };
  const levels: Record<string, string> = { stored: "仅保存原件", parsed: "解析", chunked: "解析并分段", indexed: "解析并索引" };
  return <Modal open title="加入知识库" okText={createdTarget ? "重试加入" : "确认加入"} cancelText="取消" confirmLoading={busy} onOk={() => void submit()} onCancel={busy ? undefined : onCancel} maskClosable={!busy} closable={!busy} cancelButtonProps={{ disabled: busy }}>
    <p>已选择 {rows.length} 篇文献</p>
    {error && <Alert type="error" showIcon message={error} />}
    {createdTarget && <Alert type="info" message={<span>知识库已创建，入库任务尚未创建。可重试加入或<a href={`/lib/knowledge/detail/${encodeURIComponent(createdTarget)}`}>查看知识库</a>。</span>} />}
    <Radio.Group value={mode} disabled={busy || Boolean(createdTarget)} onChange={(e: { target: { value: string } }) => setMode(e.target.value)} style={{ margin: "16px 0" }}><Radio.Button value="existing">已有知识库</Radio.Button><Radio.Button value="new">新建知识库</Radio.Button></Radio.Group>
    <Form form={form} layout="vertical" disabled={busy || Boolean(createdTarget)}>
      {mode === "existing" ? <Form.Item name="target" label="目标知识库" rules={[{ required: true, message: "请选择知识库" }]}><Select showSearch optionFilterProp="label" placeholder="选择有编辑权限的知识库" options={targets.map(row => ({ value: row.dataset_id, label: row.display_name }))} /></Form.Item> : <>
        <Form.Item name="name" label="名称" rules={[{ required: true, message: "请输入名称" }, { pattern: KNOWLEDGE_BASE_NAME_PATTERN, message: "仅支持中英文、数字、下划线、点和短横线" }]}><Input maxLength={KNOWLEDGE_BASE_NAME_MAX_LENGTH} /></Form.Item>
        <Form.Item name="tags" label="标签" rules={[{ required: true, type: "array", min: 1, message: "请添加标签" }]}><TagSelect tags={[]} maxTagLength={20} /></Form.Item>
        <Form.Item name="description" label="描述"><Input.TextArea maxLength={1000} /></Form.Item>
        <Form.Item name="scene" label="使用场景" rules={[{ required: true }]}><Select options={options?.scenes.map(row => ({ value: row.key, label: t(row.name_i18n_key) }))} /></Form.Item>
        <Form.Item name="processing_level" label="处理方式" rules={[{ required: true }]}><Select options={options?.processing_levels.map(value => ({ value, label: levels[value] || value }))} /></Form.Item>
      </>}
    </Form>
    <small>确认后在后台处理，关闭页面不影响已创建的任务。</small>
  </Modal>;
}

export function SourceImportActions({ source }: { source: ChatSource }) {
  const ctx = useSourceImports();
  const inspected = ctx && findInspectedSource(ctx.inspections, source);
  const matching = ctx?.items.filter(item => item.paper_key === inspected?.paper_key) || [];
  const latest = matching[matching.length - 1];
  return <>
    {latest && <small className="source-import-current-state">{latest.target_name} · {importStatusLabel(latest)}{latest.state_unavailable && "（状态暂时无法获取）"}<Button type="link" size="small" onMouseDown={event => event.preventDefault()} onClick={() => ctx?.setOpened(true)}>入库记录</Button></small>}
    <div className="source-import-actions"><Button onMouseDown={event => event.preventDefault()} onClick={() => openSource(source)}>打开原文</Button>{inspected?.eligible && <Button type="primary" aria-label="加入知识库" icon={<PlusOutlined aria-hidden />} onMouseDown={event => event.preventDefault()} onClick={() => ctx?.choose([inspected])}>加入知识库</Button>}</div>
  </>;
}

export function ExecutionResults({ operations = [] }: { operations?: KnowledgeOperation[] }) {
  const [opened, setOpened] = useState(false);
  if (!operations.some(operation => operation.items.length > 0)) return null;
  return <Popover trigger="click" placement="topLeft" open={opened} onOpenChange={setOpened} destroyOnHidden overlayClassName="chat-sources-popover" content={<div className="execution-results-panel" role="dialog" aria-label="Agent 执行结果">
    <div className="chat-source-panel-header"><h2 className="chat-source-panel-title">Agent 执行结果</h2><Button type="text" aria-label="关闭 Agent 执行结果" icon={<CloseOutlined />} onClick={() => setOpened(false)} /></div>
    <KnowledgeOperationCards operations={operations} />
  </div>}><Button className="tool-btn source-btn" icon={<DatabaseOutlined aria-hidden />} aria-label="Agent 执行结果" aria-expanded={opened}>Agent 执行结果</Button></Popover>;
}

function SourceImportResults() {
  const ctx = useSourceImports();
  const [retrying, setRetrying] = useState(false);
  const retryKey = useRef<Map<string, string>>(new Map());
  const opened = ctx?.opened ?? false;
  const items = ctx?.items ?? [];
  const observed = useRef(new Set<string>());
  const submitted = useRef("");
  useEffect(() => {
    if (!opened || document.hidden) { observed.current.clear(); return; }
    if (ctx?.submittedBatch && submitted.current !== ctx.submittedBatch) { observed.current.add(ctx.submittedBatch); submitted.current = ctx.submittedBatch; }
    for (const row of items) if (["pending", "running"].includes(row.status)) observed.current.add(row.batch_id);
    const finished = [...observed.current].filter(batch => items.some(row => row.batch_id === batch) && !items.some(row => row.batch_id === batch && (["pending", "running", "unknown"].includes(row.status) || row.state_unavailable)));
    finished.forEach(batch => observed.current.delete(batch));
    const failures = items.filter(row => finished.includes(row.batch_id) && row.status === "failed");
    if (failures.length) Modal.error({ title: "加入失败", content: <ul>{failures.map(row => <li key={row.item_id}>{row.title}：{row.error_message || "处理失败"}</li>)}</ul> });
  }, [opened, items, ctx?.submittedBatch]);

  const retry = async (rows: api.ImportItem[]) => {
    if (retrying) return;
    setRetrying(true);
    try {
      const groups = new Map<string, string[]>();
      rows.filter(row => row.retryable).forEach(row => { const batch = row.retry_batch_id || row.batch_id; groups.set(batch, [...groups.get(batch) || [], row.retry_item_id || row.item_id]); });
      const outcomes = await Promise.allSettled([...groups].map(async ([batch, ids]) => {
        const identity = `${batch}:${ids.sort().join(",")}`;
        const key = retryKey.current.get(identity) || uuid(); retryKey.current.set(identity, key);
        await api.retrySourceImport(batch, ids, key); retryKey.current.delete(identity);
      }));
      const failure = outcomes.find(outcome => outcome.status === "rejected");
      if (failure?.status === "rejected") message.error(errorText(failure.reason));
      await ctx?.refresh();
    } finally { setRetrying(false); }
  };
  return <Modal title="文献入库记录" open={opened} onCancel={() => ctx?.setOpened(false)} footer={null} width={480} destroyOnHidden>
    {ctx?.queryError && <Alert type="warning" message="状态暂时无法获取，保留上次查询结果" action={<Button size="small" onClick={() => void ctx.refresh()}>重试查询</Button>} />}
    <KnowledgeOperationCards imports={items} onRetry={rows => void retry(rows)} retrying={retrying} onRefreshImports={ctx?.refresh} />
    {items.some(row => row.retryable) && <Space className="execution-results-footer"><Button loading={retrying} onClick={() => void retry(items)}>仅重试失败项</Button><Button onClick={() => Modal.error({ title: "加入失败", content: <ul>{items.filter(row => row.status === "failed" || row.processing_status === "failed").map(row => <li key={row.item_id}>{row.title}：{row.error_message || "处理失败"}</li>)}</ul> })}>查看失败明细</Button></Space>}
  </Modal>;
}
