import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent, ReactNode } from "react";
import { Alert, Button, Empty, Input, Modal, Skeleton, Switch, Tabs, Tag, message } from "antd";
import {
  ApiOutlined,
  ArrowLeftOutlined,
  CheckCircleFilled,
  ClockCircleOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  ExperimentOutlined,
  InfoCircleOutlined,
  LinkOutlined,
  RobotOutlined,
  RightOutlined,
  SearchOutlined,
  SettingOutlined,
  TeamOutlined,
  ToolOutlined,
  UnorderedListOutlined,
  WarningFilled,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { AgentAppsAuth } from "@/components/auth";
import GroupManagement from "@/modules/admin/pages/group";
import UserManagement from "@/modules/admin/pages/user";
import AgentIntegrationPage from "@/modules/agentIntegration/AgentIntegrationPage";
import { TerminalConnectionPage } from "@/modules/channelGateway";
import { listChannelAccounts } from "@/modules/channelGateway/api";
import { setAllMcpServersEnabled } from "@/modules/memory/toolApi";
import { getFFmpegDependencyStatus } from "@/modules/modelProvider/api/systemDependencies";
import DependencyInstallSection from "@/modules/modelProvider/components/DependencyInstallSection";
import ToolManagementSection from "@/modules/modelProvider/components/ToolManagementSection";
import DefaultServicesPage from "@/modules/modelProvider/pages/DefaultServicesPage";
import ModelProvidersPage from "@/modules/modelProvider/pages/ModelProvidersPage";
import SettingsScheduleList from "@/modules/taskCenter/SettingsScheduleList";
import TaskEntryDefaults from "@/modules/taskCenter/TaskEntryDefaults";
import { fetchUserUiPreferences, patchUserUiPreferences } from "@/modules/user/uiPreferencesApi";
import { runtimeFeatures } from "@/runtime/features";
import { isDesktopRuntime, isLocalRuntime } from "@/runtime/mode";
import { setDeveloperModeActive } from "@/utils/developerMode";
import { setSensitiveWordFilterEnabled } from "@/utils/sensitiveWordFilter";
import MemoryCapabilitySettings from "./MemoryCapabilitySettings";
import KnowledgeDataSettings from "./KnowledgeDataSettings";
import KnowledgeToolSettings, { isKnowledgeToolView } from "./KnowledgeToolSettings";
import QuickModelSettings from "./QuickModelSettings";
import RecoverySettings from "./RecoverySettings";
import UserSkillWorkflowSettings, { type ResourceTab } from "./UserSkillWorkflowSettings";
import { resolveMcpReadinessStatus } from "./mcpReadinessStatus";
import { resolveModelNavigationStatus } from "./modelNavigationStatus";
import { isSettingsSectionVisible } from "./settingsSectionVisibility";
import {
  fetchSettingsOverview,
  runSettingsChecks,
  type SettingsCheckResult,
  type SettingsOverview,
  type SettingsOverviewSection,
} from "./api";
import "@/modules/knowledge/style.css";
import "@/modules/admin/index.scss";
import "@/modules/modelProvider/index.scss";
import "./index.scss";

type SectionID =
  | "overview"
  | "models"
  | "tasks"
  | "knowledge"
  | "memory"
  | "skills"
  | "system_tools"
  | "mcp"
  | "assistants"
  | "channels"
  | "diagnostics"
  | "organization"
  | "recovery"
  | "developer";
type MasterSetting = "task_center_enabled" | "schedules_enabled" | "skills_enabled" | "workflows_enabled" | "mcp_enabled" | "document_parsing_enabled";
type Translate = (key: string, options?: Record<string, unknown>) => string;

interface NavigationItem {
  id: SectionID;
  label: string;
  keywords: string;
  icon: ReactNode;
  status?: string;
}

interface NavigationGroup {
  title: string;
  items: NavigationItem[];
}

interface DiagnosticConnectionState {
  wechatConnected: number | null;
  wechatRunning: number | null;
  dependencyInstalled: boolean | null;
  dependencyMessage: string;
}

function controlCopy(t: Translate): Record<MasterSetting, { title: string; summary: string; section: SectionID }> {
  return {
    task_center_enabled: { title: t("settingsPage.controls.taskCenter.title"), summary: t("settingsPage.controls.taskCenter.summary"), section: "tasks" },
    schedules_enabled: { title: t("settingsPage.controls.schedules.title"), summary: t("settingsPage.controls.schedules.summary"), section: "tasks" },
    skills_enabled: { title: t("settingsPage.controls.skills.title"), summary: t("settingsPage.controls.skills.summary"), section: "skills" },
    workflows_enabled: { title: t("settingsPage.controls.workflows.title"), summary: t("settingsPage.controls.workflows.summary"), section: "skills" },
    mcp_enabled: { title: t("settingsPage.controls.mcp.title"), summary: t("settingsPage.controls.mcp.summary"), section: "mcp" },
    document_parsing_enabled: { title: t("settingsPage.controls.documentParsing.title"), summary: t("settingsPage.controls.documentParsing.summary"), section: "knowledge" },
  };
}

function isAdminRole(role?: string) {
  const value = (role || "").trim().toLowerCase();
  return value === "admin" || value === "system-admin" || value === "system_admin" || value.endsWith(".admin");
}

function baseNavigation(isAdmin: boolean, t: Translate): NavigationGroup[] {
  const groups: NavigationGroup[] = [
    {
      title: t("settingsPage.navGroups.gettingStarted"),
      items: [
        { id: "models", label: t("settingsPage.sections.models"), keywords: t("settingsPage.sectionKeywords.models"), icon: <ApiOutlined /> },
        { id: "overview", label: t("settingsPage.sections.overview"), keywords: t("settingsPage.sectionKeywords.overview"), icon: <SettingOutlined />, status: t("settingsPage.sectionStatus.synced") },
      ],
    },
    {
      title: t("settingsPage.navGroups.chatKnowledge"),
      items: [
        { id: "tasks", label: t("settingsPage.sections.tasks"), keywords: t("settingsPage.sectionKeywords.tasks"), icon: <RobotOutlined /> },
        { id: "knowledge", label: t("settingsPage.sections.knowledge"), keywords: t("settingsPage.sectionKeywords.knowledge"), icon: <DatabaseOutlined /> },
        { id: "memory", label: t("settingsPage.sections.memory"), keywords: t("settingsPage.sectionKeywords.memory"), icon: <ExperimentOutlined /> },
      ],
    },
    {
      title: t("settingsPage.navGroups.capabilities"),
      items: [
        { id: "skills", label: t("settingsPage.sections.skills"), keywords: t("settingsPage.sectionKeywords.skills"), icon: <RobotOutlined /> },
        { id: "system_tools", label: t("settingsPage.sections.systemTools"), keywords: t("settingsPage.sectionKeywords.systemTools"), icon: <ToolOutlined /> },
        { id: "mcp", label: t("settingsPage.sections.mcp"), keywords: t("settingsPage.sectionKeywords.mcp"), icon: <ToolOutlined /> },
        { id: "assistants", label: t("settingsPage.sections.assistants"), keywords: t("settingsPage.sectionKeywords.assistants"), icon: <RobotOutlined /> },
        { id: "channels", label: t("settingsPage.sections.channels"), keywords: t("settingsPage.sectionKeywords.channels"), icon: <LinkOutlined />, status: t("settingsPage.sectionStatus.connect") },
      ],
    },
    {
      title: t("settingsPage.navGroups.management"),
      items: [
        ...(isSettingsSectionVisible("organization", isAdmin) ? [{ id: "organization" as const, label: t("settingsPage.sections.organization"), keywords: t("settingsPage.sectionKeywords.organization"), icon: <TeamOutlined /> }] : []),
        { id: "recovery", label: t("settingsPage.sections.recovery"), keywords: t("settingsPage.sectionKeywords.recovery"), icon: <DeleteOutlined /> },
        { id: "diagnostics", label: t("settingsPage.sections.diagnostics"), keywords: t("settingsPage.sectionKeywords.diagnostics"), icon: <CheckCircleFilled /> },
        ...(isSettingsSectionVisible("developer", isAdmin) ? [{ id: "developer" as const, label: t("settingsPage.sections.developer"), keywords: t("settingsPage.sectionKeywords.developer"), icon: <CodeOutlined />, status: t("settingsPage.sectionStatus.activated") }] : []),
      ],
    },
  ];
  return groups.filter((group) => group.items.length > 0);
}

function sectionFallback(section: SectionID, t: Translate): SettingsOverviewSection {
  const item = baseNavigation(true, t).flatMap((group) => group.items).find((entry) => entry.id === section);
  return {
    id: section,
    title: item?.label || t("settingsPage.title"),
    route: "/agent/chat/home",
    counts: { total: 0, enabled: 0, verified: 0, runnable: 0, configured: 0 },
    status: "ready",
    detail: t("settingsPage.fallbackDetail"),
  };
}

function formatCount(section: SettingsOverviewSection, t: Translate) {
  if (section.id === "models") {
    return section.counts.configured
      ? t("settingsPage.counts.configuredItems", { count: section.counts.configured })
      : t("settingsPage.counts.pendingConfig");
  }
  if (section.id === "tasks") return t("settingsPage.counts.automationPlans", { count: section.counts.enabled });
  if (section.id === "skills") return t("settingsPage.counts.enabledResources", { count: section.counts.enabled });
  if (section.id === "mcp") return t("settingsPage.counts.runnableServices", { count: section.counts.runnable });
  return section.detail;
}

export default function SettingsPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const isAdmin = isAdminRole(AgentAppsAuth.getUserInfo()?.role);
  const hasLocalDependencies = isLocalRuntime() || isDesktopRuntime();
  const navigationGroups = useMemo(() => baseNavigation(isAdmin, t), [isAdmin, t, i18n.language]);
  const navigationItems = useMemo(() => navigationGroups.flatMap((group) => group.items), [navigationGroups]);
  const controls = useMemo(() => controlCopy(t), [t, i18n.language]);
  const candidate = searchParams.get("section");
  const section: SectionID = candidate === "defaults"
    ? "tasks"
    : navigationItems.some((item) => item.id === candidate)
      ? candidate as SectionID
      : "overview";
  const knowledgeToolCandidate = searchParams.get("tool");
  const knowledgeToolView = section === "knowledge" && isKnowledgeToolView(knowledgeToolCandidate)
    ? knowledgeToolCandidate
    : null;
  const headingRef = useRef<HTMLHeadingElement>(null);
  const modelProviderTabRef = useRef<HTMLButtonElement>(null);
  const latestRequest = useRef(0);
  const [overview, setOverview] = useState<SettingsOverview | null>(null);
  const [developerActive, setDeveloperActive] = useState(false);
  const [performanceStatsEnabled, setPerformanceStatsEnabled] = useState(false);
  const [sensitiveWordFilterEnabled, setSensitiveWordFilterEnabledState] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [saving, setSaving] = useState<MasterSetting | "developer" | "performance_stats" | "sensitive_word_filter" | null>(null);
  const [checks, setChecks] = useState<SettingsCheckResult[] | null>(null);
  const [checking, setChecking] = useState(false);
  const [diagnosticLoading, setDiagnosticLoading] = useState(false);
  const [lastCheckedAt, setLastCheckedAt] = useState<string | null>(null);
  const [diagnosticConnections, setDiagnosticConnections] = useState<DiagnosticConnectionState>({
    wechatConnected: null,
    wechatRunning: null,
    dependencyInstalled: hasLocalDependencies ? null : true,
    dependencyMessage: hasLocalDependencies ? t("settingsPage.diagnostics.readingDeps") : t("settingsPage.diagnostics.cloudDeps"),
  });
  const [keyword, setKeyword] = useState("");
  const modelView = searchParams.get("view") === "providers" ? "providers" : "defaults";
  const taskView = candidate !== "defaults" && searchParams.get("view") === "tasks" ? "tasks" : "conversation";
  const [organizationView, setOrganizationView] = useState<"users" | "groups">("users");
  const [mcpRefreshToken, setMcpRefreshToken] = useState(0);

  const modelSection = overview?.sections.find((item) => item.id === "models");
  const modelNavigationStatus = resolveModelNavigationStatus(
    loading || loadError ? undefined : modelSection?.effective_enabled,
  );
  const navigationGroupsWithStatus = useMemo(() => navigationGroups.map((group) => ({
    ...group,
    items: group.items.map((item) => item.id === "models"
      ? {
          ...item,
          status: modelNavigationStatus === "ready"
            ? t("settingsPage.sectionStatus.ready")
            : modelNavigationStatus === "pending"
              ? t("settingsPage.counts.pendingConfig")
              : undefined,
        }
      : item),
  })), [modelNavigationStatus, navigationGroups, t]);

  const refresh = useCallback(async () => {
    const requestID = ++latestRequest.current;
    setLoading(true);
    setLoadError(false);
    try {
      const [nextOverview, preferences] = await Promise.all([fetchSettingsOverview(), fetchUserUiPreferences()]);
      if (requestID !== latestRequest.current) return;
      setOverview(nextOverview);
      setDeveloperActive(preferences.developer_mode_active);
      setPerformanceStatsEnabled(Boolean(preferences.performance_stats_enabled));
      const sensitiveWordFilterEnabled = Boolean(preferences.sensitive_word_filter_enabled);
      setSensitiveWordFilterEnabled(sensitiveWordFilterEnabled);
      setSensitiveWordFilterEnabledState(sensitiveWordFilterEnabled);
    } catch {
      if (requestID !== latestRequest.current) return;
      setLoadError(true);
    } finally {
      if (requestID === latestRequest.current) setLoading(false);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => { headingRef.current?.focus(); }, [section]);

  const filteredGroups = useMemo(() => {
    const query = keyword.trim().toLowerCase();
    if (!query) return navigationGroupsWithStatus;
    return navigationGroupsWithStatus.map((group) => ({
      ...group,
      items: group.items.filter((item) => `${item.label} ${item.keywords}`.toLowerCase().includes(query)),
    })).filter((group) => group.items.length > 0);
  }, [keyword, navigationGroupsWithStatus]);

  const selectSection = (next: SectionID) => setSearchParams({ section: next });
  const selectModelView = (next: "defaults" | "providers") => {
    setSearchParams(next === "providers"
      ? { section: "models", view: "providers" }
      : { section: "models" });
  };
  const selectTaskView = (next: "conversation" | "tasks") => {
    setSearchParams(next === "tasks"
      ? { section: "tasks", view: "tasks" }
      : { section: "tasks" });
  };
  const selectedSection = overview?.sections.find((item) => item.id === section) || sectionFallback(section, t);

  const syncOverview = useCallback(async () => {
    try {
      setOverview(await fetchSettingsOverview());
    } catch {
      // The detail view owns its visible state; the next page refresh retries the aggregate sync.
    }
  }, []);

  const refreshDiagnosticConnections = useCallback(async () => {
    setDiagnosticLoading(true);
    const [wechatResult, dependencyResult] = await Promise.allSettled([
      listChannelAccounts("wechat"),
      hasLocalDependencies ? getFFmpegDependencyStatus() : Promise.resolve(null),
    ]);
    setDiagnosticConnections((current) => {
      const next = { ...current };
      if (wechatResult.status === "fulfilled") {
        next.wechatConnected = wechatResult.value.items.filter((account) => account.status === "connected").length;
        next.wechatRunning = wechatResult.value.items.filter((account) => account.status === "connected" && account.runtime_status === "running").length;
      } else {
        next.wechatConnected = null;
        next.wechatRunning = null;
      }
      if (!hasLocalDependencies) {
        next.dependencyInstalled = true;
        next.dependencyMessage = t("settingsPage.diagnostics.cloudDeps");
      } else if (dependencyResult.status === "fulfilled" && dependencyResult.value) {
        next.dependencyInstalled = dependencyResult.value.installed;
        next.dependencyMessage = dependencyResult.value.message || (
          dependencyResult.value.installed
            ? t("settingsPage.diagnostics.ffmpegAvailable")
            : t("settingsPage.diagnostics.ffmpegMissing")
        );
      } else {
        next.dependencyInstalled = null;
        next.dependencyMessage = t("settingsPage.diagnostics.depsReadFailed");
      }
      return next;
    });
    setDiagnosticLoading(false);
  }, [hasLocalDependencies, t]);

  useEffect(() => {
    if (section === "diagnostics") void refreshDiagnosticConnections();
  }, [refreshDiagnosticConnections, section]);

  const requestMasterChange = (key: MasterSetting, enabled: boolean, enabledCountOverride?: number) => {
    const target = controls[key];
    const sectionInfo = overview?.sections.find((item) => item.id === target.section);
    const enabledCount = enabledCountOverride ?? sectionInfo?.counts.enabled ?? 0;
    const resourceLabel = key === "schedules_enabled"
      ? t("settingsPage.confirm.enabledSchedules", { count: enabledCount })
      : key === "task_center_enabled"
        ? t("settingsPage.confirm.subtaskSettingsKept")
      : key === "document_parsing_enabled"
        ? t("settingsPage.confirm.parsingKept")
      : key === "skills_enabled"
        ? t("settingsPage.confirm.enabledSkills", { count: enabledCount })
        : key === "workflows_enabled"
          ? enabledCountOverride == null
            ? t("settingsPage.confirm.availableWorkflows")
            : t("settingsPage.confirm.enabledWorkflows", { count: enabledCountOverride })
          : t("settingsPage.confirm.enabledServices", { count: enabledCount });
    const isResourceBulkChange = key === "skills_enabled" || key === "workflows_enabled" || key === "mcp_enabled";
    const stateLabel = enabled ? t("settingsPage.confirm.enableState") : t("settingsPage.confirm.disableState");
    const resourceChangeText = key === "mcp_enabled"
      ? t("settingsPage.confirm.mcpBulk", { resource: resourceLabel, state: stateLabel })
      : t("settingsPage.confirm.resourceBulk", { resource: resourceLabel, title: target.title, state: stateLabel });
    Modal.confirm({
      title: t("settingsPage.confirm.title", {
        action: enabled ? t("settingsPage.enable") : t("settingsPage.disable"),
        title: target.title,
      }),
      content: <div className="settings-ref-confirm">
        <p>{t("settingsPage.confirm.effectiveNow", { summary: target.summary })}</p>
        <p>{isResourceBulkChange
          ? resourceChangeText
          : key === "document_parsing_enabled" || key === "task_center_enabled"
            ? resourceLabel
            : t("settingsPage.confirm.keepChildState", { resource: resourceLabel })}</p>
        <p>{key === "task_center_enabled"
          ? t("settingsPage.confirm.subtaskConsequence")
          : key === "schedules_enabled"
            ? t("settingsPage.confirm.scheduleConsequence")
          : key === "document_parsing_enabled"
            ? t("settingsPage.confirm.parsingConsequence")
            : key === "mcp_enabled"
              ? t("settingsPage.confirm.mcpConsequence")
              : isResourceBulkChange
                ? t("settingsPage.confirm.resourceConsequence", { title: target.title })
                : t("settingsPage.confirm.defaultConsequence")}</p>
      </div>,
      okText: enabled ? t("settingsPage.confirmEnable") : t("settingsPage.confirmDisable"),
      cancelText: t("settingsPage.cancel"),
      okButtonProps: enabled ? undefined : { danger: true },
      onOk: async () => {
        setSaving(key);
        try {
          if (key === "mcp_enabled") {
            const result = await setAllMcpServersEnabled(enabled);
            setMcpRefreshToken((value) => value + 1);
            await refresh();
            if (enabled && result.skippedUnverifiedCount > 0) {
              message.warning(t("settingsPage.confirm.mcpEnabledToast", {
                updated: result.updatedCount,
                skipped: result.skippedUnverifiedCount,
              }));
            } else {
              message.success(t("settingsPage.confirm.mcpToggledToast", {
                state: stateLabel,
                count: result.updatedCount,
              }));
            }
          } else {
            await patchUserUiPreferences({ [key]: enabled });
          }
          if (key !== "mcp_enabled" && isResourceBulkChange) {
            await syncOverview();
          } else if (key !== "mcp_enabled") {
            await refresh();
          }
          if (key !== "mcp_enabled") message.success(t("settingsPage.saved"));
        } catch {
          message.error(t("settingsPage.saveFailed"));
        } finally {
          setSaving(null);
        }
      },
    });
  };

  const requestDeveloperChange = (enabled: boolean) => {
    const confirmationKey = runtimeFeatures.hideEvo
      ? enabled
        ? "settingsPage.confirm.developerEnableContentWithoutEvo"
        : "settingsPage.confirm.developerDisableContentWithoutEvo"
      : enabled
        ? "settingsPage.confirm.developerEnableContent"
        : "settingsPage.confirm.developerDisableContent";
    Modal.confirm({
      title: t("settingsPage.confirm.developerTitle", {
        action: enabled ? t("settingsPage.enable") : t("settingsPage.disable"),
      }),
      content: t(confirmationKey),
      okText: enabled ? t("settingsPage.confirmEnable") : t("settingsPage.confirmDisable"),
      cancelText: t("settingsPage.cancel"),
      okButtonProps: enabled ? undefined : { danger: true },
      onOk: async () => {
        setSaving("developer");
        try {
          await patchUserUiPreferences({ developer_mode_active: enabled });
          setDeveloperModeActive(enabled);
          await refresh();
          message.success(t("settingsPage.saved"));
        } catch {
          message.error(t("settingsPage.saveFailed"));
        } finally {
          setSaving(null);
        }
      },
    });
  };

  const handleCheckAll = async () => {
    setChecking(true);
    try {
      const response = await runSettingsChecks();
      setChecks(response.results);
      setLastCheckedAt(response.finished_at);
      await Promise.all([syncOverview(), refreshDiagnosticConnections()]);
    } catch {
      message.error(t("settingsPage.checkFailed"));
    } finally {
      setChecking(false);
    }
  };

  const switchControl = (key: MasterSetting, ariaLabel = controls[key].title, descriptionID?: string) => {
    return (
      <span>
        <Switch
          className="settings-ref-switch"
          checked={Boolean(overview?.controls[key])}
          loading={saving === key}
          disabled={saving !== null}
          onChange={(checked: boolean) => requestMasterChange(key, checked)}
          aria-label={ariaLabel}
          aria-describedby={descriptionID}
        />
      </span>
    );
  };

  const dashboardRow = (module: string, title: string, description: string, control: ReactNode) => (
    <div className="settings-dashboard-config-row" key={`${module}-${title}`}>
      <div className="settings-dashboard-copy"><span>{module}</span><strong>{title}</strong><p>{description}</p></div>
      <div className="settings-dashboard-control">{control}</div>
    </div>
  );

  const dashboardCard = (target: SectionID, icon: ReactNode, title: string, description: string, rows: ReactNode[]) => (
    <section className="settings-dashboard-card" key={target}>
      <div className="settings-dashboard-card-head"><span className="settings-section-icon">{icon}</span><div><h2>{title}</h2><p>{description}</p></div></div>
      <div className="settings-dashboard-card-body">{rows}</div>
      <div className="settings-dashboard-card-foot">
        <button type="button" onClick={() => selectSection(target)} aria-label={t("settingsPage.goToDetailAria", { title })}>
          {t("settingsPage.goToDetail")} <RightOutlined />
        </button>
      </div>
    </section>
  );

  const renderDashboard = () => {
    const sections = overview?.sections || [];
    const get = (id: string) => sections.find((item) => item.id === id) || sectionFallback(id as SectionID, t);
    const tasks = get("tasks");
    const skills = get("skills");
    const mcp = get("mcp");
    return <section className="settings-dashboard">
      <div className="settings-page-heading">
        <div>
          <h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.overview.title")}</h1>
          <p>{t("settingsPage.overview.description")}</p>
        </div>
        <Tag className="settings-sync-tag">{t("settingsPage.syncRealtime")}</Tag>
      </div>
      {overview?.issues.length ? <div className="settings-ref-issues" role="status" aria-live="polite">{overview.issues.map((issue) => <Alert key={issue.id} type={issue.severity === "warning" ? "warning" : "info"} showIcon message={issue.message} action={<Button type="link" size="small" onClick={() => selectSection(issue.section as SectionID)}>{t("settingsPage.view")}</Button>} />)}</div> : null}
      <div className="settings-dashboard-grid">
        {dashboardCard("models", <ApiOutlined />, t("settingsPage.sections.models"), t("settingsPage.overview.modelsDesc"), [
          <QuickModelSettings canConfigureEmbedding={isAdmin} key="quick-models" onSaved={syncOverview} />,
        ])}
        {dashboardCard("knowledge", <DatabaseOutlined />, t("settingsPage.sections.knowledge"), t("settingsPage.overview.knowledgeDesc"), [
          dashboardRow(t("settingsPage.sections.knowledge"), t("settingsPage.controls.documentParsing.title"), t("settingsPage.overview.documentParsingDesc"), switchControl("document_parsing_enabled")),
          dashboardRow(t("settingsPage.sections.knowledge"), t("settingsPage.overview.knowledgeRetrieval"), t("settingsPage.overview.knowledgeRetrievalDesc"), <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("knowledge")}>{t("settingsPage.quickConfig")}</Button>),
        ])}
        {dashboardCard("memory", <ExperimentOutlined />, t("settingsPage.sections.memory"), t("settingsPage.overview.memoryDesc"), [
          dashboardRow(t("settingsPage.sections.memory"), t("settingsPage.overview.memoryEdit"), t("settingsPage.overview.memoryEditDesc"), <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("memory")}>{t("settingsPage.quickConfig")}</Button>),
        ])}
        {dashboardCard("system_tools", <ToolOutlined />, t("settingsPage.sections.systemTools"), t("settingsPage.overview.systemToolsDesc"), [
          dashboardRow(t("settingsPage.sections.systemTools"), t("settingsPage.overview.builtinTools"), t("settingsPage.overview.builtinToolsDesc"), <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("system_tools")}>{t("settingsPage.manageTools")}</Button>),
          dashboardRow(t("settingsPage.sections.systemTools"), t("settingsPage.overview.localDeps"), t("settingsPage.overview.localDepsDesc"), <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("system_tools")}>{hasLocalDependencies ? t("settingsPage.checkDeps") : t("settingsPage.cloudHosted")}</Button>),
        ])}
        {dashboardCard("mcp", <ToolOutlined />, t("settingsPage.sections.mcp"), t("settingsPage.overview.mcpDesc"), [
          dashboardRow(t("settingsPage.sections.mcp"), t("settingsPage.overview.mcpMaster"), t("settingsPage.overview.mcpMasterDesc"), switchControl("mcp_enabled")),
          dashboardRow(t("settingsPage.sections.mcp"), t("settingsPage.overview.verifiedServices"), t("settingsPage.overview.verifiedServicesDesc", { verified: mcp.counts.verified, runnable: mcp.counts.runnable }), <Tag className="settings-status-tag">{formatCount(mcp, t)}</Tag>),
        ])}
        {dashboardCard("assistants", <RobotOutlined />, t("settingsPage.sections.assistants"), t("settingsPage.overview.assistantsDesc"), [
          dashboardRow(t("settingsPage.sections.assistants"), t("settingsPage.overview.assistantMcp"), t("settingsPage.overview.assistantMcpDesc"), <Tag className="settings-status-tag">MCP</Tag>),
          dashboardRow(t("settingsPage.sections.assistants"), t("settingsPage.overview.assistantExecutors"), t("settingsPage.overview.assistantExecutorsDesc"), <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("assistants")}>{t("settingsPage.manageAssistants")}</Button>),
        ])}
        {dashboardCard("skills", <RobotOutlined />, t("settingsPage.sections.skills"), t("settingsPage.overview.skillsDesc"), [
          dashboardRow(t("settingsPage.sections.skills"), t("settingsPage.controls.skills.title"), t("settingsPage.overview.mySkillsDesc"), switchControl("skills_enabled")),
          dashboardRow(t("settingsPage.sections.skills"), t("settingsPage.controls.workflows.title"), t("settingsPage.overview.myWorkflowsDesc"), switchControl("workflows_enabled")),
          dashboardRow(t("settingsPage.sections.skills"), t("settingsPage.overview.enabledResources"), t("settingsPage.overview.enabledResourcesDesc", { count: skills.counts.enabled }), <Tag className="settings-status-tag">{t("settingsPage.separatelyControlled")}</Tag>),
        ])}
        {dashboardCard("tasks", <UnorderedListOutlined />, t("settingsPage.sections.tasks"), t("settingsPage.overview.tasksDesc"), [
          dashboardRow(t("settingsPage.sections.tasks"), t("settingsPage.tasks.enableSubtasks"), t("settingsPage.tasks.enableSubtasksDesc"), switchControl("task_center_enabled", t("settingsPage.tasks.enableSubtasks"))),
          dashboardRow(t("settingsPage.sections.tasks"), t("settingsPage.tasks.enableWorkflows"), t("settingsPage.tasks.enableWorkflowsDesc"), switchControl("workflows_enabled", t("settingsPage.tasks.enableWorkflows"))),
          dashboardRow(t("settingsPage.sections.tasks"), t("settingsPage.tasks.enableSchedules"), t("settingsPage.tasks.enableSchedulesDesc", { count: tasks.counts.enabled }), switchControl("schedules_enabled", t("settingsPage.tasks.enableSchedules"))),
        ])}
        {dashboardCard("diagnostics", <CheckCircleFilled />, t("settingsPage.sections.diagnostics"), t("settingsPage.overview.diagnosticsDesc"), [
          dashboardRow(t("settingsPage.sections.diagnostics"), t("settingsPage.checkAll"), t("settingsPage.overview.checkAllDesc"), <Button size="small" loading={checking} onClick={handleCheckAll}>{t("settingsPage.check")}</Button>),
          dashboardRow(t("settingsPage.sections.diagnostics"), t("settingsPage.overview.recentResults"), checks ? t("settingsPage.overview.recentResultsReady", { count: checks.length }) : t("settingsPage.overview.recentResultsEmpty"), <Tag className="settings-status-tag">{t("settingsPage.viewable")}</Tag>),
        ])}
        {isSettingsSectionVisible("developer", isAdmin) ? dashboardCard("developer", <CodeOutlined />, t("settingsPage.sections.developer"), t("settingsPage.overview.developerDesc"), [
          dashboardRow(t("settingsPage.sections.developer"), t("settingsPage.overview.enableDeveloper"), t("settingsPage.overview.enableDeveloperDesc"), <Switch className="settings-ref-switch" checked={developerActive} loading={saving === "developer"} disabled={saving !== null} onChange={requestDeveloperChange} aria-label={t("settingsPage.overview.enableDeveloper")} />),
          dashboardRow(t("settingsPage.sections.developer"), t("settingsPage.overview.internalDebug"), t("settingsPage.overview.internalDebugDesc"), <Tag className="settings-status-tag">{developerActive ? t("settingsPage.sectionStatus.activated") : t("settingsPage.sectionStatus.notActivated")}</Tag>),
        ]) : null}
      </div>
      {checks ? <CheckResults checks={checks} onLocate={selectSection} /> : null}
    </section>;
  };

  const integratedHeader = (title: string, description: string, action?: ReactNode) => (
    <header className="settings-detail-header">
      <div><h1 ref={headingRef} tabIndex={-1}>{title}</h1><p>{description}</p></div>
      {action}
    </header>
  );

  const masterControl = (key: "mcp_enabled", title = t("settingsPage.masterSwitch", { title: controls[key].title })) => {
    const sectionInfo = overview?.sections.find((item) => item.id === controls[key].section);
    const readinessStatus = resolveMcpReadinessStatus(sectionInfo, overview?.issues);
    const statusText = !overview?.controls[key]
      ? t("settingsPage.master.paused")
      : readinessStatus === "available"
        ? t("settingsPage.master.available")
        : readinessStatus === "needs_authorization"
          ? t("settingsPage.master.waitAuthorize")
          : t("settingsPage.master.waitVerify");
    return <section className="settings-integrated-master" aria-label={title}>
      <div><strong>{title}</strong><p>{controls[key].summary}</p></div>
      <div className="settings-integrated-master-action"><Tag className="settings-status-tag">{statusText}</Tag>{switchControl(key)}</div>
    </section>;
  };

  const taskControl = (
    key: "task_center_enabled" | "workflows_enabled" | "schedules_enabled",
    icon: ReactNode,
    title: string,
    description: string,
  ) => {
    const descriptionID = `settings-task-control-${key}-description`;
    return <div className="settings-task-control" key={key}>
      <span className="settings-task-control-icon" aria-hidden="true">{icon}</span>
      <div className="settings-task-control-copy">
        <strong>{title}</strong>
        <p id={descriptionID}>{description}</p>
      </div>
      <div className="settings-task-control-action">{switchControl(key, title, descriptionID)}</div>
    </div>;
  };

  const integratedSurface = (content: ReactNode, className = "") => (
    <div className={`settings-integrated-surface ${className}`.trim()}>{content}</div>
  );

  const renderDiagnostics = () => {
    const models = overview?.sections.find((item) => item.id === "models") || sectionFallback("models", t);
    const mcp = overview?.sections.find((item) => item.id === "mcp") || sectionFallback("mcp", t);
    const lastCheckedLabel = lastCheckedAt
      ? new Date(lastCheckedAt).toLocaleString(i18n.language === "zh-CN" ? "zh-CN" : "en-US", { hour12: false })
      : t("settingsPage.diagnostics.neverChecked");
    const wechatStatus = diagnosticConnections.wechatConnected == null
      ? t("settingsPage.diagnostics.readFailed")
      : diagnosticConnections.wechatConnected > 0
        ? t("settingsPage.diagnostics.connected")
        : t("settingsPage.diagnostics.notConnected");
    const dependencyStatus = diagnosticConnections.dependencyInstalled == null
      ? t("settingsPage.diagnostics.readFailed")
      : diagnosticConnections.dependencyInstalled
        ? hasLocalDependencies ? t("settingsPage.diagnostics.configured") : t("settingsPage.cloudHosted")
        : t("settingsPage.diagnostics.pendingConfig");
    const mcpReadinessStatus = resolveMcpReadinessStatus(mcp, overview?.issues);
    const rows = [
      {
        id: "models",
        title: t("settingsPage.diagnostics.modelProviders"),
        description: models.counts.configured > 0
          ? t("settingsPage.diagnostics.modelsConfigured", { count: models.counts.configured })
          : t("settingsPage.diagnostics.modelsEmpty"),
        status: models.counts.configured > 0 ? t("settingsPage.diagnostics.configured") : t("settingsPage.diagnostics.pendingConfig"),
        tone: models.counts.configured > 0 ? "success" : "warning",
        action: t("settingsPage.diagnostics.viewConnection"),
        onClick: () => selectSection("models"),
      },
      {
        id: "mcp",
        title: t("settingsPage.sections.mcp"),
        description: t("settingsPage.diagnostics.mcpVerified", {
          verified: mcp.counts.verified,
          total: mcp.counts.total,
          runnable: mcp.counts.runnable,
        }),
        status: mcp.counts.total === 0
          ? t("settingsPage.diagnostics.notConnected")
          : mcpReadinessStatus === "needs_verification"
            ? t("settingsPage.diagnostics.pendingVerify")
            : mcpReadinessStatus === "needs_authorization"
              ? t("settingsPage.diagnostics.pendingAuthorize")
              : t("settingsPage.diagnostics.runnable"),
        tone: mcp.counts.total === 0
          ? "neutral"
          : mcpReadinessStatus === "available" ? "success" : "warning",
        action: t("settingsPage.diagnostics.viewServices"),
        onClick: () => selectSection("mcp"),
      },
      {
        id: "channels",
        title: t("settingsPage.diagnostics.wechatChannel"),
        description: diagnosticConnections.wechatConnected == null
          ? t("settingsPage.diagnostics.wechatReadFailed")
          : diagnosticConnections.wechatConnected > 0
            ? t("settingsPage.diagnostics.wechatConnected", {
              connected: diagnosticConnections.wechatConnected,
              running: diagnosticConnections.wechatRunning,
            })
            : t("settingsPage.diagnostics.wechatEmpty"),
        status: wechatStatus,
        tone: diagnosticConnections.wechatConnected && diagnosticConnections.wechatConnected > 0 ? "success" : diagnosticConnections.wechatConnected == null ? "warning" : "neutral",
        action: t("settingsPage.diagnostics.viewChannels"),
        onClick: () => selectSection("channels"),
      },
      {
        id: "system_tools",
        title: t("settingsPage.diagnostics.runtimeDeps"),
        description: diagnosticConnections.dependencyMessage,
        status: dependencyStatus,
        tone: diagnosticConnections.dependencyInstalled == null ? "warning" : diagnosticConnections.dependencyInstalled ? "success" : "warning",
        action: t("settingsPage.diagnostics.viewDeps"),
        onClick: () => selectSection("system_tools"),
      },
    ];

    return <>
      {integratedHeader(t("settingsPage.diagnostics.title"), t("settingsPage.diagnostics.description"), <Button type="primary" loading={checking || diagnosticLoading} onClick={handleCheckAll}>{t("settingsPage.checkAll")}</Button>)}
      <div className="settings-diagnostics-notice" role="status">
        <InfoCircleOutlined />
        <span>{t("settingsPage.diagnostics.notice")}</span>
        <em>{t("settingsPage.diagnostics.lastChecked", { time: lastCheckedLabel })}</em>
      </div>
      <section className="settings-diagnostics-section" aria-label={t("settingsPage.diagnostics.connectionStatus")}>
        <h2>{t("settingsPage.diagnostics.connectionStatus")}</h2>
        <div className="settings-diagnostics-list">
          {rows.map((row) => <div className="settings-diagnostics-row" key={row.id}>
            <div className="settings-diagnostics-copy"><strong>{row.title}</strong><p>{row.description}</p></div>
            <Tag className={`settings-diagnostics-state is-${row.tone}`}>{row.status}</Tag>
            <Button onClick={row.onClick}>{row.action}</Button>
          </div>)}
        </div>
      </section>
    </>;
  };

  const renderDetail = () => {
    let content: ReactNode;

    if (section === "organization") {
      content = <>
        {integratedHeader(t("settingsPage.organization.title"), t("settingsPage.organization.description"))}
        <nav className="settings-organization-tabs" aria-label={t("settingsPage.organization.tabsAria")}>
          <button
            className={organizationView === "users" ? "is-active" : ""}
            type="button"
            role="tab"
            aria-selected={organizationView === "users"}
            onClick={() => setOrganizationView("users")}
          >
            {t("admin.userManagement")}
          </button>
          <button
            className={organizationView === "groups" ? "is-active" : ""}
            type="button"
            role="tab"
            aria-selected={organizationView === "groups"}
            onClick={() => setOrganizationView("groups")}
          >
            {t("admin.groupManagement")}
          </button>
        </nav>
        {integratedSurface(
          organizationView === "users" ? <UserManagement /> : <GroupManagement embedded />,
          "is-organization",
        )}
      </>;
    } else if (section === "models") {
      content = <>
        {integratedHeader(t("settingsPage.models.title"), selectedSection.detail)}
        <nav className="settings-model-tabs" aria-label={t("settingsPage.models.tabsAria")} role="tablist">
          <button className={modelView === "defaults" ? "is-active" : ""} type="button" role="tab" aria-selected={modelView === "defaults"} onClick={() => selectModelView("defaults")}>{t("settingsPage.models.defaultSettings")}</button>
          <button ref={modelProviderTabRef} className={modelView === "providers" ? "is-active" : ""} type="button" role="tab" aria-selected={modelView === "providers"} onClick={() => selectModelView("providers")}>{t("settingsPage.models.providers")}</button>
        </nav>
        {integratedSurface(modelView === "defaults" ? (
          <DefaultServicesPage
            onModelSelectionChanged={syncOverview}
            onConfigureCloudService={(service) => navigate(
              service === "cloudParsing"
                ? "/settings?section=knowledge&tool=document-parsing"
                : "/settings?section=knowledge&tool=web-search",
            )}
            onConfigureProviders={() => {
              selectModelView("providers");
              requestAnimationFrame(() => modelProviderTabRef.current?.focus());
            }}
          />
        ) : <ModelProvidersPage onConfigurationChanged={syncOverview} />, "is-models")}
      </>;
    } else if (section === "tasks") {
      const schedulesEnabled = Boolean(overview?.controls.schedules_enabled);
      content = <>
        <header className="settings-detail-header settings-task-page-heading">
          <div>
            <h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.tasks.title")}</h1>
            <div className="settings-task-view-description">
              <p>{t(taskView === "conversation"
                ? "settingsPage.tasks.defaultsDescription"
                : "settingsPage.tasks.taskDescription")}</p>
            </div>
          </div>
        </header>
        <Tabs
          className="settings-task-section-tabs"
          activeKey={taskView}
          onChange={(key: string) => selectTaskView(key as "conversation" | "tasks")}
          items={[
            {
              key: "conversation",
              label: t("settingsPage.tasks.conversationView"),
              children: <>
                <TaskEntryDefaults
                  subtasksEnabled={Boolean(overview?.controls.task_center_enabled)}
                  workflowsEnabled={Boolean(overview?.controls.workflows_enabled)}
                  onConnectExecutors={() => selectSection("assistants")}
                />
              </>,
            },
            {
              key: "tasks",
              label: t("settingsPage.tasks.taskView"),
              children: <>
                <section className="settings-task-controls is-schedules" aria-label={t("settingsPage.tasks.scheduleControlsAria")}>
                  {taskControl("schedules_enabled", <ClockCircleOutlined />, t("settingsPage.tasks.enableSchedules"), t("settingsPage.tasks.enableSchedulesDesc", { count: selectedSection.counts.enabled }))}
                </section>
                {integratedSurface(<SettingsScheduleList schedulesEnabled={schedulesEnabled} onChanged={syncOverview} />, "is-tasks")}
              </>,
            },
          ]}
        />
      </>;
    } else if (section === "knowledge") {
      content = knowledgeToolView ? (
        <KnowledgeToolSettings
          headingRef={headingRef}
          onBack={() => selectSection("knowledge")}
          view={knowledgeToolView}
        />
      ) : (
        <KnowledgeDataSettings
          controlsDisabled={saving !== null}
          documentParsingEnabled={Boolean(overview?.controls.document_parsing_enabled)}
          documentParsingSaving={saving === "document_parsing_enabled"}
          headingRef={headingRef}
          onDocumentParsingChange={(enabled) => requestMasterChange("document_parsing_enabled", enabled)}
        />
      );
    } else if (section === "memory") {
      content = <MemoryCapabilitySettings headingRef={headingRef} />;
    } else if (section === "skills") {
      content = <UserSkillWorkflowSettings
        skillsEnabled={Boolean(overview?.controls.skills_enabled)}
        workflowsEnabled={Boolean(overview?.controls.workflows_enabled)}
        groupSaving={saving === "skills_enabled" ? "skills" : saving === "workflows_enabled" ? "workflows" : null}
        controlsDisabled={saving !== null}
        onGroupChange={(group: ResourceTab, enabled: boolean, enabledCount: number) => requestMasterChange(group === "skills" ? "skills_enabled" : "workflows_enabled", enabled, enabledCount)}
        headingRef={headingRef}
        onChanged={syncOverview}
      />;
    } else if (section === "system_tools") {
      content = <>
        {integratedHeader(t("settingsPage.systemTools.title"), t("settingsPage.systemTools.description"))}
        {integratedSurface(
          <div className={`settings-system-tools-stack${hasLocalDependencies ? " has-local-dependencies" : ""}`}>
            <ToolManagementSection
              description={t("settingsPage.systemTools.builtinDesc")}
              title={t("settingsPage.systemTools.builtinTitle")}
              view="builtin"
            />
            <DependencyInstallSection />
          </div>,
          "is-system-tools",
        )}
      </>;
    } else if (section === "mcp") {
      content = <>
        {integratedHeader(t("settingsPage.sections.mcp"), t("settingsPage.overview.mcpDesc"))}
        {masterControl("mcp_enabled")}
        {integratedSurface(
          <ToolManagementSection
            description={t("settingsPage.systemTools.mcpDesc")}
            layout="settings"
            onChanged={syncOverview}
            refreshToken={mcpRefreshToken}
            title={t("settingsPage.systemTools.mcpTitle")}
            view="mcp"
          />,
          "is-mcp",
        )}
      </>;
    } else if (section === "assistants") {
      content = integratedSurface(<AgentIntegrationPage />, "is-assistants");
    } else if (section === "channels") {
      content = integratedSurface(<TerminalConnectionPage />, "is-channels");
    } else if (section === "recovery") {
      content = <RecoverySettings headingRef={headingRef} />;
    } else if (section === "diagnostics") {
      content = renderDiagnostics();
    } else {
      content = <>
        {integratedHeader(t("settingsPage.sections.developer"), selectedSection.detail)}
        <div className="settings-detail-group">
          <div className="settings-detail-row">
            <div>
              <strong>{t(developerActive ? "settingsPage.developer.disableTitle" : "settingsPage.developer.enableTitle")}</strong>
              <p>{t(runtimeFeatures.hideEvo
                ? "settingsPage.developer.enableDescWithoutEvo"
                : "settingsPage.developer.enableDesc")}</p>
            </div>
            <Switch
              className="settings-ref-switch"
              checked={developerActive}
              loading={saving === "developer"}
              disabled={saving !== null}
              onChange={requestDeveloperChange}
              aria-label={t("settingsPage.developer.modeAria")}
            />
          </div>
          <div className="settings-detail-row">
            <div>
              <strong>{t("settingsPage.developer.sensitiveWordFilter")}</strong>
              <p>{t("settingsPage.developer.sensitiveWordFilterDesc")}</p>
            </div>
            <Switch
              className="settings-ref-switch"
              checked={sensitiveWordFilterEnabled}
              loading={saving === "sensitive_word_filter"}
              disabled={!developerActive || saving !== null}
              onChange={async (enabled) => {
                setSaving("sensitive_word_filter");
                try {
                  await patchUserUiPreferences({ sensitive_word_filter_enabled: enabled });
                  setSensitiveWordFilterEnabled(enabled);
                  await refresh();
                  message.success(t("settingsPage.saved"));
                } catch {
                  message.error(t("settingsPage.saveFailed"));
                } finally {
                  setSaving(null);
                }
              }}
              aria-label={t("settingsPage.developer.sensitiveWordFilterAria")}
            />
          </div>
        </div>
        {developerActive ? (
          <div className="settings-detail-group">
            <div className="settings-detail-row">
              <div>
                <strong>{t("settingsPage.developer.performanceTitle")}</strong>
                <p>{t("settingsPage.developer.performanceDesc")}</p>
              </div>
              <Switch
                className="settings-ref-switch"
                checked={performanceStatsEnabled}
                loading={saving === "performance_stats"}
                disabled={saving !== null}
                onChange={async (enabled) => {
                  setSaving("performance_stats");
                  try {
                    await patchUserUiPreferences({ performance_stats_enabled: enabled });
                    setPerformanceStatsEnabled(enabled);
                    message.success(t("settingsPage.saved"));
                  } catch {
                    message.error(t("settingsPage.saveFailed"));
                  } finally {
                    setSaving(null);
                  }
                }}
                aria-label={t("settingsPage.developer.performanceAria")}
              />
            </div>
          </div>
        ) : null}
      </>;
    }

    return <section className={`settings-detail-page settings-integrated-page${section === "models" ? " is-models-page" : section === "system_tools" ? " is-system-tools-page" : section === "mcp" ? " is-mcp-page" : ""}`}>
      {content}
      <div className="settings-screenreader-status" role="status" aria-live="polite">
        {saving ? t("settingsPage.savingStatus") : checking ? t("settingsPage.checkingStatus") : ""}
      </div>
    </section>;
  };

  return <main className="settings-reference" aria-label={t("settingsPage.title")}>
    <aside className="settings-reference-sidebar">
      <button className="settings-back-button" type="button" onClick={() => navigate("/agent/chat/home")}>
        <ArrowLeftOutlined />{t("settingsPage.backToHome")}
      </button>
      <div className="settings-reference-search">
        <Input
          prefix={<SearchOutlined />}
          value={keyword}
          onChange={(event: ChangeEvent<HTMLInputElement>) => setKeyword(event.target.value)}
          placeholder={t("settingsPage.searchPlaceholder")}
          aria-label={t("settingsPage.searchAria")}
          allowClear
        />
      </div>
      <nav className="settings-reference-nav" aria-label={t("settingsPage.navAria")}>
        {filteredGroups.map((group) => (
          <div className="settings-reference-nav-group" key={group.title}>
            <p>{group.title}</p>
            {group.items.map((item) => (
              <button
                key={item.id}
                type="button"
                className={section === item.id ? "is-active" : ""}
                onClick={() => selectSection(item.id)}
              >
                <span className="settings-reference-nav-icon">{item.icon}</span>
                <span>{item.label}</span>
                {item.status ? (
                  <em>{item.id === "developer" && !developerActive ? t("settingsPage.sectionStatus.notActivated") : item.status}</em>
                ) : null}
              </button>
            ))}
          </div>
        ))}
        {filteredGroups.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("settingsPage.noMatch")} /> : null}
      </nav>
      <div className="settings-reference-sidebar-foot">LazyAGI/LazyMind<br />{t("settingsPage.sidebarFoot")}</div>
    </aside>
    <section className="settings-reference-content" aria-busy={loading}>
      <div className="settings-reference-scroll">
        {loading ? (
          <div className="settings-reference-loading"><Skeleton active paragraph={{ rows: 12 }} /></div>
        ) : loadError ? (
          <div className="settings-reference-error">
            <Alert
              type="error"
              showIcon
              message={t("settingsPage.loadFailed")}
              description={t("settingsPage.loadFailedDesc")}
              action={<Button size="small" onClick={() => void refresh()}>{t("settingsPage.retry")}</Button>}
            />
          </div>
        ) : section === "overview" ? renderDashboard() : renderDetail()}
      </div>
    </section>
  </main>;
}

function CheckResults({ checks, onLocate }: { checks: SettingsCheckResult[]; onLocate: (section: SectionID) => void }) {
  const { t } = useTranslation();
  return (
    <section className="settings-check-results" role="status" aria-live="polite">
      <h2>{t("settingsPage.lastCheck")}</h2>
      {checks.map((result) => (
        <div className="settings-check-result" key={result.id}>
          <span>{result.status === "attention" ? <WarningFilled /> : <CheckCircleFilled />}</span>
          <p>{result.message}</p>
          <Tag className={result.status === "attention" ? "settings-check-warning" : "settings-status-tag"}>
            {result.status === "passed"
              ? t("settingsPage.passed")
              : result.status === "attention"
                ? t("settingsPage.needsAttention")
                : t("settingsPage.needsManualVerify")}
          </Tag>
          <button type="button" onClick={() => onLocate(result.section as SectionID)}>{t("settingsPage.locate")}</button>
        </div>
      ))}
    </section>
  );
}
