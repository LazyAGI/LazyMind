import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space, Tag, message } from "antd";
import { Link, useLocation } from "react-router-dom";
import { restartRuntime } from "@/runtime/desktopBridge";
import { getPythonComponents, installPythonComponent, type PythonComponentStatus } from "../api/systemDependencies";

const names = { rag: "本地知识库（RAG）" };
const descriptions = {
  rag: "启用文档解析、知识库索引和检索。安装前仍可使用普通对话。",
};

function errorText(error: unknown): string {
  const value = error as { response?: { data?: { message?: string; error?: string; data?: { detail?: string } } }; message?: string };
  return value.response?.data?.data?.detail || value.response?.data?.message || value.response?.data?.error || value.message || "组件操作失败";
}

export default function PythonComponentDependencies() {
  const [items, setItems] = useState<PythonComponentStatus[]>([]);
  const [loadError, setLoadError] = useState("");
  const [selected, setSelected] = useState<PythonComponentStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const abort = useRef<AbortController>();
  const location = useLocation();
  const refresh = useCallback(async () => {
    try { setItems(await getPythonComponents()); setLoadError(""); }
    catch (error) { setLoadError(errorText(error)); }
  }, []);
  useEffect(() => { void refresh(); return () => abort.current?.abort(); }, [refresh]);
  useEffect(() => {
    if (location.hash.startsWith("#python-")) document.getElementById(location.hash.slice(1))?.scrollIntoView();
  }, [location.hash, items]);

  const install = async () => {
    if (!selected || busy) return;
    const controller = new AbortController();
    abort.current = controller;
    setBusy(true);
    try {
      await installPythonComponent(selected.id, controller.signal);
      setSelected(null);
      message.success("组件安装完成，重启本地服务后启用。");
      await refresh();
    } catch (error) {
      if (!controller.signal.aborted) message.error(errorText(error));
    } finally { setBusy(false); abort.current = undefined; await refresh(); }
  };
  const restart = async () => {
    setRestarting(true);
    try {
      const result = await restartRuntime();
      if (!result.ok) throw new Error("请从桌面菜单重启本地服务，或退出并重新打开应用。");
      await refresh();
    } catch (error) { message.error(errorText(error)); }
    finally { setRestarting(false); }
  };
  return <>
    {loadError && <Alert type="error" showIcon message="无法读取可选组件状态" description={loadError}
      action={<Button onClick={() => void refresh()}>重试</Button>} />}
    {items.filter(item => item.installSupported).map(item => <section key={item.id}
      id={`python-${item.id}-dependency`} className="model-provider-service-category">
      <h3>{names[item.id]} <Tag color={item.active ? "green" : "default"}>
        {item.active ? "已启用" : item.restartRequired ? "等待重启" : "未安装"}
      </Tag></h3>
      <p>{descriptions[item.id]}</p>
      <Space wrap>
        <span>下载约 {((item.sizeBytes || 0) / 1048576).toFixed(1)} MiB</span>
        {!item.installed && <Button type="primary" disabled={busy || item.installing} onClick={() => {
          setSelected(item);
        }}>{item.installing ? "安装中" : "安装组件"}</Button>}
        {item.restartRequired && <Button onClick={() => void restart()} loading={restarting}>重启本地服务</Button>}
        <Button onClick={() => void refresh()}>刷新状态</Button>
      </Space>
      {item.restartRequired && <p>重启将中断正在进行的任务，请完成任务后再操作。也可退出并重新打开应用。</p>}
    </section>)}
    <Modal maskClosable={!busy} keyboard={!busy} closable={!busy} title={selected ? `安装${names[selected.id]}` : "安装组件"} open={!!selected}
      okText={busy ? "正在下载并校验" : "下载并安装"}
      okButtonProps={{ loading: busy, disabled: busy || !selected?.url?.startsWith("https://") }}
      cancelText={busy ? "取消下载" : "取消"} onOk={() => void install()}
      onCancel={() => { abort.current?.abort(); if (!busy) setSelected(null); }}>
      <p>将从以下来源下载此版本所需组件，并自动校验文件完整性。</p>
      <p style={{ overflowWrap: "anywhere" }}>{selected?.filename}</p>
      <p aria-label="组件下载来源" style={{ overflowWrap: "anywhere" }}>{selected?.url || "下载来源暂不可用，请刷新后重试。"}</p>
    </Modal>
  </>;
}

export function RAGComponentNotice() {
  const [missing, setMissing] = useState(false);
  useEffect(() => {
    let active = true;
    getPythonComponents().then(items => {
      if (active) setMissing(items.some(item => item.id === "rag" && !item.active));
    }).catch(() => {});
    return () => { active = false; };
  }, []);
  if (!missing) return null;
  return <Alert type="info" showIcon style={{ margin: 16 }} message="本地知识库组件尚未启用"
    description="安装并重启本地服务后，即可解析文档和检索知识库。已有知识库数据会保留。"
    action={<Link to="/settings?section=system_tools#python-rag-dependency">安装组件</Link>} />;
}
