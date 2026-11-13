import { useEffect, useState } from "react";
import { Alert, Button, Empty, Input, Pagination, Select, Spin, Tag } from "antd";
import { CheckCircleOutlined, ClockCircleOutlined, CloseCircleOutlined, FileTextOutlined, ReloadOutlined, RightOutlined } from "@ant-design/icons";
import type { ProcessingTasksQuery } from "./useProcessingTasks";
import "./ProcessingTaskPanel.scss";

const labels: Record<string, string> = { cleared: "待重新索引", pending: "等待处理", running: "处理中", succeeded: "已完成", failed: "失败", canceled: "已取消", unknown: "待确认", not_requested: "未请求", submitted: "已提交" };
const levels: Record<string, string> = { parsed: "解析", chunked: "解析与切片", indexed: "建立向量" };
const stages: Record<string, string> = { parse: "解析正文", chunk: "切片", index: "向量索引" };
const filters = [{ key: "all", label: "全部" }, { key: "active", label: "进行中" }, { key: "failed", label: "失败" }, { key: "succeeded", label: "已完成" }, { key: "canceled", label: "已取消" }, { key: "unknown", label: "待确认" }];
export default function ProcessingTaskPanel({ query }: { query: ProcessingTasksQuery }) {
  const { data, filters: selected, setFilters, loading, queryFailed, refresh } = query;
  const [expanded, setExpanded] = useState<string>();
  const [search, setSearch] = useState(selected.q);
  useEffect(() => { setSearch(selected.q); }, [selected.q]);
  const update = (patch: Partial<typeof selected>) => { setExpanded(undefined); setFilters(current => ({ ...current, ...patch, page: 1 })); };
  const activeCount = (data?.counts.active ?? 0);
  return <section className="processing-tasks" aria-label="文档处理任务">
    <div className="processing-tasks-intro"><div><strong>跟踪每份文档的处理结果</strong><p>每份文档显示最近一次处理。未请求的阶段不会进入队列。</p></div><Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>刷新</Button></div>
    {queryFailed && <Alert type="warning" showIcon message={data ? "状态暂时无法更新，以下为上次读取的结果。" : "暂时无法读取任务，请稍后重试。"} action={<Button size="small" onClick={refresh}>重试</Button>} />}
    <div className="processing-tasks-filters">
      <Input.Search aria-label="搜索文档或资料库" placeholder="搜索文档或资料库" value={search} onChange={event => setSearch(event.target.value)} onSearch={value => update({ q: value })} allowClear />
      <Select aria-label="处理类型" value={selected.level} onChange={level => update({ level })} options={[{ value: "", label: "全部处理类型" }, ...Object.entries(levels).map(([value, label]) => ({ value, label }))]} />
    </div>
    <div className="processing-tasks-status" role="tablist" aria-label="按任务状态筛选">
      {filters.map(({ key, label }) => <button type="button" key={key} role="tab" aria-selected={selected.status === key} onClick={() => update({ status: key })}>{label}<span>{data ? key === "all" ? Object.values(data.counts).reduce((a,b) => a+b,0) : key === "active" ? activeCount : data.counts[key] ?? 0 : "—"}</span></button>)}
    </div>
    <div className="processing-tasks-scroll" aria-busy={loading}>
      {loading && !data ? <div className="processing-tasks-empty"><Spin tip="正在读取处理任务" /></div> : !data?.items.length ? <div className="processing-tasks-empty"><Empty description={queryFailed ? "任务状态暂不可用" : selected.status !== "all" || selected.q || selected.level ? "没有符合条件的任务" : "暂无文档处理任务"} /><p>上传文档并开始解析、切片或建立向量后，可在这里查看状态。</p></div> : data.items.map(task => <article className={`processing-task ${task.status}`} key={task.id}>
        <button type="button" className="processing-task-summary" aria-expanded={expanded === task.id} aria-label={`${expanded === task.id ? "收起" : "展开"} ${task.document_name}`} onClick={() => setExpanded(expanded === task.id ? undefined : task.id)}>
          <span className="processing-task-icon"><FileTextOutlined /></span>
          <span className="processing-task-name"><strong>{task.document_name || "未命名文档"}</strong><small>{task.dataset_name} · {levels[task.level] || "文档处理"}</small></span>
          <span className="processing-task-time">{new Date(task.created_at).toLocaleString()}</span>
          <Tag icon={task.status === "succeeded" ? <CheckCircleOutlined /> : task.status === "failed" ? <CloseCircleOutlined /> : <ClockCircleOutlined />} color={task.status === "failed" ? "error" : task.status === "succeeded" ? "success" : ["running", "pending"].includes(task.status) ? "processing" : "default"}>{queryFailed ? "状态待更新" : labels[task.status] || "待确认"}</Tag>
          <RightOutlined className="processing-task-chevron" />
        </button>
        {task.error && <p className="processing-task-error">{task.error}</p>}
        {expanded === task.id && <div className="processing-task-details">
          <div className="processing-task-stages">{task.stages.map(stage => <div key={stage.name} className={stage.status}><span>{stages[stage.name] || stage.name}</span><strong>{labels[stage.status] || "待确认"}</strong></div>)}</div>
          {task.status === "failed" && task.stages.some(stage => stage.status === "unknown") && <p className="processing-task-hint">服务未返回具体失败阶段，请结合失败原因处理。</p>}
          {task.stages.some(stage => stage.status === "submitted") && <p className="processing-task-hint">任务已提交；服务尚未提供各阶段的实时进度。</p>}
          <div className="processing-task-actions"><small>任务 ID：{task.id}</small><div><Button href={`/lib/knowledge/detail/${encodeURIComponent(task.dataset_id)}`}>前往资料库处理</Button>{task.level === "indexed" && <Button href="/lib/knowledge/list?index_management=open">前往索引管理</Button>}</div></div>
        </div>}
      </article>)}
    </div>
    <div className="processing-tasks-pagination"><span>共 {data?.total ?? 0} 份文档</span><Pagination size="small" current={data?.page ?? selected.page} total={data?.total ?? 0} pageSize={20} showSizeChanger={false} disabled={loading} onChange={page => { setExpanded(undefined); setFilters(current => ({ ...current, page })); }} /></div>
  </section>;
}
