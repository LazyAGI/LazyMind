import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Empty, Modal, Select, Table, Tag, Tooltip, message } from "antd";
import { DatabaseOutlined, FileTextOutlined, ReloadOutlined, SettingOutlined, DeleteOutlined, SearchOutlined } from "@ant-design/icons";
import "./TextIndexManager.scss";
import { axiosInstance, BASE_URL } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import { useRAGComponent } from "@/modules/modelProvider/contexts/RAGComponentContext";
import { clearEmbeddingChunks } from "@/modules/modelProvider/api";

type IndexDocument = { id: string; name: string; parse_status: string; index_status: string; embedding_version: string; error: string };
type IndexState = { generation: number; model_name?: string; provider_name?: string; indexed_knowledge_bases: number; operation_phase: string; index_identity: string };
const statusNames: Record<string, string> = { succeeded: "已完成", pending: "未建立", running: "处理中", processing: "处理中", failed: "失败" };

export default function TextIndexManager({ datasetId }: { datasetId?: string }) {
  const { availability: ragAvailability, refresh: refreshRAG } = useRAGComponent();
  const [open, setOpen] = useState(false);
  const [dataset, setDataset] = useState(datasetId);
  const [libraries, setLibraries] = useState<{ label: string; value: string }[]>([]);
  const [state, setState] = useState<IndexState>();
  const [rows, setRows] = useState<IndexDocument[]>([]);
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [librariesLoading, setLibrariesLoading] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const revision = useRef(0);
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === "system-admin";
  const refresh = useCallback(async () => {
    const current = ++revision.current;
    setLoading(true);
    try {
      const [index, documents] = await Promise.all([
        axiosInstance.get(`${BASE_URL}/api/core/model_providers/embedding/index-state`),
        dataset ? axiosInstance.get(`${BASE_URL}/api/core/datasets/${encodeURIComponent(dataset)}/text-index`, { params: { page } }) : Promise.resolve(undefined),
      ]);
      if (current !== revision.current) return;
      setLoadError("");
      setState(index.data.data ?? index.data);
      setRows(documents?.data.documents ?? []);
      setTotal(documents?.data.total ?? 0);
    } catch {
      if (current === revision.current) setLoadError("索引状态读取失败，请刷新重试。");
    } finally {
      if (current === revision.current) setLoading(false);
    }
  }, [dataset, page]);
  useEffect(() => {
    if (!open) return;
    void refresh();
    const timer = window.setInterval(() => { if (!busy) void refresh(); }, 5000);
    return () => { window.clearInterval(timer); revision.current++; };
  }, [open, refresh, busy]);
  useEffect(() => { setDataset(datasetId); setSelected([]); setPage(1); }, [datasetId]);

  const show = async () => {
    setError("");
    setOpen(true);
    if (datasetId) return;
    setLibrariesLoading(true);
    try {
      const items: { label: string; value: string }[] = [];
      let token = "";
      do {
        const response = await axiosInstance.get(`${BASE_URL}/api/core/datasets`, { params: { page_size: 100, page_token: token } });
        for (const kb of response.data.datasets ?? []) items.push({ value: kb.dataset_id, label: kb.display_name });
        token = response.data.next_page_token || "";
      } while (token);
      setLibraries(items);
      if (!dataset && items.length) setDataset(items[0].value);
    } catch { setError("资料库列表读取失败，请关闭后重试。"); }
    finally { setLibrariesLoading(false); }
  };
  const reindex = async () => {
    if (!dataset || !selected.length) return;
    setBusy(true);
    setError("");
    try {
      const response = await axiosInstance.post(`${BASE_URL}/api/core/datasets/${encodeURIComponent(dataset)}/text-index`, { document_ids: selected });
      const failed = (response.data.tasks ?? []).filter((task: { status: string }) => task.status !== "STARTED");
      if (failed.length) setError(failed.map((task: { message: string }) => task.message).join("；"));
      else { setSelected([]); message.success("索引任务已提交；知识库的默认处理级别保持不变。"); await refresh(); }
    } catch { setError("索引未启动，请检查向量模型和 RAG 服务状态后重试。原文件不受影响。"); }
    finally { setBusy(false); }
  };
  const clear = () => Modal.confirm({
    title: "清除全部文本向量索引？",
    content: "影响所有知识库的文本向量检索。原文件、解析文本、切片及图像向量均保留。清除后可切换或移除旧模型，再按需选择文档重新索引。",
    okText: "清除文本向量", cancelText: "取消", okButtonProps: { danger: true },
    onOk: async () => {
      if (!state) return;
      setBusy(true);
      try {
        await clearEmbeddingChunks({ model_id: "", source: "own", generation: state.generation, confirm_clear_chunks: true });
        await refresh();
        message.success("文本向量已清除，原件和解析结果已保留。");
      } catch (err) { setError("清除未完成，请刷新状态后重试；运行中的索引任务需要先结束。"); throw err; }
      finally { setBusy(false); }
    },
  });
  const maintenance = ["clearing", "cleanup_failed"].includes(state?.operation_phase || "");
  const statusLabel = state?.operation_phase === "clearing" ? "正在清理"
    : state?.operation_phase === "cleanup_failed" ? "清理未完成"
      : state?.index_identity === "verified" ? "模型已确认" : state?.model_name ? "索引待确认" : "未配置模型";
  const statusTag = (value: string, row?: IndexDocument) => {
    const manual = row?.embedding_version === "reembed:manual" && !["running", "processing", "failed"].includes(value);
    return <Tag color={manual ? "default" : value === "succeeded" ? "success" : value === "failed" ? "error" : ["running", "processing"].includes(value) ? "processing" : "default"}>
      {manual ? "待重新索引" : statusNames[value] || "待核实"}
    </Tag>;
  };
  return <>
    <Button icon={<DatabaseOutlined />} onClick={() => void show()}>索引管理</Button>
    <Modal title="文本向量索引管理" open={open} width={960} className="text-index-manager" footer={isAdmin && <section className="text-index-cleanup" aria-label="全局索引清理">
        <div><strong>清理全部文本向量</strong><p>影响所有资料库的向量检索。原文件、解析正文、切片和图片向量保留。</p></div>
        <Button danger icon={<DeleteOutlined />} disabled={busy || !state || state.operation_phase === "clearing"} onClick={clear}>清理全部向量</Button>
      </section>}
      onCancel={() => { if (!busy) setOpen(false); }} destroyOnHidden>
      <p className="text-index-intro">管理用于语义检索的文档。上传和解析不依赖向量模型。</p>
      {state?.index_identity === "legacy_unknown" && <Alert className="text-index-error" type="warning" showIcon message="旧向量索引待处理" description="模型已移除或旧索引身份尚未确认。原文件、解析正文和旧向量保留；选择新模型后，按提示清理旧向量并重新索引。" />}
      {ragAvailability !== "ready" && <Alert type="info" showIcon
        message={ragAvailability === "missing" ? "向量索引需要本地知识库组件" : "正在确认知识库组件状态"}
        description="原文件上传、原生解析和阅读仍可使用。安装并启用组件后再建立向量索引。"
        action={ragAvailability === "missing" ? <a href="/settings?section=system_tools#python-rag-dependency">安装并启用组件</a> : <Button onClick={refreshRAG}>重新检查</Button>} />}
      {state && !state.model_name && <Alert type="info" showIcon message="尚未选择默认向量模型"
        description="清空索引后可以重新选择原模型，也可以选择其他模型。"
        action={<a href="/settings?section=model_defaults">配置向量模型</a>} />}
      <section className="text-index-overview" aria-label="当前向量模型">
        <div className="text-index-model">
          <span className="text-index-model-icon" aria-hidden><DatabaseOutlined /></span>
          <div>
            <span className="text-index-caption">当前向量模型</span>
            <strong>{state?.model_name || (loading ? "正在读取模型…" : "尚未配置向量模型")}</strong>
            <span className="text-index-model-meta">{state?.provider_name || "选择模型后即可建立向量索引"}{state && <> <Tag>{statusLabel}</Tag></>}</span>
          </div>
        </div>
        <div className="text-index-model-actions">
          <span><strong>{state?.indexed_knowledge_bases ?? "—"}</strong> 个资料库已索引</span>
          <a href="/settings?section=model_defaults"><SettingOutlined /> 切换默认模型</a>
        </div>
      </section>
      {(error || loadError) && <Alert className="text-index-error" type="error" showIcon message={error || loadError} />}
      <div className="text-index-documents">
        <div className="text-index-library-toolbar">
          {!datasetId ? <div className="text-index-library-select">
            <label htmlFor="text-index-library">资料库</label>
            <Select id="text-index-library" aria-label="选择知识库" placeholder="选择资料库" showSearch optionFilterProp="label"
              options={libraries} value={dataset} loading={librariesLoading} disabled={busy} suffixIcon={<SearchOutlined />}
              onChange={(value: string) => { setDataset(value); setPage(1); setSelected([]); setRows([]); setError(""); }} />
          </div> : <h3>文档索引</h3>}
          <Button icon={<ReloadOutlined spin={loading} />} onClick={() => void refresh()} disabled={busy || loading}>刷新状态</Button>
        </div>
        <div className="text-index-selection-bar">
          <span>{selected.length ? <>已选择 <strong>{selected.length}</strong> 份文档</> : "选择需要参与向量检索的文档"}<small>每次最多 20 份</small></span>
          <Button type="primary" loading={busy} disabled={!dataset || selected.length === 0 || maintenance || ragAvailability !== "ready" || !state?.model_name} onClick={() => void reindex()}>
            {selected.length ? `建立索引（${selected.length}）` : "建立索引"}
          </Button>
        </div>
        <Table<IndexDocument> rowKey="id" size="middle" dataSource={rows} loading={loading && rows.length === 0}
          scroll={{ x: 620 }} pagination={total ? { current: page, pageSize: 100, total, showSizeChanger: false,
            showTotal: (count: number) => `共 ${count} 份文档`, onChange: (value: number) => { setPage(value); setSelected([]); } } : false}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={!dataset ? "选择资料库，查看文档的解析与索引状态" : "这个资料库还没有可索引的文档"} /> }}
          rowSelection={{ selectedRowKeys: selected, onChange: (keys: React.Key[]) => setSelected(keys.slice(0, 20)),
            getCheckboxProps: (row: IndexDocument) => ({ disabled: busy || maintenance || ["running", "processing"].includes(row.index_status) || ["running", "processing"].includes(row.parse_status) || (selected.length >= 20 && !selected.includes(row.id)) }) }}
          columns={[
            { title: "文档", dataIndex: "name", width: "44%", render: (name: string, row: IndexDocument) => <div className="text-index-document-name"><FileTextOutlined /><span title={name || row.id}>{name || row.id}</span></div> },
            { title: "解析状态", dataIndex: "parse_status", width: 110, render: (value: string) => statusTag(value) },
            { title: "向量索引", dataIndex: "index_status", width: 130, render: (value: string, row: IndexDocument) => statusTag(value, row) },
            { title: "说明", dataIndex: "error", render: (value: string) => value ? <Tooltip title={value}><span className="text-index-document-error">{value}</span></Tooltip> : <span className="text-index-muted">—</span> },
          ]} />
      </div>

    </Modal>
  </>;
}
