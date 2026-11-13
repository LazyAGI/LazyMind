import { getNodeDependencyStatus, type NodeDependencyStatus } from "@/modules/modelProvider/api/systemDependencies";
import { isDesktopRuntime, isLocalRuntime } from "@/runtime/mode";
import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space, Spin, Steps, Tag, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { agentIntegrationStatuses, type DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

import { workbuddyErrorMessage } from "./workbuddyErrors";
import { codexErrorMessage } from "./codexErrors";
import { requiresAgentQuit } from "./agentActions";

type Stage = "nodeRequired" | "prepare" | "installing" | "login" | "quitRequired" | "installFailed" | "entry" | "trust" | "enable" | "verify" | "checking" | "notReady" | "success";

function NodePrerequisite() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<NodeDependencyStatus>();
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let cancelled = false;
    setStatus(undefined);
    setFailed(false);
    void getNodeDependencyStatus().then(value => {
      if (!cancelled) setStatus(value);
    }).catch(() => {
      if (!cancelled) setFailed(true);
    });
    return () => { cancelled = true; };
  }, [attempt]);
  return <div className="agent-integration-node-prerequisite" aria-live="polite">
    <div className="agent-integration-node-prerequisite__copy">
      <div><strong>{t("nodeDependency.title")}</strong> {status && <Tag color={status.installed ? "success" : "warning"}>{status.installed ? t("nodeDependency.ready") : t("nodeDependency.missing")}{status.version ? ` · ${status.version}` : ""}</Tag>}</div>
      <Typography.Text type="secondary">{failed ? t("nodeDependency.failed") : t("nodeDependency.guidePrerequisite")}</Typography.Text>
    </div>
    {!status && !failed ? <Spin size="small" aria-label={t("nodeDependency.checking")} /> : failed ? <Button size="small" onClick={() => setAttempt(value => value + 1)}>{t("nodeDependency.refresh")}</Button> : !status?.installed && <Button size="small" href="/settings?section=system_tools#node-dependency">{t("nodeDependency.goto")}</Button>}
  </div>;
}

export default function WorkBuddyInstallationGuide({ status, onConnect, onLogin, onStatus, onClose, agent = "workbuddy" }: {
  agent?: "workbuddy" | "codex";
  status?: DesktopAgentIntegrationStatus;
  onConnect: () => Promise<DesktopAgentIntegrationStatus | undefined>;
  onLogin?: () => Promise<DesktopAgentIntegrationStatus | undefined>;
  onStatus: (status: DesktopAgentIntegrationStatus) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const text = (key: string) => t(`agentIntegration.${agent === "codex" ? "codexInstallGuide" : "workbuddyInstallGuide"}.${key}`);
  const confirmationStage = agent === "codex" ? "enable" : "entry";
  const checkLocalNode = agent === "codex" && (isLocalRuntime() || isDesktopRuntime());
  // An installed plugin survives a normal Codex launch without CDP. It must
  // still go through the explicit connection/restart step before confirmation.
  const [stage, setStage] = useState<Stage>(requiresAgentQuit(status) ? "quitRequired" : status?.state === "enabled" && status.connection?.mcp_configured
    && (agent === "codex" ? status.connection.control_ready && !status.issue
      : !status.issue || ["mcp_trust_required", "mcp_enable_required"].includes(status.issue.code)) ? confirmationStage : "prepare");
  const [error, setError] = useState("");
  const [zoomed, setZoomed] = useState(false);
  const [diagnostic, setDiagnostic] = useState("");
  const mounted = useRef(true);
  const busy = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const install = async () => {
    if (busy.current) return;
    busy.current = true;
    setStage("installing"); setError("");
    try {
      if (checkLocalNode) {
        const node = await getNodeDependencyStatus();
        if (!mounted.current) return;
        if (!node.installed) { setStage("nodeRequired"); return; }
      }
      const result = await onConnect();
      if (!mounted.current) return;
      setDiagnostic(result?.diagnostic_id || result?.issue?.operation_id || "");
      if (requiresAgentQuit(result)) {
        setStage("quitRequired"); return;
      }
      if (agent === "workbuddy" && result?.action?.kind === "login" && onLogin) {
        setStage("login");
        const login = await onLogin();
        if (!mounted.current) return;
        if (!login) { setError(text("loginFailed")); setStage("installFailed"); }
        return;
      }
      if (result?.state !== "enabled" || !result.connection?.mcp_configured
        || agent === "codex" && result.issue && !["mcp_enable_required", "mcp_trust_required"].includes(result.issue.code)) {
        setError(agent === "codex" ? codexErrorMessage(result, t, "installFailedDescription")
          : result?.issue ? workbuddyErrorMessage(result, t) : result?.message || text("installFailedDescription"));
        setStage("installFailed"); return;
      }
      // Connect includes restart and installation checks. Trust is a separate step.
      setStage(confirmationStage);
    } catch (cause) {
      if (mounted.current) {
        setError(agent === "codex" && cause instanceof Error && cause.message ? cause.message : text("installFailedDescription"));
        setStage("installFailed");
      }
    } finally { busy.current = false; }
  };
  useEffect(() => {
    if (stage !== "login") return;
    let stopped = false, inFlight = false;
    const poll = async () => {
      if (inFlight || busy.current) return;
      inFlight = true;
      try {
        const result = await agentIntegrationStatuses();
        if (stopped || !mounted.current || !result.ok) return;
        const current = result.data[agent];
        if (current) onStatus(current);
        if (current?.login?.state === "succeeded") { stopped = true; await install(); }
        else if (current?.login && ["failed", "cancelled"].includes(current.login.state)) {
          setError(text("loginFailed")); setStage("installFailed");
        }
      } catch { /* Keep waiting for the bounded native login operation. */ }
      finally { inFlight = false; }
    };
    const timer = window.setInterval(() => void poll(), 1500);
    const timeout = window.setTimeout(() => {
      if (!stopped && mounted.current) { setError(text("loginFailed")); setStage("installFailed"); }
    }, 130000);
    return () => { stopped = true; window.clearInterval(timer); window.clearTimeout(timeout); };
  }, [stage, agent]);

  const check = async (automatic = false) => {
    if (busy.current) return;
    busy.current = true;
    if (!automatic) { setStage("checking"); setError(""); }
    const checkFailed = () => {
      if (!automatic || agent === "codex" && ["notReady", "installFailed"].includes(stage)) {
        setError(text("checkFailedDescription")); setDiagnostic("");
        if (!automatic) setStage("notReady");
      }
    };
    try {
      const result = await agentIntegrationStatuses();
      if (!mounted.current) return;
      if (!result.ok) { checkFailed(); return; }
      const current = result.data[agent];
      if (current) onStatus(current);
      const connection = current?.connection;
      const configured = current?.state === "enabled" && connection?.mcp_configured;
      const connectorReady = configured && connection?.mcp_enabled && connection.mcp_trusted;
      const ready = connectorReady && connection.control_ready && !current?.issue;
      if (!automatic || agent === "codex" || ready) setDiagnostic(current?.diagnostic_id || current?.issue?.operation_id || "");
      if (automatic && !ready) {
        if (agent === "codex" && ["notReady", "installFailed"].includes(stage)) {
          setError(codexErrorMessage(current, t, connectorReady ? "controlUnavailableDescription" : "notReadyDescription"));
        }
        return;
      }
      if (!ready && agent === "codex") setError(codexErrorMessage(current, t, connectorReady ? "controlUnavailableDescription" : "notReadyDescription"));
      else if (!ready && current?.issue) setError(workbuddyErrorMessage(current, t));
      else if (connectorReady && !connection?.control_ready) setError(text("controlUnavailableDescription"));
      else if (!ready) setError(text(!configured ? "configurationMissingDescription"
        : connection?.mcp_trusted === undefined || connection.mcp_enabled === undefined ? "checkFailedDescription"
          : !connection.mcp_trusted ? "trustRequiredDescription" : "enableRequiredDescription"));
      setStage(ready ? "success" : "notReady");
    } catch {
      if (mounted.current) checkFailed();
    } finally { busy.current = false; }
  };
  // A late startup heartbeat or a return from trust/enable settings can finish
  // setup after the install request failed. These checks never install/restart.
  useEffect(() => {
    if (!["installFailed", "entry", "trust", "enable", "verify", "notReady"].includes(stage)) return;
    const recheck = () => { if (document.visibilityState === "visible") void check(true); };
    const timer = window.setInterval(recheck, 3000);
    window.addEventListener("focus", recheck);
    return () => { window.clearInterval(timer); window.removeEventListener("focus", recheck); };
  }, [stage, agent]);
  const installing = stage === "installing", checking = stage === "checking", loggingIn = stage === "login";
  const current = ["prepare", "nodeRequired"].includes(stage) ? 0 : ["installing", "login", "quitRequired", "installFailed", "entry", "trust", "enable"].includes(stage) ? 1 : 2;
  let primary;
  if (stage === "prepare" || stage === "installFailed") primary = <Button type="primary" onClick={() => void install()}>{text(stage === "prepare" ? "saveAndContinue" : "retryInstall")}</Button>;
  else if (stage === "nodeRequired") primary = <Space><Button onClick={() => void install()}>{t("nodeDependency.refresh")}</Button><Button type="primary" href="/settings?section=system_tools#node-dependency">{t("nodeDependency.goto")}</Button></Space>;
  else if (stage === "quitRequired") primary = <Button type="primary" onClick={() => void install()}>{t("agentIntegration.quitRequired.retryInstall")}</Button>;
  else if (stage === "entry") primary = <Button type="primary" onClick={() => setStage(agent === "codex" ? "enable" : "trust")}>{text("next")}</Button>;
  else if (stage === "trust") primary = <Button type="primary" onClick={() => setStage("enable")}>{text("trustConfirmation")}</Button>;
  else if (stage === "enable") primary = <Button type="primary" onClick={() => setStage("verify")}>{text("enabledConfirmation")}</Button>;
  else if (stage === "verify" || stage === "notReady") primary = <Button type="primary" onClick={() => void check()}>{text(stage === "notReady" ? "retryCheck" : "check")}</Button>;
  else if (stage === "success") primary = <Button type="primary" onClick={onClose}>{text("done")}</Button>;
  else primary = <Button type="primary" loading disabled>{text(loggingIn ? "waitingForLogin" : installing ? "installing" : "checking")}</Button>;

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
        {["verify", "notReady"].includes(stage) && <Button onClick={() => setStage(agent === "codex" ? stage === "notReady" ? "prepare" : "enable" : "trust")}>{text(agent === "codex" && stage === "notReady" ? "reconnect" : "viewEnableSteps")}</Button>}
        {agent === "workbuddy" && stage === "notReady" && <Button onClick={() => setStage("prepare")}>{text("reconnect")}</Button>}
        {primary}
      </Space>
    </div>}>
    <Steps size="small" current={current} items={["saveStep", "installStep", "checkStep"].map(key => ({ title: text(key) }))} />
    <div className="agent-integration-installation-content" aria-live="polite">
      {stage === "prepare" && <Typography.Paragraph>{text("saveDescription")}</Typography.Paragraph>}
      {checkLocalNode && stage === "prepare" && <NodePrerequisite />}
      {stage === "nodeRequired" && <Alert type="warning" showIcon message={t("nodeDependency.title")} description={t("nodeDependency.required")} />}
      {loggingIn && <Alert type="info" showIcon message={text("waitingForLogin")} description={text("loginDescription")} />}
      {stage === "quitRequired" && <Alert type="warning" showIcon
        message={t("agentIntegration.quitRequired.title", { agent: status?.display_name || (agent === "codex" ? "Codex" : "WorkBuddy") })}
        description={t("agentIntegration.quitRequired.description", { agent: status?.display_name || (agent === "codex" ? "Codex" : "WorkBuddy") })} />}
      {(installing || checking) && <Space direction="vertical" align="center" className="agent-integration-installation-progress"><Spin /><Typography.Text>{text(installing ? "installingDescription" : "checkingDescription")}</Typography.Text></Space>}
      {stage === "installFailed" && <Alert type="error" showIcon message={text("installFailedTitle")} description={error} />}
      {["installFailed", "quitRequired", "notReady"].includes(stage) && diagnostic && <Typography.Text type="secondary" copyable>{t("agentIntegration.setupFeedback.diagnosticID")}: {diagnostic}</Typography.Text>}
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
      </>}
    </div>
  </Modal>
    <Modal open={zoomed} title={imageAlt} footer={null} width={960} onCancel={() => setZoomed(false)}><img src={imageURL} alt={imageAlt} style={{ width: "100%" }} /></Modal>
  </>;
}
