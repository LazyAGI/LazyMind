import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Alert, Button, Empty, Input, Modal, Skeleton, Switch, Tag, message } from "antd";
import {
  ApiOutlined,
  ArrowLeftOutlined,
  CheckCircleFilled,
  CodeOutlined,
  DatabaseOutlined,
  ExperimentOutlined,
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
import { useNavigate, useSearchParams } from "react-router-dom";
import { AgentAppsAuth } from "@/components/auth";
import { TerminalConnectionPage } from "@/modules/channelGateway";
import { setAllMcpServersEnabled } from "@/modules/memory/toolApi";
import DependencyInstallSection from "@/modules/modelProvider/components/DependencyInstallSection";
import ToolManagementSection from "@/modules/modelProvider/components/ToolManagementSection";
import DefaultServicesPage from "@/modules/modelProvider/pages/DefaultServicesPage";
import ModelProvidersPage from "@/modules/modelProvider/pages/ModelProvidersPage";
import SettingsScheduleList from "@/modules/taskCenter/SettingsScheduleList";
import { fetchUserUiPreferences, patchUserUiPreferences } from "@/modules/user/uiPreferencesApi";
import { isDesktopRuntime, isLocalRuntime } from "@/runtime/mode";
import { setDeveloperModeActive } from "@/utils/developerMode";
import MemoryCapabilitySettings, { MemoryEditQuickControl } from "./MemoryCapabilitySettings";
import KnowledgeDataSettings from "./KnowledgeDataSettings";
import QuickModelSettings from "./QuickModelSettings";
import UserSkillWorkflowSettings, { type ResourceTab } from "./UserSkillWorkflowSettings";
import {
  fetchSettingsOverview,
  runSettingsChecks,
  type SettingsCheckResult,
  type SettingsOverview,
  type SettingsOverviewSection,
} from "./api";
import "@/modules/knowledge/style.css";
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
  | "channels"
  | "diagnostics"
  | "organization"
  | "developer";
type MasterSetting = "task_center_enabled" | "skills_enabled" | "workflows_enabled" | "mcp_enabled" | "document_parsing_enabled";

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

const controlCopy: Record<MasterSetting, { title: string; summary: string; section: SectionID }> = {
  task_center_enabled: { title: "任务中心", summary: "统一暂停后续调度与立即执行", section: "tasks" },
  skills_enabled: { title: "我的技能", summary: "统一启用或停用全部个人技能", section: "skills" },
  workflows_enabled: { title: "我的工作流", summary: "统一启用或停用全部可用工作流", section: "skills" },
  mcp_enabled: { title: "MCP 工具", summary: "统一启用或停用当前用户的 MCP 服务", section: "mcp" },
  document_parsing_enabled: { title: "文档解析", summary: "暂停新的文档解析与重新解析", section: "knowledge" },
};

function isAdminRole(role?: string) {
  const value = (role || "").trim().toLowerCase();
  return value === "admin" || value === "system-admin" || value === "system_admin" || value.endsWith(".admin");
}

function baseNavigation(isAdmin: boolean): NavigationGroup[] {
  const groups: NavigationGroup[] = [
    {
      title: "开始使用",
      items: [
        { id: "models", label: "模型与服务", keywords: "模型 服务 系统默认设置 供应商", icon: <ApiOutlined />, status: "已就绪" },
        { id: "overview", label: "设置概览", keywords: "设置 概览 仪表盘 关键配置", icon: <SettingOutlined />, status: "已同步" },
      ],
    },
    {
      title: "对话与知识",
      items: [
        { id: "tasks", label: "对话与子任务", keywords: "对话 任务 定时任务 自动化 计划", icon: <RobotOutlined /> },
        { id: "knowledge", label: "知识与数据", keywords: "知识 数据 本地文件 云文件", icon: <DatabaseOutlined /> },
        { id: "memory", label: "记忆与自进化", keywords: "记忆 自进化 跨会话", icon: <ExperimentOutlined /> },
      ],
    },
    {
      title: "能力与集成",
      items: [
        { id: "skills", label: "技能与插件", keywords: "技能 插件 工作流", icon: <RobotOutlined /> },
        { id: "system_tools", label: "系统工具", keywords: "系统工具 依赖 FFmpeg", icon: <ToolOutlined /> },
        { id: "mcp", label: "MCP 工具", keywords: "MCP 服务 连接 验证 权限", icon: <ToolOutlined /> },
        { id: "channels", label: "终端连接", keywords: "终端 微信 飞书 渠道", icon: <LinkOutlined />, status: "连接" },
      ],
    },
    {
      title: "管理",
      items: [
        ...(isAdmin ? [{ id: "organization" as const, label: "组织与共享", keywords: "组织 共享 系统管理 用户 用户组", icon: <TeamOutlined /> }] : []),
        { id: "diagnostics", label: "同步与查验", keywords: "同步 查验 模型 MCP 渠道 诊断", icon: <CheckCircleFilled /> },
        ...(isAdmin ? [{ id: "developer" as const, label: "开发者", keywords: "开发者 调试 开发者模式", icon: <CodeOutlined />, status: "已激活" }] : []),
      ],
    },
  ];
  return groups.filter((group) => group.items.length > 0);
}

function sectionFallback(section: SectionID): SettingsOverviewSection {
  const item = baseNavigation(true).flatMap((group) => group.items).find((entry) => entry.id === section);
  return {
    id: section,
    title: item?.label || "设置",
    route: "/agent/chat/home",
    counts: { total: 0, enabled: 0, verified: 0, runnable: 0, configured: 0 },
    status: "ready",
    detail: "集中查看现有配置和运行状态。",
  };
}

function formatCount(section: SettingsOverviewSection) {
  if (section.id === "models") return section.counts.configured ? `已配置 ${section.counts.configured} 项` : "待配置";
  if (section.id === "tasks") return `${section.counts.enabled} 个自动化计划`;
  if (section.id === "skills") return `${section.counts.enabled} 个已启用资源`;
  if (section.id === "mcp") return `${section.counts.runnable} 个服务可运行`;
  return section.detail;
}

export default function SettingsPage() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const isAdmin = isAdminRole(AgentAppsAuth.getUserInfo()?.role);
  const hasLocalDependencies = isLocalRuntime() || isDesktopRuntime();
  const navigationGroups = useMemo(() => baseNavigation(isAdmin), [isAdmin]);
  const navigationItems = useMemo(() => navigationGroups.flatMap((group) => group.items), [navigationGroups]);
  const candidate = searchParams.get("section") as SectionID | null;
  const section = navigationItems.some((item) => item.id === candidate) ? candidate! : "overview";
  const headingRef = useRef<HTMLHeadingElement>(null);
  const latestRequest = useRef(0);
  const [overview, setOverview] = useState<SettingsOverview | null>(null);
  const [developerActive, setDeveloperActive] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [saving, setSaving] = useState<MasterSetting | "developer" | null>(null);
  const [checks, setChecks] = useState<SettingsCheckResult[] | null>(null);
  const [checking, setChecking] = useState(false);
  const [keyword, setKeyword] = useState("");
  const [modelView, setModelView] = useState<"defaults" | "providers">("defaults");
  const [mcpRefreshToken, setMcpRefreshToken] = useState(0);

  const refresh = useCallback(async () => {
    const requestID = ++latestRequest.current;
    setLoading(true);
    setLoadError(false);
    try {
      const [nextOverview, preferences] = await Promise.all([fetchSettingsOverview(), fetchUserUiPreferences()]);
      if (requestID !== latestRequest.current) return;
      setOverview(nextOverview);
      setDeveloperActive(preferences.developer_mode_active);
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
    if (!query) return navigationGroups;
    return navigationGroups.map((group) => ({
      ...group,
      items: group.items.filter((item) => `${item.label} ${item.keywords}`.toLowerCase().includes(query)),
    })).filter((group) => group.items.length > 0);
  }, [keyword, navigationGroups]);

  const selectSection = (next: SectionID) => setSearchParams({ section: next });
  const selectedSection = overview?.sections.find((item) => item.id === section) || sectionFallback(section);

  const syncOverview = useCallback(async () => {
    try {
      setOverview(await fetchSettingsOverview());
    } catch {
      // The detail view owns its visible state; the next page refresh retries the aggregate sync.
    }
  }, []);

  const requestMasterChange = (key: MasterSetting, enabled: boolean, enabledCountOverride?: number) => {
    const target = controlCopy[key];
    const sectionInfo = overview?.sections.find((item) => item.id === target.section);
    const enabledCount = enabledCountOverride ?? sectionInfo?.counts.enabled ?? 0;
    const resourceLabel = key === "task_center_enabled"
      ? `已启用定时任务 ${enabledCount} 个`
      : key === "document_parsing_enabled"
        ? "已配置的解析服务和历史文档会被保留"
      : key === "skills_enabled"
        ? `已启用技能 ${enabledCount} 个`
        : key === "workflows_enabled"
          ? `已启用工作流 ${enabledCount} 个`
          : `已启用服务 ${enabledCount} 个`;
    const isResourceBulkChange = key === "skills_enabled" || key === "workflows_enabled" || key === "mcp_enabled";
    const resourceChangeText = key === "mcp_enabled"
      ? `${resourceLabel}；本次操作会将当前用户的 MCP 服务统一设为${enabled ? "启用" : "停用"}，开启时未验证服务仍保持停用。`
      : `${resourceLabel}；本次操作只会将${target.title}的子项统一设为${enabled ? "启用" : "停用"}。`;
    Modal.confirm({
      title: `${enabled ? "开启" : "关闭"}${target.title}`,
      content: <div className="settings-ref-confirm">
        <p>立即生效：{target.summary}。</p>
        <p>{isResourceBulkChange ? resourceChangeText : key === "document_parsing_enabled" ? resourceLabel : `${resourceLabel}；子项原始开关会被保留。`}</p>
        <p>{key === "task_center_enabled" ? "已开始执行的任务不会被强制终止。重新开启后仅从当前时间计算下一次任务。" : key === "document_parsing_enabled" ? "已经开始的解析任务不会被强制终止；关闭后拒绝新的解析和重新解析。" : key === "mcp_enabled" ? "已经发起的 MCP 调用不会被强制终止；他人共享的 MCP 服务不受影响。" : isResourceBulkChange ? `已经发起的${target.title}调用不会被强制终止，另一组资源不受影响。` : "已经发起的调用不会被强制终止。重新开启后恢复子项选择。"}</p>
      </div>,
      okText: enabled ? "确认开启" : "确认关闭",
      cancelText: "取消",
      okButtonProps: enabled ? undefined : { danger: true },
      onOk: async () => {
        setSaving(key);
        try {
          if (key === "mcp_enabled") {
            const result = await setAllMcpServersEnabled(enabled);
            setMcpRefreshToken((value) => value + 1);
            await refresh();
            if (enabled && result.skippedUnverifiedCount > 0) {
              message.warning(`已启用 ${result.updatedCount} 个服务，${result.skippedUnverifiedCount} 个未验证服务保持停用`);
            } else {
              message.success(`已${enabled ? "启用" : "停用"} ${result.updatedCount} 个 MCP 服务`);
            }
          } else {
            await patchUserUiPreferences({ [key]: enabled });
          }
          if (key !== "mcp_enabled" && isResourceBulkChange) {
            await syncOverview();
          } else if (key !== "mcp_enabled") {
            await refresh();
          }
          if (key !== "mcp_enabled") message.success("设置已保存");
        } catch {
          message.error("保存失败，已保留原设置");
        } finally {
          setSaving(null);
        }
      },
    });
  };

  const requestDeveloperChange = (enabled: boolean) => {
    Modal.confirm({
      title: `${enabled ? "开启" : "关闭"}开发者模式`,
      content: "该模式只对管理员可见。关闭后不会删除已有的自进化数据。",
      okText: enabled ? "确认开启" : "确认关闭",
      cancelText: "取消",
      okButtonProps: enabled ? undefined : { danger: true },
      onOk: async () => {
        setSaving("developer");
        try {
          await patchUserUiPreferences({ developer_mode_active: enabled });
          setDeveloperModeActive(enabled);
          await refresh();
          message.success("设置已保存");
        } catch {
          message.error("保存失败，已保留原设置");
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
      await refresh();
    } catch {
      message.error("检查未完成，请重试");
    } finally {
      setChecking(false);
    }
  };

  const switchControl = (key: MasterSetting) => <Switch className="settings-ref-switch" checked={Boolean(overview?.controls[key])} loading={saving === key} disabled={saving !== null} onChange={(checked) => requestMasterChange(key, checked)} aria-label={controlCopy[key].title} />;

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
      <div className="settings-dashboard-card-foot"><button type="button" onClick={() => selectSection(target)} aria-label={`前往${title}详细配置`}>跳转至详细配置 <RightOutlined /></button></div>
    </section>
  );

  const renderDashboard = () => {
    const sections = overview?.sections || [];
    const get = (id: string) => sections.find((item) => item.id === id) || sectionFallback(id as SectionID);
    const tasks = get("tasks");
    const skills = get("skills");
    const mcp = get("mcp");
    return <section className="settings-dashboard">
      <div className="settings-page-heading"><div><h1 ref={headingRef} tabIndex={-1}>设置概览</h1><p>集中查看和修改各模块的关键配置；这里与详细配置页使用同一份状态。</p></div><Tag className="settings-sync-tag">配置实时同步</Tag></div>
      {overview?.issues.length ? <div className="settings-ref-issues" role="status" aria-live="polite">{overview.issues.map((issue) => <Alert key={issue.id} type={issue.severity === "warning" ? "warning" : "info"} showIcon message={issue.message} action={<Button type="link" size="small" onClick={() => selectSection(issue.section as SectionID)}>查看</Button>} />)}</div> : null}
      <div className="settings-dashboard-grid">
        {dashboardCard("models", <ApiOutlined />, "模型与服务", "默认模型与供应商连接", [
          <QuickModelSettings canConfigureEmbedding={isAdmin} key="quick-models" onSaved={syncOverview} />,
        ])}
        {dashboardCard("knowledge", <DatabaseOutlined />, "知识与数据", "文件访问与知识处理服务", [
          dashboardRow("知识与数据", "文档解析", "控制新的文档解析与重新解析", switchControl("document_parsing_enabled")),
          dashboardRow("知识与数据", "检索与数据连接", "管理知识库、搜索服务、云文件和数据库连接", <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("knowledge")}>快速配置</Button>),
        ])}
        {dashboardCard("memory", <ExperimentOutlined />, "记忆与自进化", "跨会话记忆能力", [
          dashboardRow("记忆与自进化", "记忆编辑", "记录和编辑跨会话的用户记忆和偏好", <MemoryEditQuickControl onSaved={syncOverview} />),
        ])}
        {dashboardCard("system_tools", <ToolOutlined />, "系统工具", "内置工具与本地运行环境依赖", [
          dashboardRow("系统工具", "内置工具", "管理系统内置工具的启用状态", <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("system_tools")}>管理工具</Button>),
          dashboardRow("系统工具", "本地依赖", "仅本地运行环境展示依赖安装", <Button className="settings-dashboard-quick-action" size="small" onClick={() => selectSection("system_tools")}>{hasLocalDependencies ? "检查依赖" : "云端托管"}</Button>),
        ])}
        {dashboardCard("mcp", <ToolOutlined />, "MCP 工具", "外部服务连接与可用状态", [
          dashboardRow("MCP 工具", "MCP 工具总开关", "统一控制全部 MCP 服务的可用状态", switchControl("mcp_enabled")),
          dashboardRow("MCP 工具", "已验证服务", `${mcp.counts.verified} 个已验证，${mcp.counts.runnable} 个可运行`, <Tag className="settings-status-tag">{formatCount(mcp)}</Tag>),
        ])}
        {dashboardCard("skills", <RobotOutlined />, "技能与插件", "技能能力与插件运行状态", [
          dashboardRow("技能与插件", "我的技能", "批量控制个人技能的可用状态", switchControl("skills_enabled")),
          dashboardRow("技能与插件", "我的工作流", "批量控制可用工作流的运行状态", switchControl("workflows_enabled")),
          dashboardRow("技能与插件", "已启用资源", `${skills.counts.enabled} 个技能或工作流当前启用`, <Tag className="settings-status-tag">分别控制</Tag>),
        ])}
        {dashboardCard("tasks", <UnorderedListOutlined />, "对话与子任务", "任务执行与自动化计划", [
          dashboardRow("对话与子任务", "启用任务中心", "与主页面任务中心使用同一开关状态", switchControl("task_center_enabled")),
          dashboardRow("对话与子任务", "定时任务", `${tasks.counts.enabled} 个自动化计划`, <Tag className="settings-status-tag">{overview?.controls.task_center_enabled ? "运行中" : "已暂停"}</Tag>),
        ])}
        {dashboardCard("diagnostics", <CheckCircleFilled />, "同步与查验", "连接、权限和运行环境状态", [
          dashboardRow("同步与查验", "检查全部", "模型、MCP 和本地依赖状态", <Button size="small" loading={checking} onClick={handleCheckAll}>检查</Button>),
          dashboardRow("同步与查验", "最近结果", checks ? `${checks.length} 项检查结果` : "尚未运行检查", <Tag className="settings-status-tag">可查看</Tag>),
        ])}
        {isAdmin && dashboardCard("developer", <CodeOutlined />, "开发者", "调试能力与执行过程显示", [
          dashboardRow("开发者", "启用开发者模式", "控制系统工具管理和调试能力", <Switch className="settings-ref-switch" checked={developerActive} loading={saving === "developer"} disabled={saving !== null} onChange={requestDeveloperChange} aria-label="启用开发者模式" />),
          dashboardRow("开发者", "内部调试", "显示完整执行轨迹和调试信息", <Tag className="settings-status-tag">管理员</Tag>),
        ])}
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

  const masterControl = (key: MasterSetting, title = `${controlCopy[key].title}总开关`) => {
    const sectionInfo = overview?.sections.find((item) => item.id === controlCopy[key].section);
    const statusText = !overview?.controls[key]
      ? "当前暂停"
      : sectionInfo?.effective_enabled
        ? "当前可用"
        : key === "mcp_enabled"
          ? "等待服务验证"
          : "等待子项启用";
    const consequence = key === "mcp_enabled"
      ? "关闭后全部自有服务停用；开启时只启用已验证服务。"
      : "关闭后保留子项原始状态。";
    return <section className="settings-integrated-master" aria-label={title}>
      <div><strong>{title}</strong><p>{controlCopy[key].summary}；{consequence}</p></div>
      <div className="settings-integrated-master-action"><Tag className="settings-status-tag">{statusText}</Tag>{switchControl(key)}</div>
    </section>;
  };

  const integratedSurface = (content: ReactNode, className = "") => (
    <div className={`settings-integrated-surface ${className}`.trim()}>{content}</div>
  );

  const renderDetail = () => {
    let content: ReactNode;

    if (section === "organization") {
      content = <><div className="settings-info-banner"><TeamOutlined />组织管理沿用现有的系统管理入口，不在设置页面重复建设。</div><div className="settings-admin-entry"><span className="settings-section-icon"><TeamOutlined /></span><div><h2>进入系统管理</h2><p>继续管理用户、用户组、数据源和其他管理员资源。</p></div><Button type="primary" onClick={() => navigate("/admin")}>打开系统管理</Button></div></>;
    } else if (section === "models") {
      content = <>
        {integratedHeader("模型与服务", selectedSection.detail)}
        <nav className="settings-model-tabs" aria-label="模型与服务页面">
          <button className={modelView === "defaults" ? "is-active" : ""} type="button" onClick={() => setModelView("defaults")}>系统默认设置</button>
          <button className={modelView === "providers" ? "is-active" : ""} type="button" onClick={() => setModelView("providers")}>模型供应商</button>
        </nav>
        {integratedSurface(modelView === "defaults" ? <DefaultServicesPage /> : <ModelProvidersPage />, "is-models")}
      </>;
    } else if (section === "tasks") {
      const taskCenterEnabled = Boolean(overview?.controls.task_center_enabled);
      content = <>
        {integratedHeader("对话与子任务", "控制任务中心与自动化计划；状态与主页面实时同步。", <Tag className="settings-sync-tag">{taskCenterEnabled ? "已开启" : "已暂停"}</Tag>)}
        {masterControl("task_center_enabled", "启用任务中心")}
        {integratedSurface(<SettingsScheduleList masterEnabled={taskCenterEnabled} onChanged={syncOverview} />, "is-tasks")}
      </>;
    } else if (section === "knowledge") {
      content = <KnowledgeDataSettings
        controlsDisabled={saving !== null}
        documentParsingEnabled={Boolean(overview?.controls.document_parsing_enabled)}
        documentParsingSaving={saving === "document_parsing_enabled"}
        headingRef={headingRef}
        onDocumentParsingChange={(enabled) => requestMasterChange("document_parsing_enabled", enabled)}
        onOpenModels={() => selectSection("models")}
      />;
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
        {integratedHeader("系统工具", "管理内置系统工具及本地运行环境依赖。")}
        {integratedSurface(
          <div className={`settings-system-tools-stack${hasLocalDependencies ? " has-local-dependencies" : ""}`}>
            <ToolManagementSection
              description="按需启用系统能力；关闭工具不会删除已有配置。"
              title="内置工具"
              view="builtin"
            />
            <DependencyInstallSection />
          </div>,
          "is-system-tools",
        )}
      </>;
    } else if (section === "mcp") {
      content = <>{integratedHeader("MCP 工具", selectedSection.detail)}{masterControl("mcp_enabled")}{integratedSurface(<ToolManagementSection description="管理服务连接、工具发现与调用权限。" layout="settings" refreshToken={mcpRefreshToken} title="MCP 服务" view="mcp" />, "is-mcp")}</>;
    } else if (section === "channels") {
      content = integratedSurface(<TerminalConnectionPage />, "is-channels");
    } else if (section === "diagnostics") {
      content = <>{integratedHeader("同步与查验", selectedSection.detail, <Button type="primary" loading={checking} onClick={handleCheckAll}>检查全部</Button>)}{checks ? <CheckResults checks={checks} onLocate={selectSection} /> : <div className="settings-detail-group"><div className="settings-detail-row"><div><strong>尚未运行检查</strong><p>检查模型、MCP、渠道和本地依赖状态，并在这里直接定位问题。</p></div><Button loading={checking} onClick={handleCheckAll}>开始检查</Button></div></div>}</>;
    } else {
      content = <>{integratedHeader("开发者", selectedSection.detail, <Tag className="settings-admin-tag">管理员专属</Tag>)}<div className="settings-detail-group"><div className="settings-detail-row"><div><strong>启用开发者模式</strong><p>激活工具管理、算法跃迁、内部 ID 和完整执行过程。</p></div><Switch className="settings-ref-switch" checked={developerActive} loading={saving === "developer"} disabled={saving !== null} onChange={requestDeveloperChange} aria-label="开发者模式" /></div></div></>;
    }

    return <section className={`settings-detail-page settings-integrated-page${section === "system_tools" ? " is-system-tools-page" : section === "mcp" ? " is-mcp-page" : ""}`}>
      {content}
      <div className="settings-screenreader-status" role="status" aria-live="polite">{saving ? "正在保存设置" : checking ? "正在检查全部设置" : ""}</div>
    </section>;
  };

  return <main className="settings-reference" aria-label="设置">
    <aside className="settings-reference-sidebar">
      <button className="settings-back-button" type="button" onClick={() => navigate(-1)}><ArrowLeftOutlined />返回主页面</button>
      <div className="settings-reference-search"><Input prefix={<SearchOutlined />} value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="搜索设置..." aria-label="搜索设置" allowClear /></div>
      <nav className="settings-reference-nav" aria-label="设置导航">
        {filteredGroups.map((group) => <div className="settings-reference-nav-group" key={group.title}><p>{group.title}</p>{group.items.map((item) => <button key={item.id} type="button" className={section === item.id ? "is-active" : ""} onClick={() => selectSection(item.id)}><span className="settings-reference-nav-icon">{item.icon}</span><span>{item.label}</span>{item.status ? <em>{item.id === "developer" && !developerActive ? "未激活" : item.status}</em> : null}</button>)}</div>)}
        {filteredGroups.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有匹配的设置" /> : null}
      </nav>
      <div className="settings-reference-sidebar-foot">LazyAGI/LazyMind<br />设置与详细配置实时同步</div>
    </aside>
    <section className="settings-reference-content" aria-busy={loading}>
      <div className="settings-reference-scroll">{loading ? <div className="settings-reference-loading"><Skeleton active paragraph={{ rows: 12 }} /></div> : loadError ? <div className="settings-reference-error"><Alert type="error" showIcon message="无法加载设置" description="请检查网络或稍后重试。" action={<Button size="small" onClick={() => void refresh()}>重试</Button>} /></div> : section === "overview" ? renderDashboard() : renderDetail()}</div>
    </section>
  </main>;
}

function CheckResults({ checks, onLocate }: { checks: SettingsCheckResult[]; onLocate: (section: SectionID) => void }) {
  return <section className="settings-check-results" role="status" aria-live="polite"><h2>最近一次检查</h2>{checks.map((result) => <div className="settings-check-result" key={result.id}><span>{result.status === "attention" ? <WarningFilled /> : <CheckCircleFilled />}</span><p>{result.message}</p><Tag className={result.status === "attention" ? "settings-check-warning" : "settings-status-tag"}>{result.status === "passed" ? "通过" : result.status === "attention" ? "需处理" : "需单独验证"}</Tag><button type="button" onClick={() => onLocate(result.section as SectionID)}>定位</button></div>)}</section>;
}
