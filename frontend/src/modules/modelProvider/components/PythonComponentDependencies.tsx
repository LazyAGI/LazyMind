import { useRAGComponent } from "../contexts/RAGComponentContext";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Button, Modal, Space, Tag } from "antd";
import { ApartmentOutlined, DatabaseOutlined } from "@ant-design/icons";
import { Link, useLocation } from "react-router-dom";
import { getPythonComponents, type PythonComponentStatus } from "../api/systemDependencies";
import {
  usePythonComponentTask, pythonComponentTaskBusy, runPythonComponentTask,
  cancelPythonComponentDownload, pythonComponentErrorText,
} from "../store/pythonComponentTask";

function componentLabel(id: PythonComponentStatus["id"]) {
  return id === "cluster" ? "clusterComponent" : "ragComponent";
}

export default function PythonComponentDependencies() {
  const { t } = useTranslation();
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
  const progressLabel = task.phase === "installing" ? t("ragComponent.installingBackground") : task.phase === "restarting" ? t("ragComponent.restarting") : t("ragComponent.verifying");
  return <>
    {loadError && !busy && <Alert type="error" showIcon message={t("ragComponent.loadFailed")} description={loadError}
      action={<Button onClick={() => void refresh()}>{t("common.retry")}</Button>} />}
    {items.filter(item => item.installSupported).map(item => {
      const label = componentLabel(item.id);
      const inProgress = busy && task.id === item.id;
      return <section key={item.id}
          id={`python-${item.id}-dependency`} className="model-provider-service-category model-provider-python-component">
          <div className="model-provider-python-component-row">
            <div className="model-provider-service-category-head">
              <span aria-hidden="true">{item.id === "cluster" ? <ApartmentOutlined /> : <DatabaseOutlined />}</span>
              <div className="model-provider-python-component-copy">
                <div className="model-provider-python-component-title">
                  <h3>{t(`${label}.name`)}</h3>
                  <Tag color={item.active ? "success" : "default"}>
                    {inProgress ? progressLabel : item.active ? t("common.enabled") : item.restartRequired ? t("ragComponent.pendingRestart") : t("ragComponent.notInstalled")}
                  </Tag>
                </div>
                <p>{t(`${label}.description`)}</p>
                <span className="model-provider-python-component-meta">{item.installMethod === "source" ? t("ragComponent.sourceInstall") : t("ragComponent.downloadSize", { size: ((item.sizeBytes || 0) / 1048576).toFixed(1) })}</span>
              </div>
            </div>
            <Space className="model-provider-python-component-actions" wrap>
              {inProgress ? <Button type="primary" loading disabled>{progressLabel}</Button> : <>
                {!item.installed && <Button type="primary" disabled={busy || item.installing} onClick={() => setSelected(item)}>
                  {item.installing ? t("ragComponent.installing") : t("ragComponent.install")}
                </Button>}
                {item.restartRequired && item.installMethod !== "source" && <Button disabled={busy} onClick={() => void runPythonComponentTask(item.id, true)}>{t("ragComponent.retryActivation")}</Button>}
              </>}
              {inProgress && task.phase === "installing" && <Button onClick={cancelPythonComponentDownload}>{t("ragComponent.cancelDownload")}</Button>}
            </Space>
          </div>
          {!busy && item.restartRequired && item.installMethod === "source" && <p className="model-provider-python-component-notice">{t("ragComponent.sourceRestart")}</p>}
          {inProgress && <p className="model-provider-python-component-notice">{t("ragComponent.backgroundNotice")}</p>}
          {task.id === item.id && task.phase === "error" && <Alert type="error" showIcon message={t("ragComponent.activationFailed")} description={task.error} />}
        </section>;
    })}
    <Modal title={selected ? t("ragComponent.installTitle", { name: t(`${componentLabel(selected.id)}.name`) }) : t("ragComponent.install")} open={!!selected}
      okText={t("ragComponent.downloadAndInstall")}
      okButtonProps={{ disabled: busy || selected?.installing || (selected?.installMethod !== "source" && !selected?.url) }}
      cancelText={t("common.cancel")} onOk={beginInstall} onCancel={() => setSelected(null)}>
      <p>{selected?.installMethod === "source" ? t("ragComponent.sourceInstaller") : t("ragComponent.packageInstaller")}</p>
      <p>{selected?.installMethod === "source" ? t("ragComponent.sourceRestartNotice") : t("ragComponent.packageRestartNotice")}</p>
      <p style={{ overflowWrap: "anywhere" }}>{selected?.filename}</p>
      <p aria-label={t("ragComponent.downloadSource")} style={{ overflowWrap: "anywhere" }}>{selected?.installMethod === "source" ? t("ragComponent.localSource") : selected?.url || t("ragComponent.sourceUnavailable")}</p>
    </Modal>
  </>;
}

export function RAGComponentNotice() {
  const { t } = useTranslation();
  const { availability, refresh } = useRAGComponent();
  if (availability === "error") return <Alert type="warning" showIcon style={{ margin: 16 }}
    message={t("ragComponent.statusUnknown")} description={t("ragComponent.retryStatus")}
    action={<Button onClick={refresh}>{t("common.retry")}</Button>} />;
  if (availability !== "missing") return null;
  return <Alert type="info" showIcon style={{ margin: 16 }} message={t("ragComponent.notEnabled")}
    description={t("ragComponent.limitedParsing")}
    action={<Link to="/settings?section=system_tools#python-rag-dependency">{t("ragComponent.install")}</Link>} />;
}
