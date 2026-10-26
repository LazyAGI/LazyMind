import { useRAGComponent } from "../contexts/RAGComponentContext";
import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space, Tag } from "antd";
import { Link, useLocation } from "react-router-dom";
import { getPythonComponents, type PythonComponentStatus } from "../api/systemDependencies";
import {
  usePythonComponentTask, pythonComponentTaskBusy, runPythonComponentTask,
  cancelPythonComponentDownload, pythonComponentErrorText,
} from "../store/pythonComponentTask";

const names = { rag: "本地知识库（RAG）" };
const descriptions = { rag: "启用文档解析、知识库索引和检索。安装前仍可使用普通对话。" };

export default function PythonComponentDependencies() {
  const [items, setItems] = useState<PythonComponentStatus[]>([]);
  const [loadError, setLoadError] = useState("");
  const [selected, setSelected] = useState<PythonComponentStatus | null>(null);
  const task = usePythonComponentTask();
  const busy = pythonComponentTaskBusy(task.phase);
  const requestId = useRef(0);
  const location = useLocation();
  const refresh = useCallback(async () => {
    if (["restarting", "verifying"].includes(usePythonComponentTask.getState().phase)) return;
    const id = ++requestId.current;
    try {
      const next = await getPythonComponents();
      if (id === requestId.current) { setItems(next); setLoadError(""); }
    } catch (error) {
      if (id === requestId.current) setLoadError(pythonComponentErrorText(error));
    }
  }, []);
  useEffect(() => {
    void refresh();
    return () => { ++requestId.current; };
  }, [refresh, task.phase]);
  useEffect(() => {
    if (location.hash.startsWith("#python-")) document.getElementById(location.hash.slice(1))?.scrollIntoView();
  }, [location.hash, items]);
  const beginInstall = () => {
    if (!selected || busy || selected.installing) return;
    void runPythonComponentTask(selected.id);
    setSelected(null);
  };
  const progressLabel = task.phase === "installing" ? "后台安装中" : task.phase === "restarting" ? "正在重启服务" : "正在确认组件状态";
  return <>
    {loadError && !busy && <Alert type="error" showIcon message="无法读取可选组件状态" description={loadError}
      action={<Button onClick={() => void refresh()}>重试</Button>} />}
    {items.filter(item => item.installSupported).map(item => <section key={item.id}
      id={`python-${item.id}-dependency`} className="model-provider-service-category">
      <h3>{names[item.id]} <Tag color={item.active ? "green" : "default"}>
        {busy ? progressLabel : item.active ? "已启用" : item.restartRequired ? "等待启用" : "未安装"}
      </Tag></h3>
      <p>{descriptions[item.id]}</p>
      <Space wrap>
        <span>{item.installMethod === "source" ? "按本地源码依赖配置下载并安装" : `下载约 ${((item.sizeBytes || 0) / 1048576).toFixed(1)} MiB`}</span>
        {busy ? <Button type="primary" loading disabled>{progressLabel}</Button> : <>
          {!item.installed && <Button type="primary" disabled={item.installing} onClick={() => setSelected(item)}>
            {item.installing ? "安装中" : "安装组件"}
          </Button>}
          {item.restartRequired && (item.installMethod === "source"
            ? <span>依赖已安装，请执行 make local-down，再执行 make local-up LAZYMIND_RAG_ENABLED=false 启用。</span>
            : <Button onClick={() => void runPythonComponentTask(item.id, true)}>重试启用组件</Button>)}
        </>}
        {task.phase === "installing" && <Button onClick={cancelPythonComponentDownload}>取消下载</Button>}
        <Button disabled={busy} onClick={() => void refresh()}>刷新状态</Button>
      </Space>
      {busy && <p>正在后台处理，可以继续浏览其他页面。安装后会自动重启本地服务，确认启用后再通知你。</p>}
      {task.phase === "error" && <Alert type="error" showIcon message="组件未完成启用" description={task.error} />}
    </section>)}
    <Modal title={selected ? `安装${names[selected.id]}` : "安装组件"} open={!!selected}
      okText="下载并安装"
      okButtonProps={{ disabled: busy || selected?.installing || (selected?.installMethod !== "source" && !selected?.url?.startsWith("https://")) }}
      cancelText="取消" onOk={beginInstall} onCancel={() => setSelected(null)}>
      <p>{selected?.installMethod === "source" ? "将使用本地运行环境的依赖安装器，按当前源码配置下载 RAG 依赖。" : "将从以下来源下载此版本所需组件，并自动校验文件完整性。"}</p>
      <p>{selected?.installMethod === "source" ? "下载在后台进行，可以继续浏览。完成后需重新运行 make local-up 启用组件。" : "下载在后台进行，可以继续浏览。完成后会自动重启本地服务，重启期间正在运行的任务会中断。"}</p>
      <p style={{ overflowWrap: "anywhere" }}>{selected?.filename}</p>
      <p aria-label="组件下载来源" style={{ overflowWrap: "anywhere" }}>{selected?.installMethod === "source" ? "当前本地 Python 包源（LazyLLM RAG 与 requirements-local.txt）" : selected?.url || "下载来源暂不可用，请刷新后重试。"}</p>
    </Modal>
  </>;
}

export function RAGComponentNotice() {
  const { availability, refresh } = useRAGComponent();
  if (availability === "error") return <Alert type="warning" showIcon style={{ margin: 16 }}
    message="无法确认本地知识库组件状态" description="请重试确认组件状态。已有知识库及文件会保留。"
    action={<Button onClick={refresh}>重试</Button>} />;
  if (availability !== "missing") return null;
  return <Alert type="info" showIcon style={{ margin: 16 }} message="本地知识库组件尚未启用"
    description="未安装组件时仍可保存原文件，并对支持的格式提取文本、生成基础分块；不生成向量。图片文字需要 OCR。安装组件且 embedding 就绪后，目标为索引的文档会继续处理。"
    action={<Link to="/settings?section=system_tools#python-rag-dependency">安装组件</Link>} />;
}
