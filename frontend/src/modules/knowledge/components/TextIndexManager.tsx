import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Select, Space, Table, Tag, message } from "antd";
import { axiosInstance, BASE_URL } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import { clearEmbeddingChunks } from "@/modules/modelProvider/api";

type IndexDocument = { id: string; name: string; parse_status: string; index_status: string; embedding_version: string; error: string };
type IndexState = { generation: number; model_name?: string; provider_name?: string; indexed_knowledge_bases: number; operation_phase: string; index_identity: string };
const statusNames: Record<string, string> = { succeeded: "已完成", pending: "未建立", running: "处理中", processing: "处理中", failed: "失败" };

export default function TextIndexManager({ datasetId }: { datasetId?: string }) {
  const [open, setOpen] = useState(false);
  const [dataset, setDataset] = useState(datasetId);
  const [libraries, setLibraries] = useState<{ label: string; value: string }[]>([]);
  const [state, setState] = useState<IndexState>();
  const [rows, setRows] = useState<IndexDocument[]>([]);
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const revision = useRef(0);
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === "system-admin";
  const refresh = useCallback(async () => {
    const current = ++revision.current;
    try {
      const [index, documents] = await Promise.all([
        axiosInstance.get(`${BASE_URL}/api/core/model_providers/embedding/index-state`),
        dataset ? axiosInstance.get(`${BASE_URL}/api/core/datasets/${encodeURIComponent(dataset)}/text-index`, { params: { page } }) : Promise.resolve(undefined),
      ]);
      if (current !== revision.current) return;
      setState(index.data.data ?? index.data);
      setRows(documents?.data.documents ?? []);
      setTotal(documents?.data.total ?? 0);
    } catch {
      if (current === revision.current) setError("索引状态读取失败，请刷新重试。");
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
    setOpen(true);
    if (datasetId) return;
    try {
      const items: { label: string; value: string }[] = [];
      let token = "";
      do {
        const response = await axiosInstance.get(`${BASE_URL}/api/core/datasets`, { params: { page_size: 100, page_token: token } });
        for (const kb of response.data.datasets ?? []) items.push({ value: kb.dataset_id, label: kb.display_name });
        token = response.data.next_page_token || "";
      } while (token);
      setLibraries(items);
    } catch { setError("知识库列表读取失败，请关闭后重试。"); }
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
  return <>
    <Button onClick={() => void show()}>索引管理</Button>
    <Modal title="文本向量索引管理" open={open} width={960} footer={null} onCancel={() => { if (!busy) setOpen(false); }} destroyOnHidden>
      <Space direction="vertical" style={{ width: "100%" }} size="middle">
        <Alert type="info" showIcon message={state?.model_name ? `当前模型：${state.provider_name || ""} · ${state.model_name}` : "尚未建立可用的文本向量索引"}
          description={`已索引知识库：${state?.indexed_knowledge_bases ?? "—"}。上传和解析无需向量模型；在此选择需要用于向量检索的文档。`} />
        {error && <Alert type="error" showIcon message={error} />}
        <Space wrap>
          {!datasetId && <Select aria-label="选择知识库" placeholder="选择知识库查看索引" showSearch optionFilterProp="label" options={libraries} value={dataset} style={{ width: 300 }}
            onChange={(value: string) => { setDataset(value); setPage(1); setSelected([]); setRows([]); }} />}
          <Button onClick={() => void refresh()} disabled={busy}>刷新状态</Button>
          <Button type="primary" disabled={busy || selected.length === 0 || selected.length > 20} onClick={() => void reindex()}>索引所选文档（{selected.length}/20）</Button>
          {isAdmin && <Button danger disabled={busy || !state || state.operation_phase === "clearing"} onClick={clear}>清除全部文本向量</Button>}
          <a href="/settings?section=model_defaults">切换默认向量模型</a>
        </Space>
        <Table<IndexDocument> rowKey="id" size="small" dataSource={rows} pagination={{ current: page, pageSize: 100, total, showSizeChanger: false, onChange: (value: number) => { setPage(value); setSelected([]); } }}
          rowSelection={{ selectedRowKeys: selected, onChange: setSelected, getCheckboxProps: (row: IndexDocument) => ({ disabled: busy || ["running", "processing"].includes(row.index_status) || ["running", "processing"].includes(row.parse_status) }) }}
          columns={[
            { title: "文档", dataIndex: "name", render: (name: string, row: IndexDocument) => name || row.id },
            { title: "解析", dataIndex: "parse_status", render: (value: string) => statusNames[value] || "待核实" },
            { title: "向量索引", dataIndex: "index_status", render: (value: string, row: IndexDocument) => <Tag color={value === "succeeded" ? "green" : value === "failed" ? "red" : "default"}>{row.embedding_version === "reembed:manual" && !["running", "processing", "failed"].includes(value) ? "已清除，待重新索引" : statusNames[value] || "待核实"}</Tag> },
            { title: "详情", dataIndex: "error" },
          ]} />
      </Space>
    </Modal>
  </>;
}
