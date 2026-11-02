import { tr } from "./translations";
import { knowledgeCreationDefaults, knowledgeCreationMetadata } from "@/modules/knowledge/utils/creation";
import { sourceContextKey, useSourceImportState } from "./state";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, Button, Form, Input, Modal, Popover, Radio, Select, Space, message, theme } from "antd";
import { CloseOutlined, DatabaseOutlined, PlusOutlined } from "@ant-design/icons";
import axios from "axios";
import { v4 as uuid } from "uuid";
import { type ChatSource, type ChatSourceCollection, openSource } from "../../utils/sourceAdapter";
import type { KnowledgeOperation } from "../../utils/knowledgeOperations";
import KnowledgeOperationCards, { importStatusLabel } from "../KnowledgeOperationCards";
import { getKnowledgeOptions, type KnowledgeOptions } from "@/modules/knowledge/utils/request";
import { KNOWLEDGE_BASE_NAME_MAX_LENGTH, KNOWLEDGE_BASE_NAME_PATTERN, KNOWLEDGE_BASE_DESCRIPTION_MAX_LENGTH, KNOWLEDGE_BASE_TAG_MAX_LENGTH, KNOWLEDGE_BASE_TAG_MAX_COUNT } from "@/modules/knowledge/constants/validation";
import TagSelect from "@/modules/knowledge/components/TagSelect";
import * as api from "./api";
import "./index.scss";

interface ImportContext {
  inspections: api.InspectedSource[];
  inspectError: boolean;
  load: () => Promise<void>;
  items: api.ImportItem[];
  queryError: boolean;
  choose: (sources: api.InspectedSource[]) => void;
  refresh: () => Promise<void>;
  submittedBatch: string;
  opened: boolean;
  choosing: boolean;
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
  return axios.isAxiosError(error) ? error.response?.data?.message || error.message : error instanceof Error ? error.message : tr("operationFailed");
}

export function SourceImportProvider({ context, sources, children }: { context: api.SourceContext; sources?: ChatSourceCollection; children: ReactNode }) {
  const key = sourceContextKey(context);
  const stableContext = useMemo(() => context, [key]);
  const enabled = Boolean(context.workflow_session_id || context.task_id || (context.conversation_id && context.history_id));
  const [opened, setOpened] = useState(false);
  const [submittedBatch, setSubmittedBatch] = useState("");
  const [selection, setSelection] = useState<api.InspectedSource[] | null>(null);
  const { inspections, inspectError, items, queryError, refresh, inspect } = useSourceImportState(stableContext, enabled && opened);
  const sourceKey = JSON.stringify(sources ?? []);
  const load = useCallback(async () => {
    if (!enabled) return;
    await Promise.all([inspect(sourceKey), refresh()]);
  }, [enabled, sourceKey, inspect, refresh]);
  useEffect(() => { setSelection(null); setOpened(false); setSubmittedBatch(""); }, [key]);
  return <Context.Provider value={{ inspections, inspectError, load, items, queryError, choose: rows => setSelection(uniquePapers(rows)), refresh, submittedBatch, opened, choosing: selection !== null, setOpened }}>
    {children}
    <SourceImportResults />
    {selection && <TargetModal rows={selection} context={stableContext} onCancel={() => setSelection(null)} onSubmitted={batch => { setSelection(null); setSubmittedBatch(batch); setOpened(true); void refresh(); }} />}
  </Context.Provider>;
}

function TargetModal({ rows, context, onCancel, onSubmitted }: { rows: api.InspectedSource[]; context: api.SourceContext; onCancel: () => void; onSubmitted: (batch: string) => void }) {
  const { token } = theme.useToken();
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
      form.setFieldsValue({ ...knowledgeCreationDefaults(config, "academic_papers"), tags: [tr("paperTag")] });
    }).catch(e => { if (active) setError(errorText(e)); });
    return () => { active = false; };
  }, [form]);
  const submit = async () => {
    if (busy) return;
    try {
      const validated = await form.validateFields();
      const values = { ...form.getFieldsValue(true), ...validated };
      const target = mode === "existing"
        ? { target_dataset_id: values.target }
        : {
          new_knowledge_base: {
            ...knowledgeCreationMetadata(values.name, values.description, values.tags),
            scene: "academic_papers",
            processing_level: values.processing_level,
            algo_id: values.algo_id,
          },
        };
      const body = {
        source_context: context,
        source_ids: rows.map(row => row.source_id),
        ...target,
      };
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
  const levels: Record<string, string> = { stored: tr("originalOnly"), parsed: tr("parse"), chunked: tr("parseChunk"), indexed: tr("parseIndex") };
  return <Modal open zIndex={token.zIndexPopupBase + 200} title={tr("add")} okText={createdTarget ? tr("retryImport") : tr("confirmImport")} cancelText={tr("cancel")} confirmLoading={busy} onOk={() => void submit()} onCancel={busy ? undefined : onCancel} maskClosable={!busy} closable={!busy} cancelButtonProps={{ disabled: busy }}>
    <p>{tr("selectedPrefix")}{rows.length}{tr("papersSuffix")}</p>
    {error && <Alert type="error" showIcon message={error} />}
    {createdTarget && <Alert type="info" message={<span>{tr("createdWithoutTask")}<a href={`/lib/knowledge/detail/${encodeURIComponent(createdTarget)}`}>{tr("viewLibrary")}</a>。</span>} />}
    <Radio.Group value={mode} disabled={busy || Boolean(createdTarget)} onChange={(e: { target: { value: string } }) => setMode(e.target.value)} style={{ margin: "16px 0" }}><Radio.Button value="existing">{tr("existingLibrary")}</Radio.Button><Radio.Button value="new">{tr("newLibrary")}</Radio.Button></Radio.Group>
    <Form form={form} layout="vertical" disabled={busy || Boolean(createdTarget)}>
      {mode === "existing" ? <Form.Item name="target" label={tr("target")} rules={[{ required: true, message: tr("selectLibrary") }]}><Select showSearch optionFilterProp="label" placeholder={tr("selectEditableLibrary")} options={targets.map(row => ({ value: row.dataset_id, label: row.display_name }))} /></Form.Item> : <>
        <Form.Item name="name" label={tr("name")} rules={[{ required: true, message: tr("enterName") }, { pattern: KNOWLEDGE_BASE_NAME_PATTERN, message: tr("namePattern") }]}><Input maxLength={KNOWLEDGE_BASE_NAME_MAX_LENGTH} /></Form.Item>
        <Form.Item name="tags" label={tr("tags")} rules={[{ required: true, type: "array", min: 1, max: KNOWLEDGE_BASE_TAG_MAX_COUNT, message: tr("addTags") }]}><TagSelect tags={[]} maxTagLength={KNOWLEDGE_BASE_TAG_MAX_LENGTH} /></Form.Item>
        <Form.Item name="description" label={tr("description")}><Input.TextArea maxLength={KNOWLEDGE_BASE_DESCRIPTION_MAX_LENGTH} /></Form.Item>
        <Form.Item label={tr("scene")}><span>{tr("paperCollection")}</span></Form.Item>
        <Form.Item name="processing_level" label={tr("processing")} rules={[{ required: true }]}><Select options={options?.processing_levels.map(value => ({ value, label: levels[value] || value }))} /></Form.Item>
      </>}
    </Form>
    <small>{tr("backgroundNotice")}</small>
  </Modal>;
}

export function SourceImportActions({ source }: { source: ChatSource }) {
  const ctx = useSourceImports();
  useEffect(() => { void ctx?.load(); }, [ctx?.load]);
  const inspected = ctx && findInspectedSource(ctx.inspections, source);
  const matching = ctx?.items.filter(item => item.paper_key === inspected?.paper_key) || [];
  const latest = matching[matching.length - 1];
  return <>
    {latest && <small className="source-import-current-state">{latest.target_name} · {importStatusLabel(latest)}{latest.state_unavailable && tr("unavailableSuffix")}<Button type="link" size="small" onMouseDown={event => event.preventDefault()} onClick={() => ctx?.setOpened(true)}>{tr("history")}</Button></small>}
    <div className="source-import-actions"><Button onMouseDown={event => event.preventDefault()} onClick={() => openSource(source)}>{tr("openOriginal")}</Button>{inspected?.eligible && <Button type="primary" aria-label={tr("add")} icon={<PlusOutlined aria-hidden />} onMouseDown={event => event.preventDefault()} onClick={() => ctx?.choose([inspected])}>{tr("add")}</Button>}</div>
  </>;
}

export function ExecutionResults({ operations = [] }: { operations?: KnowledgeOperation[] }) {
  const [opened, setOpened] = useState(false);
  if (!operations.some(operation => operation.items.length > 0)) return null;
  return <Popover trigger="click" placement="topLeft" open={opened} onOpenChange={setOpened} destroyOnHidden overlayClassName="chat-sources-popover" content={<div className="execution-results-panel" role="dialog" aria-label={tr("results")}>
    <div className="chat-source-panel-header"><h2 className="chat-source-panel-title">{tr("results")}</h2><Button type="text" aria-label={tr("closeResults")} icon={<CloseOutlined />} onClick={() => setOpened(false)} /></div>
    <KnowledgeOperationCards operations={operations} />
  </div>}><Button className="tool-btn source-btn" icon={<DatabaseOutlined aria-hidden />} aria-label={tr("results")} aria-expanded={opened}>{tr("results")}</Button></Popover>;
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
    if (failures.length) Modal.error({ title: tr("importFailed"), content: <ul>{failures.map(row => <li key={row.item_id}>{row.title}：{row.error_message || tr("processingFailed")}</li>)}</ul> });
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
  return <Modal title={tr("paperHistory")} open={opened} onCancel={() => ctx?.setOpened(false)} footer={null} width={480} destroyOnHidden>
    {ctx?.queryError && <Alert type="warning" message={tr("keepLastStatus")} action={<Button size="small" onClick={() => void ctx.refresh()}>{tr("retryQuery")}</Button>} />}
    <KnowledgeOperationCards imports={items} onRetry={rows => void retry(rows)} retrying={retrying} onRefreshImports={ctx?.refresh} />
    {items.some(row => row.retryable) && <Space className="execution-results-footer"><Button loading={retrying} onClick={() => void retry(items)}>{tr("retryFailedOnly")}</Button><Button onClick={() => Modal.error({ title: tr("importFailed"), content: <ul>{items.filter(row => row.status === "failed" || row.processing_status === "failed").map(row => <li key={row.item_id}>{row.title}：{row.error_message || tr("processingFailed")}</li>)}</ul> })}>{tr("failureDetails")}</Button></Space>}
  </Modal>;
}
