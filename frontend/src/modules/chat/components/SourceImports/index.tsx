import { tr } from "./translations";
import { knowledgeCreationDefaults, knowledgeCreationMetadata } from "@/modules/knowledge/utils/creation";
import { sourceContextKey, useSourceImportState } from "./state";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { Alert, Button, Checkbox, Form, Input, Modal, Pagination, Progress, Radio, Select, Space, Tag, message, theme } from "antd";
import type { CheckboxChangeEvent } from "antd/es/checkbox";
import { PlusOutlined, SearchOutlined } from "@ant-design/icons";
import axios from "axios";
import { v4 as uuid } from "uuid";
import { type ChatSource, type ChatSourceCollection, getSourceOriginalHref, openSource } from "../../utils/sourceAdapter";
import ImportHistoryCards, { importStatusLabel } from "./ImportHistoryCards";
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
  const papers = new Map<string, api.InspectedSource>();
  for (const row of rows) {
    if (!row.eligible || !row.paper_key) continue;
    const previous = papers.get(row.paper_key);
    if (!previous || (previous.needs_pdf_lookup && !row.needs_pdf_lookup)) {
      papers.set(row.paper_key, row);
    }
  }
  return [...papers.values()];
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
  const { inspections, inspectError, items, queryError, refresh, inspect, recordFulltext } = useSourceImportState(stableContext, enabled && opened);
  const sourceKey = JSON.stringify(sources ?? []);
  const load = useCallback(async () => {
    if (!enabled) return;
    await Promise.all([inspect(sourceKey), refresh()]);
  }, [enabled, sourceKey, inspect, refresh]);
  useEffect(() => { setSelection(null); setOpened(false); setSubmittedBatch(""); }, [key]);
  return <Context.Provider value={{ inspections, inspectError, load, items, queryError, choose: rows => setSelection(uniquePapers(rows)), refresh, submittedBatch, opened, choosing: selection !== null, setOpened }}>
    {children}
    <SourceImportResults />
    {selection && <TargetModal rows={selection} context={stableContext} onFound={recordFulltext} onCancel={() => setSelection(null)} onSubmitted={batch => { setSelection(null); setSubmittedBatch(batch); setOpened(true); void refresh(); }} />}
  </Context.Provider>;
}

const fulltextPageSize = 4;
const lookupBatchSize = 4;

function TargetModal({ rows, context, onFound, onCancel, onSubmitted }: {
  rows: api.InspectedSource[]; context: api.SourceContext;
  onFound: (rows: api.InspectedSource[]) => void; onCancel: () => void; onSubmitted: (batch: string) => void;
}) {
  const { token } = theme.useToken();
  const [targets, setTargets] = useState<api.SourceTarget[]>([]);
  const [options, setOptions] = useState<KnowledgeOptions>();
  const [mode, setMode] = useState("existing");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [createdTarget, setCreatedTarget] = useState("");
  const ready = rows.filter(row => !row.needs_pdf_lookup);
  const candidates = rows.filter(row => row.needs_pdf_lookup);
  const [checked, setChecked] = useState(() => new Set(ready.map(row => row.source_id)));
  const [outcomes, setOutcomes] = useState<api.InspectedSource[]>([]);
  const [page, setPage] = useState(1);
  const [lookupProgress, setLookupProgress] = useState<{ done: number; total: number }>();
  const [lookupSummary, setLookupSummary] = useState<{ found: number; notFound: number; failed: number }>();
  const lookupRequest = useRef<AbortController>();
  const found = outcomes.filter(row => row.eligible && !row.needs_pdf_lookup);
  const fulltexts = [...found, ...ready];
  const selected = fulltexts.filter(row => checked.has(row.source_id));
  const pending = candidates.filter(row => !outcomes.some(outcome => outcome.source_id === row.source_id && !outcome.pdf_lookup_failed));
  const [form] = Form.useForm();
  const targetID = Form.useWatch("target", form);
  const targetName = Form.useWatch("name", form);
  const hasTarget = createdTarget || (mode === "existing" ? targetID : targetName?.trim());
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
  useEffect(() => () => lookupRequest.current?.abort(), []);
  const lookup = async () => {
    if (lookupRequest.current) return;
    const controller = new AbortController();
    lookupRequest.current = controller;
    setError("");
    setLookupProgress({ done: 0, total: pending.length });
    const summary = { found: 0, notFound: 0, failed: 0 };
    try {
      for (let i = 0; i < pending.length; i += lookupBatchSize) {
        const ids = pending.slice(i, i + lookupBatchSize).map(row => row.source_id);
        let results: api.InspectedSource[];
        try {
          results = await api.lookupSourcePDFs(context, ids, controller.signal);
        } catch (e) {
          summary.failed += ids.length;
          throw e;
        }
        if (controller.signal.aborted) return;
        const added = results.filter(row => row.eligible && !row.needs_pdf_lookup);
        const failed = results.filter(row => row.pdf_lookup_failed).length;
        summary.found += added.length;
        summary.failed += failed;
        summary.notFound += results.length - added.length - failed;
        onFound(added);
        setOutcomes(current => [...current.filter(row => !ids.includes(row.source_id)), ...results]);
        setChecked(current => new Set([...current, ...added.map(row => row.source_id)]));
        setLookupProgress({ done: i + ids.length, total: pending.length });
      }
    } catch (e) {
      if (!controller.signal.aborted) setError(errorText(e));
    } finally {
      lookupRequest.current = undefined;
      if (!controller.signal.aborted) {
        setPage(1);
        setLookupProgress(undefined);
        setLookupSummary(summary);
      }
    }
  };
  const submit = async () => {
    if (busy || lookupRequest.current || !selected.length) return;
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
        source_ids: selected.map(row => row.source_id),
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
  return <Modal open centered zIndex={token.zIndexPopupBase + 200} title={tr("add")}
    styles={{ body: { maxHeight: "calc(100dvh - 144px)", overflowY: "auto" } }}
    okText={createdTarget ? tr("retryImport") : tr("addCount", { count: selected.length })}
    cancelText={tr("cancel")} confirmLoading={busy}
    okButtonProps={{ disabled: !hasTarget || !selected.length || Boolean(lookupProgress) }}
    onOk={() => void submit()} onCancel={busy ? undefined : onCancel} maskClosable={!busy} closable={!busy}
    cancelButtonProps={{ disabled: busy }}>
    {error && <Alert type="error" showIcon message={error} />}
    {createdTarget && <Alert type="info" message={<span>{tr("createdWithoutTask")}<a href={`/lib/knowledge/detail/${encodeURIComponent(createdTarget)}`}>{tr("viewLibrary")}</a>。</span>} />}
    <div className="source-fulltext-summary" style={{ background: token.colorFillQuaternary, borderRadius: token.borderRadius }}>
      <div className="source-fulltext-heading">
        <strong>{tr("fulltextLookup")}</strong>
        <Button icon={<SearchOutlined aria-hidden />} loading={Boolean(lookupProgress)} disabled={!pending.length || busy || Boolean(createdTarget)} onClick={() => void lookup()}>
          {tr("lookupFulltext")}
        </Button>
      </div>
      <div role="status" style={{ color: token.colorTextSecondary }}>
        {lookupProgress
          ? tr("lookupProgress", lookupProgress)
          : lookupSummary
            ? tr("lookupResult", { found: lookupSummary.found })
            : tr(candidates.length ? "lookupSummary" : "lookupReady", { ready: ready.length, candidates: candidates.length })}
        {!lookupProgress && lookupSummary?.notFound ? <> {tr("lookupNotFound", { count: lookupSummary.notFound })}</> : null}
        {!lookupProgress && lookupSummary?.failed ? <> {tr("lookupFailedCount", { count: lookupSummary.failed })}</> : null}
      </div>
      {lookupProgress && <Progress percent={Math.round(lookupProgress.done / lookupProgress.total * 100)} size="small" showInfo={false} />}
    </div>
    <section className="source-fulltext-list" aria-label={tr("availableFulltext", { count: fulltexts.length })} style={{ borderColor: token.colorBorderSecondary, borderRadius: token.borderRadius }}>
      <div className="source-fulltext-heading">
        <strong>{tr("availableFulltext", { count: fulltexts.length })}</strong>
        <span role="status" style={{ color: token.colorTextSecondary }}>{tr("selectedFulltext", { count: selected.length })}</span>
      </div>
      <ul className="source-fulltext-rows" style={{ height: `calc(${fulltextPageSize} * var(--source-fulltext-row-height))` }}>
        {fulltexts.slice((page - 1) * fulltextPageSize, page * fulltextPageSize).map(row => {
          const title = row.title || row.url;
          return <li key={row.source_id} style={{ borderColor: token.colorBorderSecondary }}>
            <Checkbox checked={checked.has(row.source_id)} disabled={busy || Boolean(createdTarget)} onChange={(event: CheckboxChangeEvent) => {
              const isChecked = event.target.checked;
              setChecked(current => {
                const next = new Set(current);
                if (isChecked) next.add(row.source_id);
                else next.delete(row.source_id);
                return next;
              });
            }}><span className="source-fulltext-title" title={title}>{title}</span></Checkbox>
            {found.includes(row) && <Tag color="blue">{tr("newFulltext")}</Tag>}
          </li>;
        })}
      </ul>
      <div className="source-fulltext-pagination">
        {fulltexts.length > fulltextPageSize && <Pagination current={page} pageSize={fulltextPageSize} total={fulltexts.length} size="small" showSizeChanger={false} onChange={setPage} />}
      </div>
    </section>
    <Radio.Group value={mode} disabled={busy || Boolean(createdTarget)} onChange={(e: { target: { value: string } }) => setMode(e.target.value)} style={{ margin: "16px 0" }}><Radio.Button value="existing">{tr("existingLibrary")}</Radio.Button><Radio.Button value="new">{tr("newLibrary")}</Radio.Button></Radio.Group>
    <Form className="source-target-form" form={form} layout="vertical" disabled={busy || Boolean(createdTarget)}>
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
  const originalHref = getSourceOriginalHref(source);
  return <>
    {latest && <small className="source-import-current-state">{latest.target_name} · {importStatusLabel(latest)}{latest.state_unavailable && tr("unavailableSuffix")}<Button type="link" size="small" onMouseDown={(event: MouseEvent) => event.preventDefault()} onClick={() => ctx?.setOpened(true)}>{tr("history")}</Button></small>}
    {inspected?.needs_pdf_lookup && <small className="source-import-current-state">{tr("canLookupFulltext")}</small>}
    <div className="source-import-actions"><Button disabled={!originalHref} onMouseDown={(event: MouseEvent) => event.preventDefault()} onClick={() => openSource(source)}>{tr(originalHref ? "openOriginal" : "noOriginalLink")}</Button>{inspected?.eligible && <Button type="primary" aria-label={tr("add")} icon={<PlusOutlined aria-hidden />} onMouseDown={(event: MouseEvent) => event.preventDefault()} onClick={() => ctx?.choose([inspected])}>{tr("add")}</Button>}</div>
  </>;
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
    for (const row of items) if (["pending", "running", "waiting_retry"].includes(row.status)) observed.current.add(row.batch_id);
    const finished = [...observed.current].filter(batch => items.some(row => row.batch_id === batch) && !items.some(row => row.batch_id === batch && (["pending", "running", "waiting_retry", "unknown"].includes(row.status) || row.state_unavailable)));
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
    <ImportHistoryCards imports={items} onRetry={rows => void retry(rows)} retrying={retrying} onRefreshImports={ctx?.refresh} />
    {items.some(row => row.retryable) && <Space className="import-history-footer"><Button loading={retrying} onClick={() => void retry(items)}>{tr("retryFailedOnly")}</Button><Button onClick={() => Modal.error({ title: tr("importFailed"), content: <ul>{items.filter(row => row.status === "failed" || row.processing_status === "failed").map(row => <li key={row.item_id}>{row.title}：{row.error_message || tr("processingFailed")}</li>)}</ul> })}>{tr("failureDetails")}</Button></Space>}
  </Modal>;
}
