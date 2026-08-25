import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Card, Space, Spin, Tag, Typography, message } from "antd";
import {
  DisconnectOutlined,
  LinkOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  agentIntegrationAction,
  agentIntegrationStatuses,
  type DesktopAgent,
  type DesktopAgentIntegrationResult,
  type DesktopAgentIntegrationStatus,
} from "@/runtime/desktopBridge";
import "./index.scss";

const REQUIRED_TOOLS = [
  "cloud_document.list",
  "cloud_document.get",
  "cloud_document.search",
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

const AGENTS: Array<{ id: DesktopAgent; name: string; icon: string }> = [
  { id: "codex", name: "Codex", icon: "/assistant-icons/codex.png" },
  { id: "cursor", name: "Cursor", icon: "/assistant-icons/cursor.png" },
  { id: "workbuddy", name: "WorkBuddy", icon: "/assistant-icons/workbuddy.png" },
  { id: "traework", name: "TRAE Work", icon: "/assistant-icons/traework.png" },
  { id: "deepseek-harness", name: "DeepSeek Harness", icon: "/assistant-icons/deepseek.png" },
];

type StatusMap = Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>>;

let cachedSnapshot: { statuses: StatusMap; error: string } | null = null;

function resultError(result: DesktopAgentIntegrationResult): string {
  if (result.ok) return "";
  if (result.error instanceof Error) return result.error.message;
  return result.error ? String(result.error) : result.reason;
}

export default function AgentIntegrationPage() {
  const { t } = useTranslation();
  const [statuses, setStatuses] = useState<StatusMap>(() => cachedSnapshot?.statuses || {});
  const [loading, setLoading] = useState(() => cachedSnapshot === null);
  const [action, setAction] = useState("");
  const [error, setError] = useState(() => cachedSnapshot?.error || "");

  const refresh = useCallback(async () => {
    setLoading(true);
    const result = await agentIntegrationStatuses(AGENTS.map(({ id }) => id));
    if (result.ok) {
      setStatuses(result.data);
      setError("");
      cachedSnapshot = { statuses: result.data, error: "" };
    } else {
      const nextError = result.reason === "unavailable" ? t("agentIntegration.bridgeUnavailable") : resultError(result);
      setStatuses({});
      setError(nextError);
      cachedSnapshot = { statuses: {}, error: nextError };
    }
    setLoading(false);
  }, [t]);

  useEffect(() => {
    if (cachedSnapshot === null) void refresh();
  }, [refresh]);

  const runAction = async (agent: DesktopAgent, nextAction: "connect" | "disconnect") => {
    setAction(`${agent}:${nextAction}`);
    const result = await agentIntegrationAction(agent, nextAction);
    setAction("");
    if (result.ok) {
      setStatuses((current) => {
        const next = { ...current, [agent]: result.data };
        cachedSnapshot = { statuses: next, error: "" };
        return next;
      });
      setError("");
      const name = AGENTS.find((item) => item.id === agent)?.name || agent;
      message.success(t(nextAction === "connect" ? "agentIntegration.connectSuccess" : "agentIntegration.disconnectSuccess", { agent: name }));
      return;
    }
    const nextError = resultError(result);
    setError(nextError);
    cachedSnapshot = { statuses, error: nextError };
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
          onClose={() => {
            setError("");
            cachedSnapshot = { statuses, error: "" };
          }}
        />
      )}

      <Spin spinning={loading}>
        <div className="agent-integration-grid">
          {AGENTS.map(({ id, name, icon }) => {
            const status = statuses[id];
            const availableTools = new Set(status?.tools || []);
            const allToolsReady = REQUIRED_TOOLS.every((tool) => availableTools.has(tool));
            const configured = Boolean(status?.configured && status?.owned);
            const foreignConfiguration = Boolean(status?.configured && !status?.owned);
            const healthy = Boolean(configured && status?.ready && allToolsReady);

            return (
              <Card key={id} className="agent-integration-card">
                <div className="agent-integration-card-title">
                  <div className="agent-integration-identity">
                    <span className="agent-integration-logo" aria-hidden="true">
                      <img alt="" src={icon} />
                    </span>
                    <Typography.Title level={4}>{name}</Typography.Title>
                  </div>
                  <Tag color={healthy ? "success" : "default"}>
                    <span className="agent-integration-state-dot" />
                    {healthy
                      ? t("agentIntegration.connected")
                      : foreignConfiguration
                        ? t("agentIntegration.configConflict")
                      : status?.installed
                        ? t("agentIntegration.notConnected")
                        : t("agentIntegration.notDetected")}
                  </Tag>
                </div>

                <Space className="agent-integration-actions">
                  <Button
                    type="primary"
                    icon={<LinkOutlined />}
                    loading={action === `${id}:connect`}
                    disabled={action !== "" || !status?.installed || foreignConfiguration || healthy}
                    onClick={() => void runAction(id, "connect")}
                  >
                    {t("agentIntegration.connect")}
                  </Button>
                  <Button
                    icon={<DisconnectOutlined />}
                    loading={action === `${id}:disconnect`}
                    disabled={action !== "" || !configured || foreignConfiguration}
                    onClick={() => void runAction(id, "disconnect")}
                  >
                    {t("agentIntegration.disconnect")}
                  </Button>
                </Space>
              </Card>
            );
          })}
        </div>
      </Spin>
    </div>
  );
}
