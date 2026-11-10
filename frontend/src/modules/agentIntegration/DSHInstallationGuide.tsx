import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space, Spin, Steps, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { agentIntegrationStatuses, type DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";
import { requiresAgentQuit } from "./agentActions";
import { dshConnectionReady, dshErrorMessage } from "./dshConnection";

type Stage = "prepare" | "installing" | "quitRequired" | "installFailed" | "enable" | "checking" | "notReady" | "success" | "disconnectRequired";
const imageURL = "/installation-guides/deepseek-harness/enable.png";

export default function DSHInstallationGuide({ status, onConnect, onStatus, onClose }: {
  status?: DesktopAgentIntegrationStatus;
  onConnect: () => Promise<DesktopAgentIntegrationStatus | undefined>;
  onStatus: (status: DesktopAgentIntegrationStatus) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const text = (key: string) => t(`agentIntegration.dshInstallGuide.${key}`);
  const [stage, setStage] = useState<Stage>(status?.retry_action === "disconnect" ? "disconnectRequired" : requiresAgentQuit(status) ? "quitRequired"
    : status?.state === "enabled" && (status.connection?.mcp_configured ?? true) ? "enable" : "prepare");
  const [zoomed, setZoomed] = useState(false);
  const [error, setError] = useState("");
  const [diagnostic, setDiagnostic] = useState(status?.issue?.operation_id || status?.diagnostic_id || "");
  const mounted = useRef(true);
  const busy = useRef(false);
  const requestVersion = useRef(0);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; requestVersion.current += 1; };
  }, []);

  const install = async () => {
    if (busy.current) return;
    busy.current = true;
    const request = ++requestVersion.current;
    setStage("installing");
    setError("");
    setDiagnostic("");
    try {
      const result = await onConnect();
      if (!mounted.current || request !== requestVersion.current) return;
      setDiagnostic(result?.issue?.operation_id || result?.diagnostic_id || "");
      if (result?.issue && !requiresAgentQuit(result)) setError(dshErrorMessage(result, t));
      setStage(requiresAgentQuit(result) ? "quitRequired"
        : result?.state === "enabled" && (result.connection?.mcp_configured ?? true) ? "enable" : "installFailed");
    } catch {
      if (mounted.current && request === requestVersion.current) setStage("installFailed");
    } finally {
      if (request === requestVersion.current) busy.current = false;
    }
  };

  const check = async (automatic = false) => {
    if (busy.current) return;
    busy.current = true;
    const request = ++requestVersion.current;
    if (!automatic) { setStage("checking"); setError(""); }
    try {
      const result = await agentIntegrationStatuses();
      if (!mounted.current || request !== requestVersion.current) return;
      const current = result.ok ? result.data["deepseek-harness"] : undefined;
      if (current) onStatus(current);
      if (current?.retry_action === "disconnect") { setStage("disconnectRequired"); return; }
      const ready = dshConnectionReady(current);
      // A normal startup may deliver its first heartbeat after the user returns.
      // Background checks never install, restart, or conceal an explicit failure.
      if (automatic && !ready) return;
      setDiagnostic(current?.issue?.operation_id || current?.diagnostic_id || "");
      if (!ready) setError(!current || !current.connection ? text("checkFailedDescription")
        : current.connection?.runtime_state === "waiting" ? text("waitingDescription")
          : current.issue ? dshErrorMessage(current, t)
            : !current.connection?.mcp_configured ? text("configurationMissingDescription")
              : text("notReadyDescription"));
      setStage(ready ? "success" : "notReady");
    } catch {
      if (mounted.current && request === requestVersion.current && !automatic) { setError(text("checkFailedDescription")); setStage("notReady"); }
    } finally { if (request === requestVersion.current) busy.current = false; }
  };

  const reconnect = () => {
    requestVersion.current += 1;
    busy.current = false;
    setStage("prepare");
    setError("");
    setDiagnostic("");
  };

  useEffect(() => {
    if (!["installFailed", "enable", "notReady"].includes(stage)) return;
    const recheck = () => { if (document.visibilityState === "visible") void check(true); };
    const timer = window.setInterval(recheck, 3000);
    window.addEventListener("focus", recheck);
    return () => { window.clearInterval(timer); window.removeEventListener("focus", recheck); };
  }, [stage]);

  const installing = stage === "installing";
  const checking = stage === "checking";
  const current = ["prepare", "quitRequired"].includes(stage) ? 0 : ["enable", "checking", "notReady", "success"].includes(stage) ? 2 : 1;
  const showImage = stage === "enable" || stage === "notReady";
  return <>
    <Modal open centered width={720} className="agent-integration-installation-guide agent-integration-dsh-guide"
      title={text("title")} onCancel={onClose} closable={!installing && !checking}
      maskClosable={false} keyboard={!installing && !checking}
      footer={<div className="agent-integration-installation-footer">
        <Button onClick={onClose} disabled={installing || checking}>{text(stage === "prepare" ? "cancel" : "close")}</Button>
        <Space wrap key={stage}>
        {stage === "notReady" && <Button onClick={reconnect}>{text("reconnect")}</Button>}
        {stage === "disconnectRequired" ? null : stage === "success" ? <Button type="primary" onClick={onClose}>{text("done")}</Button>
          : ["enable", "notReady", "checking"].includes(stage)
          ? <Button type="primary" loading={checking} disabled={checking} onClick={() => void check()}>
            {text(checking ? "checking" : stage === "notReady" ? "retryCheck" : "check")}
          </Button>
          : <Button key={stage} type="primary" loading={installing} disabled={installing} onClick={() => void install()}>
            {text(installing ? "installing" : stage === "prepare" ? "continue" : "retryInstall")}
          </Button>}
        </Space>
      </div>}>
      <Steps size="small" current={current} items={["prepareStep", "installStep", "enableStep"].map(key => ({ title: text(key) }))} />
      <div className="agent-integration-installation-content" aria-live="polite">
        {stage === "prepare" && <Typography.Paragraph>{text("prepareDescription")}</Typography.Paragraph>}
        {stage === "disconnectRequired" && <Alert type="warning" showIcon message={text("disconnectPendingTitle")}
          description={text("disconnectPendingDescription")} />}
        {(installing || checking) && <Space direction="vertical" align="center" className="agent-integration-installation-progress">
          <Spin /><Typography.Text>{text(installing ? "installingDescription" : "checkingDescription")}</Typography.Text>
        </Space>}
        {stage === "quitRequired" && <Alert type="warning" showIcon
          message={t("agentIntegration.quitRequired.title", { agent: "DeepSeek Harness" })}
          description={t("agentIntegration.quitRequired.description", { agent: "DeepSeek Harness" })} />}
        {stage === "installFailed" && <Alert type="error" showIcon
          message={text("installFailedTitle")} description={error || text("installFailedDescription")} />}
        {["quitRequired", "installFailed", "notReady"].includes(stage) && diagnostic && <Typography.Text type="secondary" copyable>
          {t("agentIntegration.setupFeedback.diagnosticID")}: {diagnostic}
        </Typography.Text>}
        {stage === "enable" && <Alert type="success" showIcon
          message={text("installedTitle")} description={text("installedDescription")} />}
        {stage === "notReady" && <Alert type="warning" showIcon message={text("notReadyTitle")} description={error} />}
        {stage === "success" && <Alert type="success" showIcon message={text("successTitle")} description={text("successDescription")} />}
        {showImage && <>
          <button type="button" className="agent-integration-installation-image" aria-label={text("enlargeImage")} onClick={() => setZoomed(true)}>
            <img src={imageURL} alt={text("imageAlt")} />
          </button>
          <Typography.Paragraph type="secondary" className="agent-integration-installation-note">{text("imageNote")}</Typography.Paragraph>
        </>}
      </div>
    </Modal>
    {showImage && <Modal open={zoomed} title={text("enableStep")} footer={null} width={1100} onCancel={() => setZoomed(false)}>
      <img src={imageURL} alt={text("imageAlt")} style={{ width: "100%" }} />
    </Modal>}
  </>;
}
