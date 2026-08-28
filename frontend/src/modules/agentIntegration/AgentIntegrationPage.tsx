import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Alert, Button, Card, Input, Modal, Popover, Space, Spin, Switch, Tag, Typography, message } from "antd";
import {
  CheckCircleOutlined,
  DownOutlined,
  FolderOpenOutlined,
  InfoCircleFilled,
  LinkOutlined,
  LoginOutlined,
  QuestionCircleOutlined,
  ReloadOutlined,
  WarningOutlined,
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
    executorName: "CodeBuddy Code CLI",
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

const EXECUTOR_SYNC_ATTEMPTS = 6;
const EXECUTOR_SYNC_DELAY_MS = 500;
const EXTERNAL_CONFIGURATION_RECHECK_DELAYS_MS = [1_500, 4_000, 10_000];

type StatusMap = Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>>;
type ExecutorPolicyMap = Partial<Record<DesktopExecutorProvider, DesktopExecutorPolicy>>;
type BindingMap = Partial<Record<DesktopAgentBindingTarget, string>>;

export default function AgentIntegrationPage() {
  const { t } = useTranslation();
  const [statuses, setStatuses] = useState<StatusMap>({});
  const [executors, setExecutors] = useState<ChatExecutorDescriptor[]>([]);
  const [executorPolicies, setExecutorPolicies] = useState<ExecutorPolicyMap>({});
  const [bindings, setBindings] = useState<BindingMap>({});
  const [expandedAgent, setExpandedAgent] = useState<DesktopAgent | null>("codex");
  const [loading, setLoading] = useState(true);
  const [action, setAction] = useState("");
  const [error, setError] = useState("");
  const [bridgeUnavailable, setBridgeUnavailable] = useState(false);
  const [manualBindingTarget, setManualBindingTarget] = useState<DesktopAgentBindingTarget | null>(null);
  const [manualBindingPath, setManualBindingPath] = useState("");
  const [externalConfigurationAgent, setExternalConfigurationAgent] = useState<DesktopAgent | null>(null);
  const refreshVersion = useRef(0);

  const refresh = useCallback(async () => {
    const version = ++refreshVersion.current;
    const isCurrent = () => refreshVersion.current === version;
    setLoading(true);
    let nextError = "";
    let localBridgeUnavailable = false;
    let currentPolicies: ExecutorPolicyMap = {};
    try {
      const result = await agentIntegrationStatuses();
      if (!isCurrent()) return;
      if (result.ok) setStatuses(result.data);
      else localBridgeUnavailable = true;

      const policyResult = await executorIntegrationPolicies();
      if (!isCurrent()) return;
      if (policyResult.ok) {
        currentPolicies = policyResult.data;
        setExecutorPolicies(policyResult.data);
      }
      else localBridgeUnavailable = true;

      const bindingResult = await agentExecutableBindings();
      if (!isCurrent()) return;
      if (bindingResult.ok) setBindings(bindingResult.data);
      else localBridgeUnavailable = true;

      try {
        let values: ChatExecutorDescriptor[] = [];
        for (let attempt = 0; attempt < EXECUTOR_SYNC_ATTEMPTS; attempt += 1) {
          if (!isCurrent()) return;
          const response = await ConversationSettingsApi().listChatExecutors();
          if (!isCurrent()) return;
          values = response.data.data.executors;
          setExecutors(values);
          const waitingForHost = values.some((executor) =>
            executor.kind === "external" && !executor.host_online);
          const waitingForEnabledExecutor = values.some((executor) =>
            executor.kind === "external" &&
            currentPolicies[executor.id as DesktopExecutorProvider]?.enabled &&
            executor.installed && !executor.available);
          if (!waitingForHost && !waitingForEnabledExecutor) break;
          if (attempt + 1 < EXECUTOR_SYNC_ATTEMPTS) {
            await new Promise((resolve) => window.setTimeout(resolve, EXECUTOR_SYNC_DELAY_MS));
          }
        }
      } catch (executorError) {
        nextError = executorError instanceof Error ? executorError.message : String(executorError);
      }
    } finally {
      if (isCurrent()) {
        setError(nextError);
        setBridgeUnavailable(localBridgeUnavailable);
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    void refresh();
    return () => {
      refreshVersion.current += 1;
    };
  }, [refresh]);

  useEffect(() => {
    if (!externalConfigurationAgent) return undefined;
    const refreshAfterExternalAction = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    window.addEventListener("focus", refreshAfterExternalAction);
    document.addEventListener("visibilitychange", refreshAfterExternalAction);
    const timers = EXTERNAL_CONFIGURATION_RECHECK_DELAYS_MS.map((delay) =>
      window.setTimeout(() => void refresh(), delay));
    return () => {
      window.removeEventListener("focus", refreshAfterExternalAction);
      document.removeEventListener("visibilitychange", refreshAfterExternalAction);
      timers.forEach((timer) => window.clearTimeout(timer));
    };
  }, [externalConfigurationAgent, refresh]);

  useEffect(() => {
    if (externalConfigurationAgent &&
      statuses[externalConfigurationAgent]?.state !== "action_required") {
      setExternalConfigurationAgent(null);
    }
  }, [externalConfigurationAgent, statuses]);

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
    } else if (nextAction === "login") {
      const executorName = AGENTS.find((item) => item.id === agent)?.executorName || result.data.display_name;
      message.info(t("agentIntegration.loginStarted", { agent: executorName }));
    }
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

  const saveBinding = async (target: DesktopAgentBindingTarget, path?: string) => {
    setAction(`binding:${target}`);
    const result = path === undefined
      ? await clearAgentExecutable(target)
      : await bindAgentExecutable(target, path);
    setAction("");
    if (!result.ok) {
      setError(result.error instanceof Error ? result.error.message : result.reason);
      return;
    }
    message.success(t(path === undefined
      ? "agentIntegration.executableBindingCleared"
      : "agentIntegration.executableBindingSaved"));
    setManualBindingTarget(null);
    setManualBindingPath("");
    await refresh();
  };

  const runBindingAction = async (target: DesktopAgentBindingTarget, clear: boolean) => {
    if (clear) {
      await saveBinding(target);
      return;
    }
    if (!getDesktopPlatform()) {
      setManualBindingTarget(target);
      setManualBindingPath("");
      return;
    }
    const path = await selectExecutable(target);
    if (path) await saveBinding(target, path);
  };

  return (
    <div className="agent-integration-page">
      <div className="agent-integration-header">
        <div>
          <Typography.Title level={2}>{t("agentIntegration.title")}</Typography.Title>
          <Typography.Paragraph type="secondary">{t("agentIntegration.mergedDescription")}</Typography.Paragraph>
        </div>
        <Button icon={<ReloadOutlined />} onClick={() => void refresh()} loading={loading}>
          {t("common.refresh")}
        </Button>
      </div>

      {(error || bridgeUnavailable) && (
        <Alert
          type="error"
          showIcon
          closable
          message={t("agentIntegration.operationFailed")}
          description={(
            <span className="agent-integration-error">
              {bridgeUnavailable && <span>{t("agentIntegration.bridgeUnavailable")}</span>}
              {bridgeUnavailable && error && <br />}
              {error}
            </span>
          )}
          onClose={() => {
            setError("");
            setBridgeUnavailable(false);
          }}
        />
      )}

      <Spin spinning={loading}>
        <section className="agent-integration-section">
          <div className="agent-integration-grid">
            {AGENTS.map((agent) => (
              <AgentCard
                key={agent.id}
                agent={agent}
                mcpStatus={statuses[agent.id]}
                executorStatus={executors.find((item) => item.id === agent.id)}
                executorPolicy={executorPolicies[agent.id as DesktopExecutorProvider]}
                expanded={expandedAgent === agent.id}
                busyAction={action}
                bindings={bindings}
                onToggle={() => setExpandedAgent((current) => current === agent.id ? null : agent.id)}
                onMCPAction={runAction}
                onExecutorAction={runExecutorAction}
                onBindingAction={runBindingAction}
                onExternalConfigurationStarted={setExternalConfigurationAgent}
                onRefresh={refresh}
                t={t}
              />
            ))}
          </div>
        </section>
      </Spin>
      <Modal
        open={manualBindingTarget !== null}
        title={t("agentIntegration.executablePathTitle")}
        okText={t("common.save")}
        cancelText={t("common.cancel")}
        confirmLoading={Boolean(manualBindingTarget && action === `binding:${manualBindingTarget}`)}
        okButtonProps={{ disabled: manualBindingPath.trim() === "" }}
        onCancel={() => {
          setManualBindingTarget(null);
          setManualBindingPath("");
        }}
        onOk={() => {
          if (manualBindingTarget && manualBindingPath.trim()) {
            void saveBinding(manualBindingTarget, manualBindingPath.trim());
          }
        }}
      >
        <Typography.Paragraph type="secondary">
          {t("agentIntegration.executablePathDescription")}
        </Typography.Paragraph>
        <Input
          autoFocus
          value={manualBindingPath}
          placeholder={t("agentIntegration.executablePathPlaceholder")}
          onChange={(event) => setManualBindingPath(event.target.value)}
        />
      </Modal>
    </div>
  );
}

function AgentCard({
  agent,
  mcpStatus,
  executorStatus,
  executorPolicy,
  expanded,
  busyAction,
  bindings,
  onToggle,
  onMCPAction,
  onExecutorAction,
  onBindingAction,
  onExternalConfigurationStarted,
  onRefresh,
  t,
}: {
  agent: AgentDefinition;
  mcpStatus?: DesktopAgentIntegrationStatus;
  executorStatus?: ChatExecutorDescriptor;
  executorPolicy?: DesktopExecutorPolicy;
  expanded: boolean;
  busyAction: string;
  bindings: BindingMap;
  onToggle: () => void;
  onMCPAction: (agent: DesktopAgent, action: DesktopAgentIntegrationAction) => Promise<void>;
  onExecutorAction: (provider: DesktopExecutorProvider, action: DesktopExecutorPolicyAction) => Promise<void>;
  onBindingAction: (target: DesktopAgentBindingTarget, clear: boolean) => Promise<void>;
  onExternalConfigurationStarted: (agent: DesktopAgent) => void;
  onRefresh: () => Promise<void>;
  t: TFunction;
}) {
  const requirements = mcpStatus?.requirements || [];
  const mcpInstalled = requirements[0]
    ? requirements[0].satisfied
    : Boolean(mcpStatus && !["requirements_missing", "error"].includes(mcpStatus.state));
  const detected = mcpInstalled;
  const mcpClientName = t(`agentIntegration.mcpClients.${agent.id}`);

  return (
    <Card
      className={`agent-integration-card${expanded ? " is-expanded" : ""}`}
      data-testid={`agent-panel-${agent.id}`}
    >
      <button
        type="button"
        className="agent-integration-card-header"
        aria-expanded={expanded}
        onClick={onToggle}
      >
        <span className="agent-integration-identity">
          <span className="agent-integration-logo" aria-hidden="true"><img alt="" src={agent.icon} /></span>
          <span>
            <Typography.Title level={4}>{agent.name}</Typography.Title>
            <span className="agent-integration-card-summary">
              {detected
                ? t(agent.executorName
                  ? "agentIntegration.detectedSummary"
                  : "agentIntegration.singleDirectionSummary", {
                  agent: agent.executorName ? agent.name : mcpClientName,
                })
                : t("agentIntegration.missingSummary", { agent: agent.name })}
            </span>
          </span>
        </span>
        <Tag className={`agent-integration-install-tag ${detected ? "is-installed" : "is-missing"}`}>
          {t(detected ? "agentIntegration.installed" : "agentIntegration.notInstalled")}
        </Tag>
        <DownOutlined className="agent-integration-chevron" aria-hidden="true" />
      </button>

      {expanded && (
        <div className="agent-integration-card-detail">
          <AgentConfigurationFlow
            agent={agent}
            mcpStatus={mcpStatus}
            executorStatus={executorStatus}
            executorPolicy={executorPolicy}
            busyAction={busyAction}
            bindings={bindings}
            onMCPAction={onMCPAction}
            onExecutorAction={onExecutorAction}
            onBindingAction={onBindingAction}
            onExternalConfigurationStarted={onExternalConfigurationStarted}
            t={t}
          />
          <div className="agent-integration-card-footer">
            <span><InfoCircleFilled />{t("agentIntegration.guideFooter")}</span>
            <Button icon={<ReloadOutlined />} onClick={() => void onRefresh()}>
              {t("agentIntegration.checkAgain")}
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}

function AgentConfigurationFlow({
  agent,
  mcpStatus,
  executorStatus,
  executorPolicy,
  busyAction,
  bindings,
  onMCPAction,
  onExecutorAction,
  onBindingAction,
  onExternalConfigurationStarted,
  t,
}: {
  agent: AgentDefinition;
  mcpStatus?: DesktopAgentIntegrationStatus;
  executorStatus?: ChatExecutorDescriptor;
  executorPolicy?: DesktopExecutorPolicy;
  busyAction: string;
  bindings: BindingMap;
  onMCPAction: (agent: DesktopAgent, action: DesktopAgentIntegrationAction) => Promise<void>;
  onExecutorAction: (provider: DesktopExecutorProvider, action: DesktopExecutorPolicyAction) => Promise<void>;
  onBindingAction: (target: DesktopAgentBindingTarget, clear: boolean) => Promise<void>;
  onExternalConfigurationStarted: (agent: DesktopAgent) => void;
  t: TFunction;
}) {
  const mcpState = mcpStatus?.state || "requirements_missing";
  const mcpRequirements = mcpStatus?.requirements || [];
  const mcpPrepared = mcpRequirements.length > 0 && mcpRequirements.every((item) => item.satisfied);
  const mcpEnabled = mcpState === "enabled";
  const mcpCanToggle = mcpState === "ready" || mcpEnabled;
  const mcpBindingConfigured = Boolean(agent.mcpBindingTarget && bindings[agent.mcpBindingTarget]);
  const mcpInstallationMissing = mcpRequirements.length > 0 && !mcpRequirements[0].satisfied;

  const executorSupported = Boolean(agent.executorName);
  const executorEnabled = executorPolicy?.enabled ?? false;
  const executorInstalled = executorPolicy?.installed ?? Boolean(executorStatus?.installed);
  const executorReady = executorPolicy?.ready ?? Boolean(executorStatus?.available);
  const executorHostReady = Boolean(executorStatus?.host_online);
  const executorPrepared = executorSupported && executorInstalled && executorReady && executorHostReady;
  const executorBindingConfigured = Boolean(
    agent.executorBindingTarget && bindings[agent.executorBindingTarget],
  );
  const executorNeedsLogin = executorSupported && executorInstalled && !executorReady &&
    agent.executorLogin && executorAuthenticationRequired(executorPolicy?.unavailable_reason);
  const manualExecutableBinding = !getDesktopPlatform();
  const mcpClientName = t(`agentIntegration.mcpClients.${agent.id}`);
  const mcpGuideSteps = ["install", "connect", "verify"].map((step) =>
    t(`agentIntegration.guides.${agent.id}.mcp.${step}`));
  const executorGuideSteps = executorSupported
    ? ["install", "login", "enable"].map((step) =>
      t(`agentIntegration.guides.${agent.id}.executor.${step}`))
    : [];

  const bindingActions = (
    target: DesktopAgentBindingTarget | undefined,
    missing: boolean,
    configured: boolean,
  ) => {
    if (!target) return null;
    return (
      <>
        {missing && (
          <Button
            size="small"
            icon={<FolderOpenOutlined />}
            loading={busyAction === `binding:${target}`}
            disabled={busyAction !== ""}
            onClick={() => void onBindingAction(target, false)}
          >
            {t(manualExecutableBinding
              ? "agentIntegration.enterExecutablePath"
              : target.endsWith("-cli")
                ? "agentIntegration.locateCLI"
                : "agentIntegration.locateApplication")}
          </Button>
        )}
        {configured && (
          <Button
            size="small"
            disabled={busyAction !== ""}
            onClick={() => void onBindingAction(target, true)}
          >
            {t("agentIntegration.restoreAutoDetection")}
          </Button>
        )}
      </>
    );
  };

  const mcpActions = (
    <Space wrap size={8}>
      {!mcpPrepared && (
        <Button size="small" icon={<LinkOutlined />} href={agent.installURL} target="_blank">
          {t("agentIntegration.viewInstallGuide")}
        </Button>
      )}
      {mcpStatus?.action?.kind === "login" && (
        <Button
          size="small"
          type="primary"
          icon={<LoginOutlined />}
          loading={busyAction === `${agent.id}:login`}
          disabled={busyAction !== ""}
          onClick={() => void onMCPAction(agent.id, "login")}
        >
          {t("agentIntegration.login")}
        </Button>
      )}
      {mcpStatus?.action?.kind === "open_url" && mcpStatus.action.url && (
        <Button
          size="small"
          type="primary"
          icon={<LinkOutlined />}
          href={mcpStatus.action.url}
          target="_blank"
          onClick={() => onExternalConfigurationStarted(agent.id)}
        >
          {t("agentIntegration.continueInAgent", { agent: agent.name })}
        </Button>
      )}
      {bindingActions(agent.mcpBindingTarget, mcpInstallationMissing, mcpBindingConfigured)}
    </Space>
  );

  const executorActions = executorSupported ? (
    <Space wrap size={8}>
      {!executorInstalled && (
        <Button size="small" icon={<LinkOutlined />} href={agent.installURL} target="_blank">
          {t("agentIntegration.viewExecutorGuide")}
        </Button>
      )}
      {executorNeedsLogin && (
        <Button
          size="small"
          type="primary"
          icon={<LoginOutlined />}
          loading={busyAction === `${agent.id}:login`}
          disabled={busyAction !== ""}
          onClick={() => void onMCPAction(agent.id, "login")}
        >
          {t("agentIntegration.login")}
        </Button>
      )}
      {bindingActions(agent.executorBindingTarget, !executorInstalled, executorBindingConfigured)}
    </Space>
  ) : null;

  return (
    <div className="agent-integration-flow">
      <ConfigurationStage
        step={1}
        title={t("agentIntegration.clientStageTitle", { agent: mcpClientName })}
        ready={mcpPrepared}
        status={mcpPrepared
          ? t("agentIntegration.stageReady")
          : t("agentIntegration.stageActionRequired")}
        help={(
          <ConfigurationHelp
            title={t("agentIntegration.mcpGuideTitle", { agent: mcpClientName })}
            steps={mcpGuideSteps}
          />
        )}
      >
        <RequirementList
          requirements={mcpRequirements.map((requirement) => ({
            id: requirement.id,
            label: t(`agentIntegration.requirements.${requirement.id}.${requirement.satisfied ? "ready" : "missing"}`, {
              defaultValue: requirement.description,
            }),
            ready: requirement.satisfied,
          }))}
          emptyLabel={t("agentIntegration.waitingForDetection")}
        />
        {mcpActions}
      </ConfigurationStage>

      {executorSupported && (
        <ConfigurationStage
          step={2}
          title={t("agentIntegration.executorStageTitle", { agent: agent.executorName })}
          ready={executorPrepared}
          status={executorPrepared
            ? t("agentIntegration.stageReady")
            : t("agentIntegration.stageActionRequired")}
          help={(
            <ConfigurationHelp
              title={t("agentIntegration.executorGuideTitle", { agent: agent.executorName })}
              steps={executorGuideSteps}
              privacy={t("agentIntegration.sessionPrivacyNotice", { agent: agent.executorName })}
            />
          )}
        >
          <RequirementList
            requirements={[
              {
                id: "host",
                label: executorHostReady
                  ? t("agentIntegration.executorDetectionReady")
                  : t("agentIntegration.executorConnecting"),
                ready: executorHostReady,
              },
              {
                id: "installed",
                label: executorInstalled
                  ? t("agentIntegration.executorInstalled", { agent: agent.executorName })
                  : t("agentIntegration.executorMissing", { agent: agent.executorName }),
                ready: executorInstalled,
              },
              {
                id: "login",
                label: executorReady
                  ? t("agentIntegration.executorAccountReady", { agent: agent.executorName })
                  : executorAuthenticationRequired(executorPolicy?.unavailable_reason)
                    ? t("agentIntegration.executorLoginRequired", { agent: agent.executorName })
                    : t("agentIntegration.executorStatusCheckFailed"),
                ready: executorReady,
              },
            ]}
          />
          {executorActions}
        </ConfigurationStage>
      )}

      <ConfigurationStage
        step={executorSupported ? 3 : 2}
        title={t("agentIntegration.integrationStageTitle")}
        ready={mcpEnabled || executorEnabled}
        status={t("agentIntegration.chooseIntegrationMode")}
      >
        <div className="agent-integration-methods">
          <IntegrationMethod
            title={t("agentIntegration.mcpModeTitle", { agent: mcpClientName })}
            description={t("agentIntegration.mcpModeDescription", { agent: mcpClientName })}
            status={mcpCapabilityStatus(mcpState, t)}
            ready={mcpEnabled}
            disabled={!mcpEnabled && !mcpCanToggle}
            loading={busyAction === `${agent.id}:${mcpEnabled ? "disconnect" : "connect"}`}
            checked={mcpEnabled}
            onChange={(checked) => void onMCPAction(agent.id, checked ? "connect" : "disconnect")}
          />
          {executorSupported && (
            <IntegrationMethod
              title={t("agentIntegration.executorModeTitle", { agent: agent.executorName })}
              description={t("agentIntegration.executorModeDescription", { agent: agent.executorName })}
              status={executorEnabled
                ? t("agentIntegration.enabled")
                : executorPrepared
                  ? t("agentIntegration.notEnabled")
                  : t("agentIntegration.configurationIncomplete")}
              ready={executorEnabled}
              disabled={!executorEnabled && !executorPrepared}
              loading={busyAction === `executor:${agent.id}:${executorEnabled ? "disable" : "enable"}`}
              checked={executorEnabled}
              onChange={(checked) => void onExecutorAction(
                agent.id as DesktopExecutorProvider,
                checked ? "enable" : "disable",
              )}
            />
          )}
        </div>
        {!mcpCanToggle && !executorPrepared && (
          <div className="agent-integration-stage-hint">
            <InfoCircleFilled />
            {t("agentIntegration.completeConfigurationHint")}
          </div>
        )}
      </ConfigurationStage>
    </div>
  );
}

function ConfigurationStage({
  step,
  title,
  status,
  ready,
  help,
  children,
}: {
  step: number;
  title: string;
  status: string;
  ready: boolean;
  help?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className={`agent-integration-stage${ready ? " is-ready" : ""}`}>
      <div className="agent-integration-stage-rail">
        <span>{step}</span>
      </div>
      <div className="agent-integration-stage-copy">
        <div className="agent-integration-stage-heading">
          <div>
            <strong>{title}</strong>
            <span>{status}</span>
          </div>
          {help}
        </div>
        <div className="agent-integration-stage-content">{children}</div>
      </div>
    </section>
  );
}

function RequirementList({
  requirements,
  emptyLabel,
}: {
  requirements: Array<{ id: string; label: string; ready: boolean }>;
  emptyLabel?: string;
}) {
  const items = requirements.length
    ? requirements
    : [{ id: "pending", label: emptyLabel || "", ready: false }];
  return (
    <div className="agent-integration-requirements">
      {items.map((item) => (
        <span key={item.id} className={item.ready ? "is-ready" : "is-missing"}>
          {item.ready ? <CheckCircleOutlined /> : <WarningOutlined />}
          <span>{item.label}</span>
        </span>
      ))}
    </div>
  );
}

function IntegrationMethod({
  title,
  description,
  status,
  ready,
  disabled,
  loading,
  checked,
  onChange,
}: {
  title: string;
  description: string;
  status: string;
  ready: boolean;
  disabled: boolean;
  loading: boolean;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className={`agent-integration-method${ready ? " is-ready" : ""}`}>
      <div>
        <div className="agent-integration-method-title">
          <strong>{title}</strong>
          <span>{status}</span>
        </div>
        <p>{description}</p>
      </div>
      <Switch
        aria-label={title}
        checked={checked}
        disabled={disabled || loading}
        loading={loading}
        onChange={onChange}
      />
    </div>
  );
}

function ConfigurationHelp({
  title,
  steps,
  privacy,
}: {
  title: string;
  steps: string[];
  privacy?: string;
}) {
  return (
    <Popover
      trigger="click"
      placement="bottom"
      content={(
        <div className="agent-integration-help-content">
          <strong>{title}</strong>
          <ol>{steps.map((step, index) => (
            <li key={`${index}-${step}`}><GuideText text={step} /></li>
          ))}</ol>
          {privacy && <p><InfoCircleFilled /> {privacy}</p>}
        </div>
      )}
    >
      <Button
        type="text"
        shape="circle"
        size="small"
        icon={<QuestionCircleOutlined />}
        aria-label={`${title} help`}
      />
    </Popover>
  );
}

function GuideText({ text }: { text: string }) {
  return text.split(/(`[^`]+`)/g).map((part, index) => part.startsWith("`") && part.endsWith("`")
    ? <code key={`${index}-${part}`}>{part.slice(1, -1)}</code>
    : part);
}

function mcpCapabilityStatus(state: DesktopAgentIntegrationStatus["state"], t: TFunction) {
  if (state === "enabled") return t("agentIntegration.enabled");
  if (state === "action_required") return t("agentIntegration.awaitingConfirmation");
  if (state === "conflict" || state === "error") return t("agentIntegration.configurationIssue");
  return t("agentIntegration.notEnabled");
}

function executorAuthenticationRequired(reason = "") {
  const normalized = reason.toLowerCase();
  return normalized.includes("not signed in") || normalized.includes("not logged in") ||
    normalized.includes("login required") || normalized.includes("authentication required");
}
