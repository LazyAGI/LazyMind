import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space, Spin, Steps, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { agentIntegrationStatuses, type DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

import { workbuddyErrorMessage } from "./workbuddyErrors";

type Stage = "prepare" | "installing" | "installFailed" | "entry" | "trust" | "enable" | "verify" | "checking" | "notReady" | "success";

export default function WorkBuddyInstallationGuide({ status, onConnect, onStatus, onClose, agent = "workbuddy" }: {
  agent?: "workbuddy" | "codex";
  status?: DesktopAgentIntegrationStatus;
  onConnect: () => Promise<DesktopAgentIntegrationStatus | undefined>;
  onStatus: (status: DesktopAgentIntegrationStatus) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const text = (key: string) => t(`agentIntegration.${agent === "codex" ? "codexInstallGuide" : "workbuddyInstallGuide"}.${key}`);
  const confirmationStage = agent === "codex" ? "enable" : "entry";
  const [stage, setStage] = useState<Stage>(status?.state === "enabled" && status.connection?.mcp_configured && (agent !== "codex" || status.connection.control_ready && !status.issue) ? confirmationStage : "prepare");
  const [error, setError] = useState("");
  const [zoomed, setZoomed] = useState(false);
  const [controlUnavailable, setControlUnavailable] = useState(false);
  const [controlError, setControlError] = useState("");
  const mounted = useRef(true);
  const busy = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const install = async () => {
    if (busy.current) return;
    busy.current = true;
    setStage("installing"); setError("");
    try {
      const result = await onConnect();
      if (!mounted.current) return;
      if (result?.state !== "enabled" || !result.connection?.mcp_configured) {
        setError(result?.issue ? (agent === "workbuddy" ? workbuddyErrorMessage(result, t) : text("installFailedDescription")) : result?.message || text("installFailedDescription"));
        setStage("installFailed"); return;
      }
      // Connect includes restart and installation checks. Trust is a separate step.
      setStage(confirmationStage);
    } catch {
      if (mounted.current) { setError(text("installFailedDescription")); setStage("installFailed"); }
    } finally { busy.current = false; }
  };

  const check = async () => {
    if (busy.current) return;
    busy.current = true;
    setStage("checking"); setError("");
    try {
      const result = await agentIntegrationStatuses();
      if (!mounted.current) return;
      if (!result.ok) { setError(text("checkFailedDescription")); setStage("notReady"); return; }
      const current = result.data[agent];
      if (current) onStatus(current);
      const connection = current?.connection;
      const configured = current?.state === "enabled" && connection?.mcp_configured;
      const ready = configured && connection?.mcp_enabled && (agent === "codex" ? connection.control_ready && !current?.issue : connection.mcp_trusted);
      setControlUnavailable(Boolean(ready && (!connection?.control_ready || current?.issue)));
      setControlError(current?.issue ? (agent === "codex" ? text("controlUnavailableDescription") : workbuddyErrorMessage(current, t)) : "");
      if (!ready && agent === "codex") setError(text("controlUnavailableDescription"));
      else if (!ready && current?.issue) setError(workbuddyErrorMessage(current, t));
      else if (!ready) setError(text(!configured ? "configurationMissingDescription"
        : connection?.mcp_trusted === undefined || connection.mcp_enabled === undefined ? "checkFailedDescription"
          : !connection.mcp_trusted ? "trustRequiredDescription" : "enableRequiredDescription"));
      setStage(ready ? "success" : "notReady");
    } catch {
      if (mounted.current) { setError(text("checkFailedDescription")); setStage("notReady"); }
    } finally { busy.current = false; }
  };
  const installing = stage === "installing", checking = stage === "checking";
  const current = stage === "prepare" ? 0 : ["installing", "installFailed", "entry", "trust", "enable"].includes(stage) ? 1 : 2;
  let primary;
  if (stage === "prepare" || stage === "installFailed") primary = <Button type="primary" onClick={() => void install()}>{text(stage === "prepare" ? "saveAndContinue" : "retryInstall")}</Button>;
  else if (stage === "entry") primary = <Button type="primary" onClick={() => setStage(agent === "codex" ? "enable" : "trust")}>{text("next")}</Button>;
  else if (stage === "trust") primary = <Button type="primary" onClick={() => setStage("enable")}>{text("trustConfirmation")}</Button>;
  else if (stage === "enable") primary = <Button type="primary" onClick={() => setStage("verify")}>{text("enabledConfirmation")}</Button>;
  else if (stage === "verify" || stage === "notReady") primary = <Button type="primary" onClick={() => void check()}>{text(stage === "notReady" ? "retryCheck" : "check")}</Button>;
  else if (stage === "success") primary = <Button type="primary" onClick={onClose}>{text("done")}</Button>;
  else primary = <Button type="primary" loading disabled>{text(installing ? "installing" : "checking")}</Button>;

  const imageStage = stage === "entry" ? "entry" : stage === "trust" ? "trust" : "enable";
  const imageURL = agent === "codex" ? "/installation-guides/codex/enable.png"
    : `/installation-guides/workbuddy/${imageStage}.${imageStage === "trust" ? "png" : "jpg"}`;
  const imageAlt = text(`${imageStage}ImageAlt`);
  const showInstructions = ["entry", "trust", "enable"].includes(stage)
    || agent === "codex" && ["prepare", "installing", "installFailed", "notReady"].includes(stage);
  return <><Modal open centered width={640} className="agent-integration-installation-guide"
    title={text("title")} onCancel={onClose} closable={!installing && !checking}
    maskClosable={false} keyboard={!installing && !checking}
    footer={<div className="agent-integration-installation-footer">
      <Button onClick={onClose} disabled={installing || checking}>{text(stage === "prepare" ? "cancel" : stage === "success" ? "close" : "later")}</Button>
      <Space wrap key={stage}>
        {agent === "workbuddy" && (stage === "trust" || stage === "enable") && <Button onClick={() => setStage(stage === "trust" ? "entry" : "trust")}>{text("back")}</Button>}
        {["verify", "notReady"].includes(stage) && <Button onClick={() => setStage(agent === "codex" ? stage === "notReady" ? "prepare" : "enable" : "trust")}>{text("viewEnableSteps")}</Button>}
        {primary}
      </Space>
    </div>}>
    <Steps size="small" current={current} items={["saveStep", "installStep", "checkStep"].map(key => ({ title: text(key) }))} />
    <div className="agent-integration-installation-content" aria-live="polite">
      {stage === "prepare" && <Typography.Paragraph>{text("saveDescription")}</Typography.Paragraph>}
      {(installing || checking) && <Space direction="vertical" align="center" className="agent-integration-installation-progress"><Spin /><Typography.Text>{text(installing ? "installingDescription" : "checkingDescription")}</Typography.Text></Space>}
      {stage === "installFailed" && <Alert type="error" showIcon message={text("installFailedTitle")} description={error} />}
      {(stage === "entry" || stage === "trust" || stage === "enable") &&
        <Alert type="success" showIcon message={text("installedTitle")} description={text("installedDescription")} />
      }
      {showInstructions && <>
        <Typography.Title level={5}>{text(`${imageStage}Title`)}</Typography.Title>
        <Typography.Paragraph>{text(`${imageStage}Description`)}</Typography.Paragraph>
        <button type="button" className="agent-integration-installation-image" aria-label={text("enlargeImage")} onClick={() => setZoomed(true)}><img src={imageURL} alt={imageAlt} /></button>
        <Typography.Paragraph type="secondary" className="agent-integration-installation-note">{text("imageNote")}</Typography.Paragraph>
      </>}
      {stage === "verify" && <Typography.Paragraph>{text("verifyDescription")}</Typography.Paragraph>}
      {stage === "notReady" && <Alert type="warning" showIcon message={text("notReadyTitle")} description={error || text("notReadyDescription")} />}
      {stage === "success" && <>
        <Alert type="success" showIcon message={text("successTitle")} description={text("successDescription")} />
        {controlUnavailable && <Alert type="warning" showIcon message={text("controlUnavailableTitle")} description={controlError || text("controlUnavailableDescription")} />}
      </>}
    </div>
  </Modal>
    <Modal open={zoomed} title={imageAlt} footer={null} width={960} onCancel={() => setZoomed(false)}><img src={imageURL} alt={imageAlt} style={{ width: "100%" }} /></Modal>
  </>;
}
