import { Badge, Button, Empty, Input, List, message, Modal, Spin, Tag, Tooltip, Typography } from "antd";
import { CloudDownloadOutlined, DownloadOutlined, ExportOutlined, LoadingOutlined, SearchOutlined } from "@ant-design/icons";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  extractAcademicReferences,
  enqueueReferenceImport,
  listAcademicImportQueue,
  listAcademicReferences,
  previewReferenceImport,
  type AcademicReference,
  type AcademicImportQueueItem,
  type ImportPreviewItem,
} from "@/modules/knowledge/api/academicReferences";
import type { PdfReferenceAction } from "@/components/ui/RenderPdf";

const value = (item: AcademicReference, camel: keyof AcademicReference, snake: keyof AcademicReference) =>
  String(item[camel] || item[snake] || "");

interface Props {
  documentId: string;
  targetDatasetId: string;
  importSelection?: { text: string; referenceId?: string; requestId: number };
  onActionsChange?: (actions: PdfReferenceAction[]) => void;
  onReferenceCountChange?: (count: number) => void;
}

const normalized = (text: string) => text.toLowerCase().replace(/\s+/g, " ").trim();
const referenceUrl = (text: string) => {
  const index = text.search(/https?\s*:?\s*\/\s*\//i);
  if (index < 0) return "";
  return text.slice(index).split(/\s+Accessed:/i)[0].replace(/\s+/g, "").replace(/^https\/\//i, "https://").replace(/[.,;)]*$/, "");
};
const githubUrl = (text: string) => {
  const url = referenceUrl(text);
  try { return /(^|\.)github\.com$/i.test(new URL(url).hostname) ? url : ""; } catch { return ""; }
};

export default function DocumentReferencesPanel({ documentId, targetDatasetId, importSelection, onActionsChange, onReferenceCountChange }: Props) {
  const [references, setReferences] = useState<AcademicReference[]>([]);
  const [preview, setPreview] = useState<ImportPreviewItem[]>([]);
  const [queue, setQueue] = useState<AcademicImportQueueItem[]>([]);
  const [queueOpen, setQueueOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const autoExtractedFor = useRef("");
  const restoredPresenceFor = useRef("");
  const handledSelection = useRef(0);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const items = await listAcademicReferences(documentId);
      setReferences(items);
      onReferenceCountChange?.(items.length);
    }
    catch { setReferences([]); }
    finally { setLoading(false); }
  }, [documentId]);

  useEffect(() => { void load(); }, [load]);
  const ids = useMemo(() => references.map((item) => value(item, "ID", "id")).filter(Boolean), [references]);
  const filteredReferences = useMemo(() => {
    const keyword = normalized(query);
    if (!keyword) return references;
    return references.filter((item) => normalized([
      value(item, "RawText", "raw_text"),
      value(item, "DOINormalized", "doi_normalized"),
      value(item, "ArxivIDBase", "arxiv_id_base"),
    ].join(" ")).includes(keyword));
  }, [query, references]);
  const activeQueueCount = useMemo(() => queue.filter((item) => item.status === "pending" || item.status === "running").length, [queue]);
  const loadQueue = useCallback(async () => {
    try {
      setQueue(await listAcademicImportQueue(targetDatasetId, documentId));
      if (ids.length) setPreview(await previewReferenceImport(ids, targetDatasetId, false));
    } catch { /* best effort */ }
  }, [documentId, ids, targetDatasetId]);

  useEffect(() => {
    void loadQueue();
    const timer = window.setInterval(() => void loadQueue(), 2500);
    return () => window.clearInterval(timer);
  }, [loadQueue]);

  useEffect(() => {
    if (!ids.length || restoredPresenceFor.current === documentId) return;
    restoredPresenceFor.current = documentId;
    void previewReferenceImport(ids, targetDatasetId, false)
      .then(setPreview)
      .catch(() => { restoredPresenceFor.current = ""; });
  }, [documentId, ids, targetDatasetId]);

  const extract = async (quiet = false) => {
    setLoading(true);
    try {
      const items = await extractAcademicReferences(documentId, true);
      setReferences(items); setPreview([]);
      onReferenceCountChange?.(items.length);
      if (!quiet) message.success(`识别到 ${items.length} 条参考文献`);
    } catch (error) {
      const detail = error instanceof Error ? error.message : "参考文献提取失败";
      if (/parsing|root nodes|解析/.test(detail)) {
        window.setTimeout(() => {
          autoExtractedFor.current = "";
          void load();
        }, 3000);
      } else if (!quiet) {
        message.error(detail);
      }
    }
    finally { setLoading(false); }
  };

  useEffect(() => {
    if (loading || references.length || autoExtractedFor.current === documentId) return;
    autoExtractedFor.current = documentId;
    void extract(true);
  }, [documentId, loading, references.length, load]);

  useEffect(() => {
    if (!importSelection || handledSelection.current === importSelection.requestId || !references.length) return;
    handledSelection.current = importSelection.requestId;
    const selection = normalized(importSelection.text);
    const matched = importSelection.referenceId
      ? references.filter((item) => value(item, "ID", "id") === importSelection.referenceId)
      : references.filter((item) => {
      const raw = normalized(value(item, "RawText", "raw_text"));
      const arxiv = value(item, "ArxivIDBase", "arxiv_id_base").toLowerCase();
      const doi = value(item, "DOINormalized", "doi_normalized").toLowerCase();
      return raw.includes(selection) || selection.includes(raw) || (arxiv && selection.includes(arxiv)) || (doi && selection.includes(doi));
      });
    if (!matched.length) {
      message.warning("选中文字未匹配到已识别的参考文献，请扩大选区后重试");
      return;
    }
    const matchedIds = matched.map((item) => value(item, "ID", "id")).filter(Boolean);
    void (async () => {
      try {
        const task = await enqueueReferenceImport(targetDatasetId, [documentId], matchedIds);
        message.success(`已加入下载队列（${task.total_items} 篇）`);
        await loadQueue();
      } catch (error) {
        message.error(error instanceof Error ? error.message : "论文导入失败");
      }
    })();
  }, [importSelection, references, targetDatasetId, load, loadQueue]);

  const previewByReference = useMemo(() => {
    const map = new Map<string, ImportPreviewItem>();
    preview.forEach((item) => (item.reference_ids || []).forEach((id) => map.set(id, item)));
    return map;
  }, [preview]);

  const downloadableIds = useMemo(() => ids.filter((id) => {
    const reference = references.find((entry) => value(entry, "ID", "id") === id);
    if (reference && githubUrl(value(reference, "RawText", "raw_text"))) return false;
    const item = previewByReference.get(id);
    return !item?.presence.importing && !item?.presence.current_dataset?.length && !item?.presence.other_datasets?.length;
  }), [ids, previewByReference, references]);

  const referenceActions = useMemo<PdfReferenceAction[]>(() => references.flatMap((item) => {
    const id = value(item, "ID", "id");
    const result = previewByReference.get(id);
    const existing = result?.presence.current_dataset?.[0] || result?.presence.other_datasets?.[0];
    const external = githubUrl(value(item, "RawText", "raw_text"));
    const page = Number(item.Page ?? item.page ?? 0);
    const rawBBox = item.BBoxJSON ?? item.bbox_json ?? [];
    if (result?.presence.importing) return [];
    const regions = Array.isArray(rawBBox)
      ? (rawBBox.length === 4 ? [{ page, bbox: rawBBox }] : [])
      : (rawBBox.regions || []);
    const base = {
      referenceId: id,
      referenceKey: value(item, "ReferenceKey", "reference_key"),
      rawText: value(item, "RawText", "raw_text"),
      kind: existing ? "open" : external ? "external" : "download",
      href: existing ? `/lib/knowledge/knowledge/${encodeURIComponent(existing.dataset_id)}/${encodeURIComponent(existing.document_id)}` : external || undefined,
    } as const;
    if (!regions.length) return [{ ...base, page: page > 0 ? page : undefined }];
    return regions.flatMap((region) => region.bbox.length === 4 ? [{
      ...base,
      page: Number(region.page),
      bbox: region.bbox.map(Number) as [number, number, number, number],
    }] : []);
  }), [previewByReference, references]);

  useEffect(() => { onActionsChange?.(referenceActions); }, [onActionsChange, referenceActions]);

  const runImport = async (referenceIds: string[]) => {
    if (!referenceIds.length) return;
    try {
      const result = await enqueueReferenceImport(targetDatasetId, [documentId], referenceIds);
      message.success(`已加入下载队列（${result.total_items} 篇）`);
      await loadQueue();
    } catch (error) { message.error(error instanceof Error ? error.message : "创建导入任务失败"); }
  };

  return <div style={{ height: "100%", overflow: "auto", padding: "0 12px 12px" }}>
    <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
      <Input
        allowClear
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        prefix={<SearchOutlined style={{ color: "rgba(0,0,0,.35)" }} />}
        placeholder="搜索参考文献"
        style={{ flex: 1, minWidth: 0, borderRadius: 8 }}
      />
      <Tooltip title={activeQueueCount ? `${activeQueueCount} 个任务下载中` : "下载队列"}>
        <Badge dot={activeQueueCount > 0} offset={[-4, 4]}>
          <Button
            type="text"
            shape="circle"
            aria-label="下载队列"
            icon={activeQueueCount ? <LoadingOutlined spin /> : <CloudDownloadOutlined />}
            onClick={() => setQueueOpen(true)}
          />
        </Badge>
      </Tooltip>
      <Button icon={<DownloadOutlined />} disabled={!downloadableIds.length} onClick={() => void runImport(downloadableIds)}>
        全部下载
      </Button>
    </div>
    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 10, color: "rgba(0,0,0,.45)", fontSize: 13 }}>
      <span>共 {references.length} 条参考文献</span>
      {query ? <span>找到 {filteredReferences.length} 条</span> : null}
    </div>
    <Modal title="下载队列" open={queueOpen} onCancel={() => setQueueOpen(false)} footer={null} width={680}>
      <List
        size="small"
        locale={{ emptyText: "暂无下载任务" }}
        dataSource={queue}
        style={{ maxHeight: "60vh", overflow: "auto" }}
        renderItem={(item) => <List.Item>
          <div style={{ minWidth: 0, maxWidth: "78%" }}>
            <Tooltip title={item.title} mouseEnterDelay={0.5}>
              <Typography.Text ellipsis style={{ display: "block" }}>{item.title}</Typography.Text>
            </Tooltip>
            {item.error_message ? <Typography.Text type="danger" ellipsis style={{ display: "block", fontSize: 12 }}>{item.error_message}</Typography.Text> : null}
          </div>
          <Tag color={item.status === "succeeded" ? "green" : item.status === "failed" ? "red" : item.status === "running" ? "processing" : "default"}>
            {item.status === "pending" ? "等待下载" : item.status === "running" ? "下载中" : item.status === "succeeded" ? "已完成" : item.status === "skipped" ? "已跳过" : item.status === "failed" ? "失败" : item.status}
          </Tag>
        </List.Item>}
      />
    </Modal>
    {loading && !references.length ? <Spin /> : references.length ? <>
      <List split={false} dataSource={filteredReferences} renderItem={(item, index) => {
        const id = value(item, "ID", "id"); const result = previewByReference.get(id);
        const existing = result?.presence.current_dataset?.[0] || result?.presence.other_datasets?.[0];
        const rawText = value(item, "RawText", "raw_text");
        const external = githubUrl(rawText);
        const href = existing ? `/lib/knowledge/knowledge/${encodeURIComponent(existing.dataset_id)}/${encodeURIComponent(existing.document_id)}` : undefined;
        const action = result?.presence.importing
            ? <Button key="queued" type="link" size="small" disabled>队列中</Button>
            : existing
              ? null
              : external
                ? <Tooltip title="打开 GitHub"><Button key="external" type="text" size="small" aria-label="打开链接" icon={<ExportOutlined />} href={external} target="_blank" rel="noreferrer" /></Tooltip>
              : <Tooltip title="下载并加入知识库"><Button key="download" type="text" size="small" aria-label="下载" icon={<DownloadOutlined />} onClick={() => void runImport([id])} /></Tooltip>;
        return <List.Item style={{ padding: "10px 12px", marginBottom: 8, border: "1px solid #edf0f5", borderRadius: 8, background: "#fff" }}>
          <div style={{ display: "grid", gridTemplateColumns: "28px minmax(0, 1fr) auto", alignItems: "center", gap: 8, minWidth: 0, width: "100%" }}>
            <Typography.Text type="secondary" style={{ textAlign: "center", fontSize: 13 }}>{index + 1}</Typography.Text>
            <Tooltip title={<div style={{ maxWidth: 560 }}>{rawText}</div>} mouseEnterDelay={0.5} placement="topLeft">
              {href || external
                ? <a href={href || external} target={external && !href ? "_blank" : undefined} rel={external && !href ? "noreferrer" : undefined} style={{ display: "block", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: "#1677ff" }}>{rawText}</a>
                : <Typography.Text ellipsis style={{ minWidth: 0 }}>{rawText}</Typography.Text>}
            </Tooltip>
            {action}
          </div>
        </List.Item>;
      }} />
    </> : <Empty description={loading ? "正在准备 parsed root nodes" : "尚未识别到参考文献"} />}
  </div>;
}
