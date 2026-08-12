import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode, RefObject } from "react";
import { Alert, Button, Drawer, Empty, Skeleton, Switch, Tag, message } from "antd";
import {
  ApiOutlined,
  DatabaseOutlined,
  FileSearchOutlined,
  FolderOpenOutlined,
  GlobalOutlined,
  ReadOutlined,
  ReloadOutlined,
  RightOutlined,
} from "@ant-design/icons";
import KnowledgeLayout from "@/modules/knowledge/layout";
import KnowledgeListPage from "@/modules/knowledge/pages/list";
import DatabaseConnectionsPage from "@/modules/dataSource/database";
import CloudDocumentsPage from "@/modules/modelProvider/pages/CloudDocumentsPage";
import ExternalServicesPage from "@/modules/modelProvider/pages/ExternalServicesPage";
import {
  disableTool,
  enableTool,
  listToolAssetsPage,
  notifyToolAvailabilityChanged,
} from "@/modules/memory/toolApi";
import type { StructuredAsset } from "@/modules/memory/shared";

type ResourceTab = "knowledge" | "cloud" | "database";
type DetailView =
  | { type: "services"; title: string }
  | { type: "resource"; title: string; resourceTab: ResourceTab };

interface KnowledgeDataSettingsProps {
  documentParsingEnabled: boolean;
  documentParsingSaving: boolean;
  controlsDisabled: boolean;
  headingRef: RefObject<HTMLHeadingElement>;
  onDocumentParsingChange: (enabled: boolean) => void;
  onOpenModels: () => void;
}

interface ToolDefinition {
  id: string;
  name: string;
  description: string;
}

interface ToolGroupDefinition {
  id: string;
  title: string;
  description: string;
  icon: ReactNode;
  tools: ToolDefinition[];
  destination?: "services" | "resources" | "models";
  resourceTab?: ResourceTab;
}

const toolGroups: ToolGroupDefinition[] = [
  {
    id: "retrieval",
    title: "知识检索",
    description: "知识库发现、文档查询和当前对话临时文件检索。",
    icon: <ReadOutlined />,
    destination: "resources",
    resourceTab: "knowledge",
    tools: [
      { id: "kb", name: "知识库", description: "发现知识库、查询文档与统计，并进行语义、关键词和上下文检索" },
      { id: "temp_kb", name: "临时文件检索", description: "从当前对话上传的临时文件中搜索相关内容" },
    ],
  },
  {
    id: "data",
    title: "数据源",
    description: "查看已配置数据源，并以只读方式访问外部数据库。",
    icon: <DatabaseOutlined />,
    destination: "resources",
    resourceTab: "database",
    tools: [
      { id: "data_sources", name: "数据源查询", description: "查询已经配置的数据源提供方" },
      { id: "external_db", name: "外部数据库查询", description: "查看数据库 schema，并执行只读 SELECT 或 WITH 查询" },
    ],
  },
  {
    id: "file-access",
    title: "文件访问",
    description: "读取当前设备的授权目录和已连接的云文件系统。",
    icon: <FolderOpenOutlined />,
    destination: "resources",
    resourceTab: "cloud",
    tools: [
      { id: "local_fs", name: "本地文件", description: "在已授权路径内搜索、读取和精确修改文件" },
      { id: "cloud_files", name: "云文件", description: "浏览、搜索和管理已连接的云文件系统" },
    ],
  },
  {
    id: "search",
    title: "搜索引擎工具",
    description: "按任务类型调用开放网页、学术资源和稳定百科内容。",
    icon: <GlobalOutlined />,
    destination: "services",
    tools: [
      { id: "web_search", name: "网页搜索", description: "自动选择已配置的网页搜索服务" },
      { id: "academic_search", name: "学术搜索", description: "自动选择可用的学术论文搜索服务" },
      { id: "wikipedia", name: "Wikipedia 搜索", description: "检索稳定百科背景和明确词条" },
      { id: "url_fetch", name: "网页抓取", description: "获取并解析公开网页的可读内容" },
    ],
  },
  {
    id: "recognition",
    title: "内容识别",
    description: "使用已选择的多模态模型识别图片内容。",
    icon: <FileSearchOutlined />,
    destination: "models",
    tools: [
      { id: "multimodal", name: "多模态识别", description: "从图片中提取文字和内容描述" },
    ],
  },
];

const allToolDefinitions = toolGroups.flatMap((group) => group.tools);

export default function KnowledgeDataSettings({
  documentParsingEnabled,
  documentParsingSaving,
  controlsDisabled,
  headingRef,
  onDocumentParsingChange,
  onOpenModels,
}: KnowledgeDataSettingsProps) {
  const [detailView, setDetailView] = useState<DetailView | null>(null);
  const [tools, setTools] = useState<StructuredAsset[]>([]);
  const [toolsLoading, setToolsLoading] = useState(true);
  const [toolsError, setToolsError] = useState(false);
  const [pendingTools, setPendingTools] = useState<Set<string>>(new Set());
  const requestSequence = useRef(0);

  const loadTools = useCallback(async () => {
    const requestID = ++requestSequence.current;
    setToolsLoading(true);
    setToolsError(false);
    try {
      const response = await listToolAssetsPage({ silentError: true });
      if (requestID === requestSequence.current) setTools(response.records);
    } catch {
      if (requestID === requestSequence.current) setToolsError(true);
    } finally {
      if (requestID === requestSequence.current) setToolsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadTools();
    return () => { requestSequence.current += 1; };
  }, [loadTools]);

  const toolsByID = useMemo(
    () => new Map(tools.map((tool) => [tool.id, tool])),
    [tools],
  );
  const managedTools = allToolDefinitions
    .map((definition) => toolsByID.get(definition.id))
    .filter((tool): tool is StructuredAsset => Boolean(tool));

  const toggleTool = async (tool: StructuredAsset, enabled: boolean) => {
    if (pendingTools.has(tool.id)) return;
    setPendingTools((current) => new Set(current).add(tool.id));
    try {
      if (enabled) await enableTool(tool.id);
      else await disableTool(tool.id);
      setTools((current) => current.map((item) => (
        item.id === tool.id ? { ...item, isEnabled: enabled } : item
      )));
      notifyToolAvailabilityChanged({ id: tool.id, enabled });
      message.success(`${tool.name}已${enabled ? "启用" : "停用"}`);
    } catch {
      message.error(`${tool.name}设置失败，已保留原状态`);
    } finally {
      setPendingTools((current) => {
        const next = new Set(current);
        next.delete(tool.id);
        return next;
      });
    }
  };

  const openGroupDestination = (group: ToolGroupDefinition) => {
    if (group.destination === "models") {
      onOpenModels();
      return;
    }
    if (group.destination === "resources" && group.resourceTab) {
      setDetailView({ type: "resource", title: group.title, resourceTab: group.resourceTab });
      return;
    }
    if (group.destination === "services") {
      setDetailView({ type: "services", title: group.title });
    }
  };

  const renderTool = (definition: ToolDefinition, group: ToolGroupDefinition) => {
    const tool = toolsByID.get(definition.id);
    const pending = pendingTools.has(definition.id);
    const status = !tool
      ? { label: "未注册", className: "is-unavailable" }
      : tool.readonly
        ? { label: "固定开启", className: "is-fixed" }
        : tool.isEnabled
          ? { label: "已启用", className: "is-enabled" }
          : { label: "已停用", className: "is-disabled" };

    return <div className="settings-knowledge-tool-row" key={definition.id}>
      <span className="settings-knowledge-tool-icon" aria-hidden="true">{group.icon}</span>
      <div className="settings-knowledge-tool-copy">
        <strong>{tool?.name || definition.name}</strong>
        <p>{tool?.description || definition.description}</p>
      </div>
      <Tag className={`settings-knowledge-state ${status.className}`}>{status.label}</Tag>
      <Switch
        aria-label={`${tool?.name || definition.name}启用状态`}
        checked={Boolean(tool?.isEnabled)}
        className="settings-ref-switch"
        disabled={!tool || tool.readonly || pending}
        loading={pending}
        onChange={(enabled) => { if (tool) void toggleTool(tool, enabled); }}
      />
      {group.destination ? <Button
        aria-label={`打开${tool?.name || definition.name}配置`}
        className="settings-knowledge-detail-button"
        icon={<RightOutlined />}
        onClick={() => openGroupDestination(group)}
        type="text"
      /> : null}
    </div>;
  };

  const capabilityContent = toolsLoading ? (
    <div className="settings-knowledge-loading"><Skeleton active paragraph={{ rows: 12 }} /></div>
  ) : toolsError ? (
    <Alert
      action={<Button icon={<ReloadOutlined />} onClick={() => void loadTools()}>重试</Button>}
      description="无法读取工具状态，页面未对任何能力做出修改。"
      message="工具状态加载失败"
      showIcon
      type="error"
    />
  ) : (
    <div className="settings-knowledge-groups">
      {toolGroups.map((group) => {
        const registered = group.tools.filter((tool) => toolsByID.has(tool.id)).length;
        const enabled = group.tools.filter((tool) => toolsByID.get(tool.id)?.isEnabled).length;
        return <section className={`settings-knowledge-group is-${group.id}`} key={group.id}>
          <header className="settings-knowledge-group-head">
            <span>{group.icon}</span>
            <div><h2>{group.title}</h2><p>{group.description}</p></div>
            <Tag>{enabled} / {registered} 已启用</Tag>
          </header>
          <div className="settings-knowledge-tool-list">{group.tools.map((tool) => renderTool(tool, group))}</div>
        </section>;
      })}
      <section className="settings-knowledge-group is-parser">
        <header className="settings-knowledge-group-head">
          <span><ApiOutlined /></span>
          <div><h2>文档解析</h2><p>管理新文档解析与重新解析能力，已有文档和配置不会被删除。</p></div>
          <Tag>{documentParsingEnabled ? "1 / 1 已启用" : "0 / 1 已启用"}</Tag>
        </header>
        <div className="settings-knowledge-tool-list">
          <div className="settings-knowledge-tool-row">
            <span className="settings-knowledge-tool-icon" aria-hidden="true"><ApiOutlined /></span>
            <div className="settings-knowledge-tool-copy">
              <strong>文档解析</strong>
              <p>控制新的文档解析任务，关闭后不影响正在运行的任务</p>
            </div>
            <Tag className={`settings-knowledge-state ${documentParsingEnabled ? "is-enabled" : "is-disabled"}`}>{documentParsingEnabled ? "已启用" : "已暂停"}</Tag>
            <Switch
              aria-label="文档解析启用状态"
              checked={documentParsingEnabled}
              className="settings-ref-switch"
              disabled={controlsDisabled}
              loading={documentParsingSaving}
              onChange={onDocumentParsingChange}
            />
            <Button
              aria-label="打开文档解析服务配置"
              className="settings-knowledge-detail-button"
              icon={<RightOutlined />}
              onClick={() => setDetailView({ type: "services", title: "文档解析" })}
              type="text"
            />
          </div>
        </div>
      </section>
    </div>
  );

  const detailContent = detailView?.type === "services"
    ? <div className="settings-knowledge-services">
      <Alert
        message="配置说明"
        description="文档解析、网页搜索和学术搜索在这里配置实际服务。多模态内容识别继续使用模型与服务中的多模态模型。"
        showIcon
        type="info"
        action={<Button onClick={onOpenModels}>配置多模态模型</Button>}
      />
      <ExternalServicesPage includeBuiltinTools={false} includeDependencies={false} includeMcp={false} />
    </div>
    : detailView?.type === "resource"
      ? <div className={`settings-knowledge-resource-panel is-${detailView.resourceTab}`}>
        {detailView.resourceTab === "knowledge" ? <KnowledgeLayout><KnowledgeListPage modelSettingsPath="/settings?section=models" taskCenterPath="/settings?section=tasks" /></KnowledgeLayout> : null}
        {detailView.resourceTab === "cloud" ? <CloudDocumentsPage /> : null}
        {detailView.resourceTab === "database" ? <DatabaseConnectionsPage /> : null}
      </div>
      : null;

  return <section className="settings-knowledge-data">
    <header className="settings-detail-header settings-knowledge-header">
      <div>
        <h1 ref={headingRef} tabIndex={-1}>知识与数据</h1>
        <p>集中管理检索工具、文档处理服务、知识库和外部数据连接；开关状态直接作用于实际运行能力。</p>
      </div>
    </header>
    {capabilityContent}
    {!toolsLoading && !toolsError && managedTools.length === 0 ? <Empty description="当前后端未返回可管理的知识与数据工具" /> : null}
    <Drawer
      className="settings-knowledge-detail-drawer"
      destroyOnClose
      onClose={() => setDetailView(null)}
      open={Boolean(detailView)}
      title={detailView ? `${detailView.title}配置` : "配置"}
      width="min(1120px, calc(100vw - 24px))"
    >
      {detailContent}
    </Drawer>
  </section>;
}
