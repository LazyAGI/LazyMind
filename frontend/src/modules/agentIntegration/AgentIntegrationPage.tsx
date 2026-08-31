import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Alert, Button, Card, Input, Modal, Popover, Space, Spin, Switch, Tag, Tooltip, Typography, message } from "antd";
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
  executorInstallURL?: string;
  executorName?: string;
  executorLoginMode?: "automatic" | "interactive";
  mcpBindingTarget?: DesktopAgentBindingTarget;
  executorBindingTarget?: DesktopAgentBindingTarget;
}

const AGENTS: AgentDefinition[] = [
  {
    id: "codex", name: "Codex", icon: "/assistant-icons/codex.png",
    installURL: "https://learn.chatgpt.com/docs/app",
    executorName: "Codex CLI", executorLoginMode: "automatic",
    mcpBindingTarget: "codex-desktop", executorBindingTarget: "codex-cli",
  },
  {
    id: "cursor", name: "Cursor", icon: "/assistant-icons/cursor.png",
    installURL: "https://cursor.com/downloads",
    executorInstallURL: "https://cursor.com/docs/cli/installation",
    executorName: "Cursor Agent CLI", executorLoginMode: "automatic",
    mcpBindingTarget: "cursor-desktop", executorBindingTarget: "cursor-cli",
  },
  {
    id: "workbuddy", name: "WorkBuddy", icon: "/assistant-icons/workbuddy.png",
    installURL: "https://www.workbuddy.cn",
    executorInstallURL: "https://www.codebuddy.ai/docs/cli/quickstart",
    executorName: "CodeBuddy Code CLI", executorLoginMode: "interactive",
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

function executorRuntimeState(
  status?: ChatExecutorDescriptor,
  policy?: DesktopExecutorPolicy,
) {
  const enabled = policy?.enabled ?? false;
  const installed = policy?.installed ?? Boolean(status?.installed);
  const ready = policy?.ready ?? Boolean(status?.available);
  const hostOnline = Boolean(status?.host_online);
  const hostInstalled = Boolean(status?.installed);
  const hostAvailable = Boolean(status?.available);
  const hostSynchronized = hostOnline && hostInstalled && (!enabled || hostAvailable);
  return {
    enabled, installed, ready, hostOnline, hostSynchronized,
    prepared: installed && ready && hostSynchronized,
  };
}

export default function AgentIntegrationPage() {
  const { t } = useTranslation();
  const [statuses, setStatuses] = useState<StatusMap>({});
  const [executors, setExecutors] = useState<ChatExecutorDescriptor[]>([]);
  const [executorPolicies, setExecutorPolicies] = useState<ExecutorPolicyMap>({});
  const [bindings, setBindings] = useState<BindingMap>({});
  const [expandedAgents, setExpandedAgents] = useState<Set<DesktopAgent>>(() => new Set(["codex"]));
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
            (!executor.installed || !executor.available));
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
      const definition = AGENTS.find((item) => item.id === agent);
      const executorName = definition?.executorName || result.data.display_name;
      message.info(t(definition?.executorLoginMode === "interactive"
        ? "agentIntegration.interactiveLoginStarted"
        : "agentIntegration.loginStarted", { agent: executorName }));
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
            {[0, 1].map((column) => (
              <div className="agent-integration-column" key={column}>
                {AGENTS.filter((_, index) => index % 2 === column).map((agent) => (
                  <AgentCard
                    key={agent.id}
                    agent={agent}
                    mcpStatus={statuses[agent.id]}
                    executorStatus={executors.find((item) => item.id === agent.id)}
                    executorPolicy={executorPolicies[agent.id as DesktopExecutorProvider]}
                    expanded={expandedAgents.has(agent.id)}
                    busyAction={action}
                    bindings={bindings}
                    onToggle={() => setExpandedAgents((current) => {
                      const next = new Set(current);
                      if (next.has(agent.id)) next.delete(agent.id);
                      else next.add(agent.id);
                      return next;
                    })}
                    onMCPAction={runAction}
                    onExecutorAction={runExecutorAction}
                    onBindingAction={runBindingAction}
                    onExternalConfigurationStarted={setExternalConfigurationAgent}
                    onRefresh={refresh}
                    t={t}
                  />
                ))}
              </div>
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
          placeholder={t(executablePathPlaceholderKey(manualBindingTarget))}
          onChange={(event) => setManualBindingPath(event.target.value)}
        />
      </Modal>
    </div>
  );
}

function executablePathPlaceholderKey(target: DesktopAgentBindingTarget | null): string {
  const desktopPlatform = getDesktopPlatform();
  const browserPlatform = typeof navigator === "undefined"
    ? ""
    : `${navigator.platform || ""} ${navigator.userAgent || ""}`.toLowerCase();
  const isMac = desktopPlatform === "darwin" || (!desktopPlatform && browserPlatform.includes("mac"));
  if (isMac) {
    return target === "codex-desktop"
      ? "agentIntegration.executablePathPlaceholderMacCodexDesktop"
      : target?.endsWith("-cli")
        ? "agentIntegration.executablePathPlaceholderMacCLI"
        : "agentIntegration.executablePathPlaceholderMacDesktop";
  }
  return target?.endsWith("-cli")
    ? "agentIntegration.executablePathPlaceholderWindowsCLI"
    : "agentIntegration.executablePathPlaceholderWindowsDesktop";
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
  const mcpState = mcpStatus?.state || "requirements_missing";
  const mcpPrepared = requirements.length > 0 && requirements.every((item) => item.satisfied) &&
    !["action_required", "conflict", "error"].includes(mcpState);
  const executorSupported = Boolean(agent.executorName);
  const executorRuntime = executorRuntimeState(executorStatus, executorPolicy);
  const executorEnabled = executorRuntime.enabled;
  const executorPrepared = executorSupported && executorRuntime.prepared;
  const detectionComplete = mcpPrepared && (!executorSupported || executorPrepared);
  const mcpEnabled = mcpState === "enabled";
  const mcpCanToggle = mcpState === "ready" || mcpEnabled;

  return (
    <Card
      className={`agent-integration-card${expanded ? " is-expanded" : ""}`}
      data-testid={`agent-panel-${agent.id}`}
    >
      <div className="agent-integration-card-header">
        <button
          type="button"
          className="agent-integration-card-toggle"
          aria-expanded={expanded}
          onClick={onToggle}
        >
          <span className="agent-integration-identity">
            <span className="agent-integration-logo" aria-hidden="true"><img alt="" src={agent.icon} /></span>
            <span>
              <Typography.Title level={4}>{agent.name}</Typography.Title>
              {mcpStatus?.version && (
                <span className="agent-integration-card-version">{mcpStatus.version}</span>
              )}
            </span>
          </span>
        </button>
        {!expanded && (
          detectionComplete ? (
            <CompactIntegrationControls
              agent={agent}
              mcpClientName={mcpClientName}
              mcpEnabled={mcpEnabled}
              mcpCanToggle={mcpCanToggle}
              executorEnabled={executorEnabled}
              executorPrepared={executorPrepared}
              busyAction={busyAction}
              onMCPAction={onMCPAction}
              onExecutorAction={onExecutorAction}
              t={t}
            />
          ) : (
            <CollapsedDetectionSummary
              agent={agent}
              mcpStatus={mcpStatus}
              executorStatus={executorStatus}
              executorPolicy={executorPolicy}
              t={t}
            />
          )
        )}
        <Tag className={`agent-integration-install-tag ${detected ? "is-installed" : "is-missing"}`}>
          {t(detected ? "agentIntegration.installed" : "agentIntegration.notInstalled")}
        </Tag>
        <button
          type="button"
          className="agent-integration-expand-button"
          aria-label={expanded ? t("common.collapse") : t("common.expand")}
          aria-expanded={expanded}
          onClick={onToggle}
        >
          <DownOutlined className="agent-integration-chevron" aria-hidden="true" />
        </button>
      </div>

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

function CollapsedDetectionSummary({
  agent,
  mcpStatus,
  executorStatus,
  executorPolicy,
  t,
}: {
  agent: AgentDefinition;
  mcpStatus?: DesktopAgentIntegrationStatus;
  executorStatus?: ChatExecutorDescriptor;
  executorPolicy?: DesktopExecutorPolicy;
  t: TFunction;
}) {
  const items = (mcpStatus?.requirements || []).map((requirement) => ({
    id: requirement.id,
    label: t(`agentIntegration.requirements.${requirement.id}.${requirement.satisfied ? "ready" : "missing"}`, {
      defaultValue: requirement.description,
    }),
    ready: requirement.satisfied,
  }));
  if (agent.executorName) {
    const runtime = executorRuntimeState(executorStatus, executorPolicy);
    const { installed, ready } = runtime;
    items.push({
      id: "executor-installed",
      label: t(installed ? "agentIntegration.compactCLIInstalled" : "agentIntegration.compactCLIMissing"),
      ready: installed,
    });
    if (installed) {
      items.push({
        id: "executor-login",
        label: t(ready ? "agentIntegration.compactCLILoggedIn" : "agentIntegration.compactCLINotLoggedIn"),
        ready,
      });
    }
    if (installed && ready) {
      items.push({
        id: "executor-host-sync",
        label: t(runtime.hostSynchronized
          ? "agentIntegration.compactHostSynchronized"
          : "agentIntegration.compactHostStale"),
        ready: runtime.hostSynchronized,
      });
    }
  }
  if (mcpStatus?.message && ["error", "conflict"].includes(mcpStatus.state)) {
    items.push({ id: "mcp-error", label: mcpStatus.message, ready: false });
  }
  if (!items.length) {
    items.push({ id: "pending", label: t("agentIntegration.waitingForDetection"), ready: false });
  }
  return (
    <div className="agent-integration-compact-detection" aria-label={t("agentIntegration.compactDetectionStatus")}>
      {items.map((item) => (
        <span key={item.id} className={item.ready ? "is-ready" : "is-missing"}>
          {item.ready ? <CheckCircleOutlined /> : <WarningOutlined />}
          <span>{item.label}</span>
        </span>
      ))}
    </div>
  );
}

function CompactIntegrationControls({
  agent,
  mcpClientName,
  mcpEnabled,
  mcpCanToggle,
  executorEnabled,
  executorPrepared,
  busyAction,
  onMCPAction,
  onExecutorAction,
  t,
}: {
  agent: AgentDefinition;
  mcpClientName: string;
  mcpEnabled: boolean;
  mcpCanToggle: boolean;
  executorEnabled: boolean;
  executorPrepared: boolean;
  busyAction: string;
  onMCPAction: (agent: DesktopAgent, action: DesktopAgentIntegrationAction) => Promise<void>;
  onExecutorAction: (provider: DesktopExecutorProvider, action: DesktopExecutorPolicyAction) => Promise<void>;
  t: TFunction;
}) {
  const methods = [{
    id: "mcp",
    title: t("agentIntegration.mcpModeTitle", { agent: mcpClientName }),
    description: t("agentIntegration.mcpModeDescription", { agent: mcpClientName }),
    checked: mcpEnabled,
    disabled: !mcpEnabled && !mcpCanToggle,
    loading: busyAction === `${agent.id}:${mcpEnabled ? "disconnect" : "connect"}`,
    onChange: (checked: boolean) => void onMCPAction(agent.id, checked ? "connect" : "disconnect"),
  }];
  if (agent.executorName) {
    methods.push({
      id: "executor",
      title: t("agentIntegration.executorModeTitle", { agent: agent.executorName }),
      description: t("agentIntegration.executorModeDescription", { agent: agent.executorName }),
      checked: executorEnabled,
      disabled: !executorEnabled && !executorPrepared,
      loading: busyAction === `executor:${agent.id}:${executorEnabled ? "disable" : "enable"}`,
      onChange: (checked: boolean) => void onExecutorAction(
        agent.id as DesktopExecutorProvider,
        checked ? "enable" : "disable",
      ),
    });
  }
  return (
    <div className="agent-integration-compact-controls">
      {methods.map((method) => (
        <Tooltip
          key={method.id}
          title={<><strong>{method.title}</strong><br />{method.description}</>}
          placement="bottom"
        >
          <div className={`agent-integration-compact-control${method.checked ? " is-enabled" : ""}`}>
            <span>{method.title}</span>
            <Switch
              size="small"
              aria-label={method.title}
              checked={method.checked}
              disabled={method.disabled || method.loading}
              loading={method.loading}
              onChange={method.onChange}
            />
          </div>
        </Tooltip>
      ))}
    </div>
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
  const executorRuntime = executorRuntimeState(executorStatus, executorPolicy);
  const executorEnabled = executorRuntime.enabled;
  const executorInstalled = executorRuntime.installed;
  const executorReady = executorRuntime.ready;
  const executorHostReady = executorRuntime.hostSynchronized;
  const executorBridgeState = executorPolicy?.bridge_state;
  const executorPrepared = executorSupported && executorInstalled && executorReady && executorHostReady;
  const executorBindingConfigured = Boolean(
    agent.executorBindingTarget && bindings[agent.executorBindingTarget],
  );
  const executorNeedsLogin = executorSupported && executorInstalled && !executorReady &&
    Boolean(agent.executorLoginMode) && executorAuthenticationRequired(executorPolicy?.unavailable_reason);
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
        <Button size="small" icon={<LinkOutlined />} href={agent.executorInstallURL || agent.installURL} target="_blank">
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
          {t(agent.executorLoginMode === "interactive"
            ? "agentIntegration.openLoginTerminal"
            : "agentIntegration.login")}
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
                  : executorBridgeState === "authentication_required"
                    ? t("agentIntegration.executorSessionExpired")
                    : executorBridgeState === "unavailable"
                      ? t("agentIntegration.executorBridgeUnavailable")
                      : executorRuntime.hostOnline
                        ? t("agentIntegration.executorHostStateStale")
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
                label: !executorInstalled
                  ? t("agentIntegration.executorWaitingForInstall", { agent: agent.executorName })
                  : executorReady
                    ? t("agentIntegration.executorAccountReady", { agent: agent.executorName })
                    : executorAuthenticationRequired(executorPolicy?.unavailable_reason)
                      ? t("agentIntegration.executorLoginRequired", { agent: agent.executorName })
                      : t("agentIntegration.executorStatusCheckFailed"),
                ready: executorReady,
              },
            ]}
          />
          {executorActions}
          {executorNeedsLogin && agent.executorLoginMode === "interactive" && (
            <div className="agent-integration-stage-hint">
              <InfoCircleFilled />
              <GuideText text={t("agentIntegration.interactiveLoginHint", { agent: agent.executorName })} />
            </div>
          )}
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
        {mcpStatus?.message && ["error", "conflict"].includes(mcpState) && (
          <div className="agent-integration-stage-hint is-error" role="alert">
            <WarningOutlined />
            {mcpStatus.message}
          </div>
        )}
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
