import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Alert, Button, Card, Space, Spin, Tag, Typography, message } from "antd";
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  DisconnectOutlined,
  FolderOpenOutlined,
  LinkOutlined,
  LoginOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  agentIntegrationAction,
  agentIntegrationStatuses,
  agentExecutableBindings,
  bindAgentExecutable,
  clearAgentExecutable,
  executorIntegrationAction,
  executorIntegrationPolicies,
  getDesktopPlatform,
  selectExecutable,
  type DesktopAgent,
  type DesktopAgentBindingTarget,
  type DesktopAgentIntegrationAction,
  type DesktopAgentIntegrationStatus,
  type DesktopAgentIntegrationState,
  type DesktopExecutorPolicy,
  type DesktopExecutorPolicyAction,
  type DesktopExecutorProvider,
} from "@/runtime/desktopBridge";
import {
  ConversationSettingsApi,
  type ChatExecutorDescriptor,
} from "@/modules/chat/utils/request";
import "./index.scss";

interface AgentDefinition {
  id: DesktopAgent;
  name: string;
  icon: string;
  installURL: string;
  executorName?: string;
  executorLogin?: boolean;
  mcpBindingTarget?: DesktopAgentBindingTarget;
  executorBindingTarget?: DesktopAgentBindingTarget;
}

const AGENTS: AgentDefinition[] = [
  {
    id: "codex", name: "Codex", icon: "/assistant-icons/codex.png",
    installURL: "https://developers.openai.com/codex/cli",
    executorName: "Codex CLI", executorLogin: true,
    mcpBindingTarget: "codex-cli", executorBindingTarget: "codex-cli",
  },
  {
    id: "cursor", name: "Cursor", icon: "/assistant-icons/cursor.png",
    installURL: "https://cursor.com/downloads",
    executorName: "Cursor Agent CLI", executorLogin: true,
    mcpBindingTarget: "cursor-desktop", executorBindingTarget: "cursor-cli",
  },
  {
    id: "workbuddy", name: "WorkBuddy", icon: "/assistant-icons/workbuddy.png",
    installURL: "https://www.workbuddy.cn",
    executorName: "CodeBuddy Code",
    mcpBindingTarget: "workbuddy-desktop", executorBindingTarget: "codebuddy-cli",
  },
  {
    id: "raccoon", name: "Raccoon", icon: "/assistant-icons/raccoon.svg",
    installURL: "https://office.xiaohuanxiong.com/download",
    mcpBindingTarget: "raccoon-desktop",
  },
  {
    id: "traework", name: "TRAE Work", icon: "/assistant-icons/traework.png",
    installURL: "https://www.trae.ai",
    mcpBindingTarget: "traework-desktop",
  },
  {
    id: "deepseek-harness", name: "DeepSeek Harness", icon: "/assistant-icons/deepseek.png",
    installURL: "https://github.com/deepseek-ai/deepseek-harness",
  },
];

const STATE_COLORS: Record<DesktopAgentIntegrationState, string> = {
  requirements_missing: "default",
  ready: "processing",
  action_required: "warning",
  enabled: "success",
  conflict: "error",
  error: "error",
};

const EXECUTOR_SYNC_ATTEMPTS = 6;
const EXECUTOR_SYNC_DELAY_MS = 500;

type StatusMap = Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>>;
type ExecutorPolicyMap = Partial<Record<DesktopExecutorProvider, DesktopExecutorPolicy>>;
type BindingMap = Partial<Record<DesktopAgentBindingTarget, string>>;

export default function AgentIntegrationPage() {
  const { t } = useTranslation();
  const [statuses, setStatuses] = useState<StatusMap>({});
  const [executors, setExecutors] = useState<ChatExecutorDescriptor[]>([]);
  const [executorPolicies, setExecutorPolicies] = useState<ExecutorPolicyMap>({});
  const [bindings, setBindings] = useState<BindingMap>({});
  const [loading, setLoading] = useState(true);
  const [action, setAction] = useState("");
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    const failures: string[] = [];
    try {
      const result = await agentIntegrationStatuses();
      if (result.ok) setStatuses(result.data);
      else failures.push(result.error instanceof Error ? result.error.message : result.reason);

      const policyResult = await executorIntegrationPolicies();
      if (policyResult.ok) setExecutorPolicies(policyResult.data);
      else failures.push(policyResult.error instanceof Error ? policyResult.error.message : policyResult.reason);

      const bindingResult = await agentExecutableBindings();
      if (bindingResult.ok) setBindings(bindingResult.data);
      else failures.push(bindingResult.error instanceof Error ? bindingResult.error.message : bindingResult.reason);

      try {
        let values: ChatExecutorDescriptor[] = [];
        for (let attempt = 0; attempt < EXECUTOR_SYNC_ATTEMPTS; attempt += 1) {
          const response = await ConversationSettingsApi().listChatExecutors();
          values = response.data.data.executors;
          if (!values.some((executor) => executor.kind === "external" && !executor.host_online)) break;
          if (attempt + 1 < EXECUTOR_SYNC_ATTEMPTS) {
            await new Promise((resolve) => window.setTimeout(resolve, EXECUTOR_SYNC_DELAY_MS));
          }
        }
        setExecutors(values);
      } catch (executorError) {
        failures.push(executorError instanceof Error ? executorError.message : String(executorError));
      }
    } finally {
      setError(failures.join("\n"));
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const runAction = async (agent: DesktopAgent, nextAction: DesktopAgentIntegrationAction) => {
    const key = `${agent}:${nextAction}`;
    setAction(key);
    const result = await agentIntegrationAction(agent, nextAction);
    setAction("");
    if (!result.ok) {
      setError(result.error instanceof Error ? result.error.message : result.error ? String(result.error) : result.reason);
      return;
    }
    setStatuses((current) => ({ ...current, [agent]: result.data }));
    setError("");
    if (nextAction === "disconnect") {
      message.success(t("agentIntegration.disconnectSuccess", { agent: result.data.display_name }));
    } else if (result.data.state === "enabled") {
      message.success(t("agentIntegration.enableSuccess", { agent: result.data.display_name }));
    }
    if (nextAction === "login") await refresh();
  };

  const runExecutorAction = async (provider: DesktopExecutorProvider, nextAction: DesktopExecutorPolicyAction) => {
    const key = `executor:${provider}:${nextAction}`;
    setAction(key);
    const result = await executorIntegrationAction(provider, nextAction);
    setAction("");
    if (!result.ok) {
      setError(result.error instanceof Error ? result.error.message : result.error ? String(result.error) : result.reason);
      return;
    }
    setExecutorPolicies((current) => ({ ...current, [provider]: result.data }));
    const agentName = AGENTS.find((agent) => agent.id === provider)?.executorName || provider;
    message.success(t(nextAction === "enable"
      ? "agentIntegration.executorEnableSuccess"
      : "agentIntegration.executorDisableSuccess", { agent: agentName }));
    await refresh();
  };

  const runBindingAction = async (target: DesktopAgentBindingTarget, clear: boolean) => {
    let path = "";
    if (!clear) {
      path = await selectExecutable(target) || "";
      if (!path) return;
    }
    setAction(`binding:${target}`);
    const result = clear
      ? await clearAgentExecutable(target)
      : await bindAgentExecutable(target, path);
    setAction("");
    if (!result.ok) {
      setError(result.error instanceof Error ? result.error.message : result.reason);
      return;
    }
    message.success(t(clear
      ? "agentIntegration.executableBindingCleared"
      : "agentIntegration.executableBindingSaved"));
    await refresh();
  };

  const canSelectExecutable = Boolean(getDesktopPlatform());

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
        <IntegrationSection
          title={t("agentIntegration.mcpTitle")}
          description={t("agentIntegration.mcpDescription")}
        >
          {AGENTS.map((agent) => (
            <MCPCard
              key={agent.id}
              agent={agent}
              status={statuses[agent.id]}
              busyAction={action}
              onAction={runAction}
              bindingTarget={agent.mcpBindingTarget}
              bindingConfigured={Boolean(agent.mcpBindingTarget && bindings[agent.mcpBindingTarget])}
              canSelectExecutable={canSelectExecutable}
              onBindingAction={runBindingAction}
              t={t}
            />
          ))}
        </IntegrationSection>

        <IntegrationSection
          title={t("agentIntegration.executorTitle")}
          description={t("agentIntegration.executorDescription")}
        >
          {AGENTS.map((agent) => (
            <ExecutorCard
              key={agent.id}
              agent={agent}
              status={executors.find((item) => item.id === agent.id)}
              enabled={executorPolicies[agent.id as DesktopExecutorProvider]?.enabled ?? true}
              busyAction={action}
              onLogin={(agent) => runAction(agent, "login")}
              onPolicyAction={runExecutorAction}
              bindingConfigured={Boolean(agent.executorBindingTarget && bindings[agent.executorBindingTarget])}
              canSelectExecutable={canSelectExecutable}
              onBindingAction={runBindingAction}
              t={t}
            />
          ))}
        </IntegrationSection>
      </Spin>
    </div>
  );
}

function IntegrationSection({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <section className="agent-integration-section">
      <div className="agent-integration-section-heading">
        <Typography.Title level={3}>{title}</Typography.Title>
        <Typography.Paragraph type="secondary">{description}</Typography.Paragraph>
      </div>
      <div className="agent-integration-grid">{children}</div>
    </section>
  );
}

function MCPCard({
  agent,
  status,
  busyAction,
  onAction,
  bindingTarget,
  bindingConfigured,
  canSelectExecutable,
  onBindingAction,
  t,
}: {
  agent: (typeof AGENTS)[number];
  status?: DesktopAgentIntegrationStatus;
  busyAction: string;
  onAction: (agent: DesktopAgent, action: DesktopAgentIntegrationAction) => Promise<void>;
  bindingTarget?: DesktopAgentBindingTarget;
  bindingConfigured: boolean;
  canSelectExecutable: boolean;
  onBindingAction: (target: DesktopAgentBindingTarget, clear: boolean) => Promise<void>;
  t: TFunction;
}) {
  const state = status?.state || "requirements_missing";
  const requirements = status?.requirements || [];
  const installationMissing = requirements.length > 0 && !requirements[0].satisfied;
  const busy = busyAction.startsWith(`${agent.id}:`);
  const localizedNote = state === "action_required"
    ? t(`agentIntegration.mcpActionNotes.${agent.id}`, { defaultValue: "" })
    : state === "ready"
      ? t(`agentIntegration.mcpNotes.${agent.id}`, { defaultValue: "" })
      : "";
  const note = localizedNote || status?.message || "";

  return (
    <Card className="agent-integration-card">
      <CardTitle name={agent.name} icon={agent.icon}>
        <Tag color={STATE_COLORS[state]}>{t(`agentIntegration.states.${state}`)}</Tag>
      </CardTitle>

      <div className="agent-integration-requirements">
        {requirements.map((requirement) => (
          <div key={requirement.id} className={requirement.satisfied ? "is-ready" : "is-missing"}>
            {requirement.satisfied ? <CheckCircleOutlined /> : <CloseCircleOutlined />}
            <span>{t(`agentIntegration.requirements.${requirement.id}`, { defaultValue: requirement.description })}</span>
          </div>
        ))}
      </div>

      {note && <Alert type={state === "error" || state === "conflict" ? "warning" : "info"} showIcon message={note} />}

      <Space className="agent-integration-actions">
        {state === "enabled" ? (
          <Button
            icon={<DisconnectOutlined />}
            loading={busyAction === `${agent.id}:disconnect`}
            disabled={busy}
            onClick={() => void onAction(agent.id, "disconnect")}
          >
            {t("agentIntegration.disable")}
          </Button>
        ) : state === "ready" ? (
          <Button
            type="primary"
            icon={<LinkOutlined />}
            loading={busyAction === `${agent.id}:connect`}
            disabled={busy}
            onClick={() => void onAction(agent.id, "connect")}
          >
            {t("agentIntegration.enable")}
          </Button>
        ) : status?.action?.kind === "login" ? (
          <Button
            type="primary"
            icon={<LoginOutlined />}
            loading={busyAction === `${agent.id}:login`}
            disabled={busy}
            onClick={() => void onAction(agent.id, "login")}
          >
            {t("agentIntegration.login")}
          </Button>
        ) : status?.action?.kind === "open_url" && status.action.url ? (
          <Button type="primary" icon={<LinkOutlined />} href={status.action.url} target="_blank">
            {t("agentIntegration.continueInAgent", { agent: agent.name })}
          </Button>
        ) : state === "requirements_missing" ? (
          <Button icon={<LinkOutlined />} href={agent.installURL} target="_blank">
            {t("agentIntegration.viewRequirements")}
          </Button>
        ) : null}
        {bindingTarget && canSelectExecutable && installationMissing && (
          <Button
            icon={<FolderOpenOutlined />}
            loading={busyAction === `binding:${bindingTarget}`}
            disabled={busyAction !== ""}
            onClick={() => void onBindingAction(bindingTarget, false)}
          >
            {t(bindingTarget.endsWith("-cli")
              ? "agentIntegration.locateCLI"
              : "agentIntegration.locateApplication")}
          </Button>
        )}
        {bindingTarget && bindingConfigured && canSelectExecutable && (
          <Button
            loading={busyAction === `binding:${bindingTarget}`}
            disabled={busyAction !== ""}
            onClick={() => void onBindingAction(bindingTarget, true)}
          >
            {t("agentIntegration.restoreAutoDetection")}
          </Button>
        )}
      </Space>
    </Card>
  );
}

function ExecutorCard({
  agent,
  status,
  enabled,
  busyAction,
  onLogin,
  onPolicyAction,
  bindingConfigured,
  canSelectExecutable,
  onBindingAction,
  t,
}: {
  agent: AgentDefinition;
  status?: ChatExecutorDescriptor;
  enabled: boolean;
  busyAction: string;
  onLogin: (agent: DesktopAgent) => Promise<void>;
  onPolicyAction: (provider: DesktopExecutorProvider, action: DesktopExecutorPolicyAction) => Promise<void>;
  bindingConfigured: boolean;
  canSelectExecutable: boolean;
  onBindingAction: (target: DesktopAgentBindingTarget, clear: boolean) => Promise<void>;
  t: TFunction;
}) {
  const supported = Boolean(agent.executorName);
  const bindingTarget = agent.executorBindingTarget;
  const available = Boolean(status?.available);
  const installed = Boolean(status?.installed);
  const canLogin = enabled && supported && agent.executorLogin && installed && !available;
  let stateLabel = t("agentIntegration.notSupported");
  let stateColor = "default";
  if (supported && !enabled) {
    stateLabel = t("agentIntegration.executorDisabled");
  } else if (supported && (!status || !status.host_online)) {
    stateLabel = t("agentIntegration.executorConnecting");
    stateColor = "processing";
  } else if (supported && !installed) {
    stateLabel = t("agentIntegration.executorNotInstalled");
  } else if (supported && !available) {
    stateLabel = t("agentIntegration.executorLoginRequired");
    stateColor = "warning";
  } else if (available) {
    stateLabel = t("agentIntegration.executorAvailable");
    stateColor = "success";
  }
  const unavailableReason = enabled && status?.unavailable_reason
    ? !status.host_online
      ? t("agentIntegration.executorReasons.hostOffline")
      : !installed
        ? t("agentIntegration.executorReasons.notInstalled", { agent: agent.executorName || agent.name })
        : t(`agentIntegration.executorReasons.${agent.id}`, { defaultValue: status.unavailable_reason })
    : "";

  return (
    <Card className="agent-integration-card">
      <CardTitle name={agent.executorName || agent.name} icon={agent.icon}>
        <Tag color={stateColor}>{stateLabel}</Tag>
      </CardTitle>

      <Typography.Paragraph type="secondary" className="agent-integration-executor-requirement">
        {t(`agentIntegration.executorRequirements.${agent.id}`)}
      </Typography.Paragraph>

      {unavailableReason && <Alert type="info" showIcon message={unavailableReason} />}

      {supported && (
        <Space className="agent-integration-actions">
          {canLogin && (
            <Button
              type="primary"
              icon={<LoginOutlined />}
              loading={busyAction === `${agent.id}:login`}
              disabled={busyAction !== ""}
              onClick={() => void onLogin(agent.id)}
            >
              {t("agentIntegration.login")}
            </Button>
          )}
          {bindingTarget && canSelectExecutable && !installed && (
            <Button
              icon={<FolderOpenOutlined />}
              loading={busyAction === `binding:${bindingTarget}`}
              disabled={busyAction !== ""}
              onClick={() => void onBindingAction(bindingTarget, false)}
            >
              {t("agentIntegration.locateCLI")}
            </Button>
          )}
          {bindingTarget && bindingConfigured && canSelectExecutable && (
            <Button
              loading={busyAction === `binding:${bindingTarget}`}
              disabled={busyAction !== ""}
              onClick={() => void onBindingAction(bindingTarget, true)}
            >
              {t("agentIntegration.restoreAutoDetection")}
            </Button>
          )}
          <Button
            type={enabled ? "default" : "primary"}
            icon={enabled ? <DisconnectOutlined /> : <LinkOutlined />}
            loading={busyAction === `executor:${agent.id}:${enabled ? "disable" : "enable"}`}
            disabled={busyAction !== ""}
            onClick={() => void onPolicyAction(
              agent.id as DesktopExecutorProvider,
              enabled ? "disable" : "enable",
            )}
          >
            {t(enabled ? "agentIntegration.disable" : "agentIntegration.enable")}
          </Button>
        </Space>
      )}
    </Card>
  );
}

function CardTitle({ name, icon, children }: { name: string; icon: string; children: ReactNode }) {
  return (
    <div className="agent-integration-card-title">
      <div className="agent-integration-identity">
        <span className="agent-integration-logo" aria-hidden="true"><img alt="" src={icon} /></span>
        <Typography.Title level={4}>{name}</Typography.Title>
      </div>
      {children}
    </div>
  );
}
