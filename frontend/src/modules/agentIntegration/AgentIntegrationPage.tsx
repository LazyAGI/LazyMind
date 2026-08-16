import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Card, Space, Spin, Tag, Typography, message } from "antd";
import {
  CheckCircleOutlined,
  CopyOutlined,
  DisconnectOutlined,
  LinkOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  agentIntegrationStatus,
  codexIntegrationAction,
  type DesktopAgent,
  type DesktopAgentIntegrationResult,
  type DesktopAgentIntegrationStatus,
} from "@/runtime/desktopBridge";
import "./index.scss";

const REQUIRED_TOOLS = [
  "knowledge.list",
  "knowledge.document.list",
  "knowledge.document.get",
  "knowledge.search",
  "skill.list",
  "skill.get",
  "workflow.list",
  "workflow.get",
  "workflow.input.import",
  "workflow.input.get",
  "workflow.start",
  "workflow.state",
  "workflow.session.list",
  "workflow.session.stop",
  "workflow.session.resume",
  "workflow.step.begin",
  "workflow.step.resume",
  "workflow.step.submit",
  "workflow.artifact.list",
  "workflow.artifact.get",
];

const AGENTS: Array<{ id: DesktopAgent; name: string; manual: boolean }> = [
  { id: "codex", name: "Codex CLI", manual: false },
  { id: "cursor", name: "Cursor", manual: true },
  { id: "workbuddy", name: "WorkBuddy", manual: true },
  { id: "traework", name: "TRAE Work", manual: true },
  { id: "deepseek-harness", name: "DeepSeek Harness", manual: true },
];

const SETUP_DESCRIPTION: Record<Exclude<DesktopAgent, "codex">, string> = {
  cursor: "agentIntegration.cursorSetupDescription",
  workbuddy: "agentIntegration.workBuddySetupDescription",
  traework: "agentIntegration.traeWorkSetupDescription",
  "deepseek-harness": "agentIntegration.deepSeekHarnessSetupDescription",
};

const CONFIG_INSTRUCTION: Record<NonNullable<DesktopAgentIntegrationStatus["setup"]>["method"], string> = {
  cursor_install_url: "agentIntegration.cursorConfigFallback",
  config_file: "agentIntegration.workBuddyConfig",
  trae_config_file: "agentIntegration.traeConfigFile",
  dsh_profile_patch: "agentIntegration.appendProfilePatch",
};

type StatusMap = Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>>;

function resultError(result: DesktopAgentIntegrationResult): string {
  if (result.ok) return "";
  if (result.error instanceof Error) return result.error.message;
  return result.error ? String(result.error) : result.reason;
}

export default function AgentIntegrationPage() {
  const { t } = useTranslation();
  const [statuses, setStatuses] = useState<StatusMap>({});
  const [loading, setLoading] = useState(true);
  const [action, setAction] = useState<"connect" | "disconnect" | "">("");
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    const results = await Promise.all(AGENTS.map(async ({ id }) => ({ id, result: await agentIntegrationStatus(id) })));
    const next: StatusMap = {};
    const failures: string[] = [];
    for (const { id, result } of results) {
      if (result.ok) next[id] = result.data;
      else failures.push(`${id}: ${resultError(result)}`);
    }
    setStatuses(next);
    setError(failures.join("\n"));
    setLoading(false);
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const runCodexAction = async (nextAction: "connect" | "disconnect") => {
    setAction(nextAction);
    const result = await codexIntegrationAction(nextAction);
    setAction("");
    if (result.ok) {
      setStatuses((current) => ({ ...current, codex: result.data }));
      setError("");
      message.success(t(nextAction === "connect" ? "agentIntegration.connectSuccess" : "agentIntegration.disconnectSuccess"));
      return;
    }
    setError(resultError(result));
  };

  const copySetupText = async (value: string | undefined, successMessage: string) => {
    if (!value || !navigator.clipboard?.writeText) {
      setError(t("agentIntegration.copyUnavailable"));
      return;
    }
    try {
      await navigator.clipboard.writeText(value);
      message.success(successMessage);
    } catch (copyError) {
      setError(copyError instanceof Error ? copyError.message : String(copyError));
    }
  };

  return (
    <div className="agent-integration-page">
      <div className="agent-integration-header">
        <div>
          <Typography.Title level={2}>{t("agentIntegration.title")}</Typography.Title>
          <Typography.Paragraph type="secondary">{t("agentIntegration.description")}</Typography.Paragraph>
        </div>
        <Button icon={<ReloadOutlined />} onClick={() => void refresh()} loading={loading}>
          {t("common.refresh")}
        </Button>
      </div>

      {error && (
        <Alert
          type="error"
          showIcon
          closable
          message={t("agentIntegration.operationFailed")}
          description={<span className="agent-integration-error">{error}</span>}
          onClose={() => setError("")}
        />
      )}

      <Spin spinning={loading}>
        <div className="agent-integration-grid">
          {AGENTS.map(({ id, name, manual }) => {
            const status = statuses[id];
            const availableTools = new Set(status?.tools || []);
            const allToolsReady = REQUIRED_TOOLS.every((tool) => availableTools.has(tool));
            const configured = Boolean(status?.configured && status?.owned);
            const foreignConfiguration = Boolean(status?.configured && !status?.owned);
            const healthy = manual
              ? Boolean(status?.ready && allToolsReady)
              : Boolean(configured && status?.ready && allToolsReady);

            return (
              <Card key={id} className="agent-integration-card">
                <div className="agent-integration-card-title">
                  <div>
                    <Typography.Title level={4}>{name}</Typography.Title>
                    <Typography.Text type="secondary">
                      {status?.version || t("agentIntegration.agentUnavailable")}
                    </Typography.Text>
                  </div>
                  <Tag color={healthy ? "success" : "default"}>
                    {manual
                      ? healthy ? t("agentIntegration.readyToConfigure") : t("agentIntegration.notReady")
                      : healthy ? t("agentIntegration.connected") : t("agentIntegration.notConnected")}
                  </Tag>
                </div>

                <div className="agent-integration-summary">
                  <div>
                    <Typography.Text type="secondary">{t("agentIntegration.agent")}</Typography.Text>
                    <strong>{status?.installed ? t("agentIntegration.detected") : t("agentIntegration.notDetected")}</strong>
                  </div>
                  <div>
                    <Typography.Text type="secondary">{t("agentIntegration.service")}</Typography.Text>
                    <strong>{status?.service_ready ? t("agentIntegration.ready") : t("agentIntegration.unavailable")}</strong>
                  </div>
                  <div>
                    <Typography.Text type="secondary">{t("agentIntegration.tools")}</Typography.Text>
                    <strong>{status?.tools?.length || 0} / {REQUIRED_TOOLS.length}</strong>
                  </div>
                </div>

                {status?.readiness_error && <Alert type="warning" showIcon message={status.readiness_error} />}

                <div className="agent-integration-tools">
                  {REQUIRED_TOOLS.map((tool) => (
                    <Tag key={tool} color={availableTools.has(tool) ? "blue" : "default"}>
                      {availableTools.has(tool) && <CheckCircleOutlined />} {tool}
                    </Tag>
                  ))}
                </div>

                {manual ? (
                  <>
                    <Alert
                      type="info"
                      showIcon
                      message={t("agentIntegration.manualSetupTitle")}
                      description={t(SETUP_DESCRIPTION[id as Exclude<DesktopAgent, "codex">])}
                    />
                    {status?.setup?.method === "cursor_install_url" && status.setup.url && (
                      <Button
                        type="primary"
                        icon={<LinkOutlined />}
                        href={status.setup.url}
                        target="_blank"
                      >
                        {t("agentIntegration.openCursorInstall")}
                      </Button>
                    )}
                    {status?.setup?.configuration && (
                      <div className="agent-integration-fallback">
                        <Typography.Text type="secondary">
                          {t(CONFIG_INSTRUCTION[status.setup.method], { path: status.setup.config_path })}
                        </Typography.Text>
                        <Typography.Paragraph className="agent-integration-command" code>
                          {status.setup.configuration}
                        </Typography.Paragraph>
                        <Button
                          icon={<CopyOutlined />}
                          onClick={() => void copySetupText(
                            status.setup?.configuration,
                            t("agentIntegration.configCopied", { agent: name }),
                          )}
                        >
                          {t(status.setup.method === "dsh_profile_patch"
                            ? "agentIntegration.copyProfilePatch"
                            : "agentIntegration.copyConfig")}
                        </Button>
                      </div>
                    )}
                    <Typography.Paragraph type="secondary" className="agent-integration-next-step">
                      {t("agentIntegration.newSessionHint", { agent: name })}
                    </Typography.Paragraph>
                  </>
                ) : (
                  <>
                    {foreignConfiguration && <Alert type="warning" showIcon message={t("agentIntegration.unmanaged")} />}
                    <Typography.Paragraph type="secondary" className="agent-integration-security">
                      {t("agentIntegration.securityNote")}
                    </Typography.Paragraph>
                    <Space>
                      <Button
                        type="primary"
                        icon={<LinkOutlined />}
                        loading={action === "connect"}
                        disabled={action !== "" || !status?.installed || foreignConfiguration || healthy}
                        onClick={() => void runCodexAction("connect")}
                      >
                        {configured ? t("agentIntegration.reconnect") : t("agentIntegration.connect")}
                      </Button>
                      <Button
                        icon={<DisconnectOutlined />}
                        loading={action === "disconnect"}
                        disabled={action !== "" || !configured}
                        onClick={() => void runCodexAction("disconnect")}
                      >
                        {t("agentIntegration.disconnect")}
                      </Button>
                    </Space>
                  </>
                )}
              </Card>
            );
          })}
        </div>
      </Spin>
    </div>
  );
}
