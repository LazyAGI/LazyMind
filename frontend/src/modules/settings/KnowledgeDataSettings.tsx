import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode, RefObject } from "react";
import { Alert, Button, Empty, Skeleton, Switch, Tag, message } from "antd";
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
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  disableTool,
  enableTool,
  listToolAssetsPage,
  notifyToolAvailabilityChanged,
} from "@/modules/memory/toolApi";
import type { StructuredAsset } from "@/modules/memory/shared";
import ExternalServicesPage from "@/modules/modelProvider/pages/ExternalServicesPage";

interface KnowledgeDataSettingsProps {
  documentParsingEnabled: boolean;
  documentParsingSaving: boolean;
  controlsDisabled: boolean;
  headingRef: RefObject<HTMLHeadingElement>;
  onDocumentParsingChange: (enabled: boolean) => void;
}

interface ToolDefinition {
  id: string;
  name: string;
  description: string;
  destination?: string;
}

interface ToolGroupDefinition {
  id: string;
  title: string;
  description: string;
  icon: ReactNode;
  tools: ToolDefinition[];
  destination?: string;
}

export default function KnowledgeDataSettings({
  documentParsingEnabled,
  documentParsingSaving,
  controlsDisabled,
  headingRef,
  onDocumentParsingChange,
}: KnowledgeDataSettingsProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tools, setTools] = useState<StructuredAsset[]>([]);
  const [toolsLoading, setToolsLoading] = useState(true);
  const [toolsError, setToolsError] = useState(false);
  const [pendingTools, setPendingTools] = useState<Set<string>>(new Set());
  const requestSequence = useRef(0);

  const toolGroups = useMemo<ToolGroupDefinition[]>(() => [
    {
      id: "retrieval",
      title: t("settingsPage.knowledge.groups.retrieval.title"),
      description: t("settingsPage.knowledge.groups.retrieval.description"),
      icon: <ReadOutlined />,
      destination: "/lib/knowledge/list",
      tools: [
        { id: "kb", name: t("settingsPage.knowledge.groups.retrieval.kb.name"), description: t("settingsPage.knowledge.groups.retrieval.kb.description") },
        { id: "temp_kb", name: t("settingsPage.knowledge.groups.retrieval.tempKb.name"), description: t("settingsPage.knowledge.groups.retrieval.tempKb.description") },
      ],
    },
    {
      id: "data",
      title: t("settingsPage.knowledge.groups.data.title"),
      description: t("settingsPage.knowledge.groups.data.description"),
      icon: <DatabaseOutlined />,
      destination: "/databases",
      tools: [
        { id: "data_sources", name: t("settingsPage.knowledge.groups.data.dataSources.name"), description: t("settingsPage.knowledge.groups.data.dataSources.description") },
        { id: "external_db", name: t("settingsPage.knowledge.groups.data.externalDb.name"), description: t("settingsPage.knowledge.groups.data.externalDb.description") },
      ],
    },
    {
      id: "file-access",
      title: t("settingsPage.knowledge.groups.fileAccess.title"),
      description: t("settingsPage.knowledge.groups.fileAccess.description"),
      icon: <FolderOpenOutlined />,
      destination: "/cloud-documents",
      tools: [
        { id: "local_fs", name: t("settingsPage.knowledge.groups.fileAccess.localFs.name"), description: t("settingsPage.knowledge.groups.fileAccess.localFs.description") },
        { id: "cloud_files", name: t("settingsPage.knowledge.groups.fileAccess.cloudFiles.name"), description: t("settingsPage.knowledge.groups.fileAccess.cloudFiles.description") },
      ],
    },
    {
      id: "search",
      title: t("settingsPage.knowledge.groups.search.title"),
      description: t("settingsPage.knowledge.groups.search.description"),
      icon: <GlobalOutlined />,
      tools: [
        { id: "web_search", name: t("settingsPage.knowledge.groups.search.webSearch.name"), description: t("settingsPage.knowledge.groups.search.webSearch.description"), destination: "/settings?section=knowledge&tool=web-search" },
        { id: "academic_search", name: t("settingsPage.knowledge.groups.search.academicSearch.name"), description: t("settingsPage.knowledge.groups.search.academicSearch.description"), destination: "/settings?section=knowledge&tool=academic-search" },
        { id: "wikipedia", name: t("settingsPage.knowledge.groups.search.wikipedia.name"), description: t("settingsPage.knowledge.groups.search.wikipedia.description") },
        { id: "url_fetch", name: t("settingsPage.knowledge.groups.search.urlFetch.name"), description: t("settingsPage.knowledge.groups.search.urlFetch.description") },
      ],
    },
    {
      id: "recognition",
      title: t("settingsPage.knowledge.groups.recognition.title"),
      description: t("settingsPage.knowledge.groups.recognition.description"),
      icon: <FileSearchOutlined />,
      destination: "/settings?section=models",
      tools: [
        { id: "multimodal", name: t("settingsPage.knowledge.groups.recognition.multimodal.name"), description: t("settingsPage.knowledge.groups.recognition.multimodal.description") },
      ],
    },
  ], [t]);

  const allToolDefinitions = useMemo(() => toolGroups.flatMap((group) => group.tools), [toolGroups]);

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
      message.success(t("settingsPage.knowledge.toggled", {
        name: tool.name,
        state: enabled ? t("settingsPage.confirm.enableState") : t("settingsPage.confirm.disableState"),
      }));
    } catch {
      message.error(t("settingsPage.knowledge.toggleFailed", { name: tool.name }));
    } finally {
      setPendingTools((current) => {
        const next = new Set(current);
        next.delete(tool.id);
        return next;
      });
    }
  };

  const renderTool = (definition: ToolDefinition, group: ToolGroupDefinition) => {
    const destination = definition.destination || group.destination;
    const tool = toolsByID.get(definition.id);
    const pending = pendingTools.has(definition.id);
    const displayName = tool?.name || definition.name;
    const status = !tool
      ? { label: t("settingsPage.knowledge.unregistered"), className: "is-unavailable" }
      : tool.readonly
        ? { label: t("settingsPage.knowledge.fixedOn"), className: "is-fixed" }
        : tool.isEnabled
          ? { label: t("settingsPage.enabled"), className: "is-enabled" }
          : { label: t("settingsPage.disabled"), className: "is-disabled" };

    return <div className="settings-knowledge-tool-row" key={definition.id}>
      <span className="settings-knowledge-tool-icon" aria-hidden="true">{group.icon}</span>
      <div className="settings-knowledge-tool-copy">
        <strong>{displayName}</strong>
        <p>{tool?.description || definition.description}</p>
      </div>
      <Tag className={`settings-knowledge-state ${status.className}`}>{status.label}</Tag>
      <Switch
        aria-label={t("settingsPage.knowledge.enableAria", { name: displayName })}
        checked={Boolean(tool?.isEnabled)}
        className="settings-ref-switch"
        disabled={!tool || tool.readonly || pending}
        loading={pending}
        onChange={(enabled: boolean) => { if (tool) void toggleTool(tool, enabled); }}
      />
      {destination ? <Button
        aria-label={t("settingsPage.knowledge.openConfigAria", { name: displayName })}
        className="settings-knowledge-detail-button"
        icon={<RightOutlined />}
        onClick={() => navigate(destination)}
        type="text"
      /> : null}
    </div>;
  };

  const capabilityContent = toolsLoading ? (
    <div className="settings-knowledge-loading"><Skeleton active paragraph={{ rows: 12 }} /></div>
  ) : toolsError ? (
    <Alert
      action={<Button icon={<ReloadOutlined />} onClick={() => void loadTools()}>{t("settingsPage.retry")}</Button>}
      description={t("settingsPage.knowledge.loadFailedDesc")}
      message={t("settingsPage.knowledge.loadFailed")}
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
            <Tag>{t("settingsPage.knowledge.enabledCount", { enabled, registered })}</Tag>
          </header>
          <div className="settings-knowledge-tool-list">{group.tools.map((tool) => renderTool(tool, group))}</div>
        </section>;
      })}
      <section className="settings-knowledge-group is-parser">
        <header className="settings-knowledge-group-head">
          <span><ApiOutlined /></span>
          <div>
            <h2>{t("settingsPage.knowledge.documentParsing")}</h2>
            <p>{t("settingsPage.knowledge.documentParsingGroupDesc")}</p>
          </div>
          <div className="settings-knowledge-parser-controls">
            <Tag>{documentParsingEnabled ? t("settingsPage.knowledge.parsingEnabledCount") : t("settingsPage.knowledge.parsingDisabledCount")}</Tag>
            <Switch
              aria-label={t("settingsPage.knowledge.documentParsingAria")}
              checked={documentParsingEnabled}
              className="settings-ref-switch"
              disabled={controlsDisabled}
              loading={documentParsingSaving}
              onChange={onDocumentParsingChange}
            />
          </div>
        </header>
        <div className="settings-knowledge-parser-services">
          <ExternalServicesPage
            includeBuiltinTools={false}
            includeDependencies={false}
            includeMcp={false}
            visibleCategories={["parsing"]}
          />
        </div>
      </section>
    </div>
  );

  return <section className="settings-knowledge-data">
    <header className="settings-detail-header settings-knowledge-header">
      <div>
        <h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.knowledge.title")}</h1>
        <p>{t("settingsPage.knowledge.description")}</p>
      </div>
    </header>
    {capabilityContent}
    {!toolsLoading && !toolsError && managedTools.length === 0 ? <Empty description={t("settingsPage.knowledge.empty")} /> : null}
  </section>;
}
