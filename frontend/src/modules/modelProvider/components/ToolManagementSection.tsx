import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Button,
  Checkbox,
  Drawer,
  Empty,
  Form,
  Input,
  InputNumber,
  Pagination,
  Popconfirm,
  Select,
  Space,
  Spin,
  Switch,
  Tag,
  Tooltip,
  message,
} from "antd";
import { CloudServerOutlined, PlusOutlined, SearchOutlined, ToolOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import type { StructuredAsset } from "@/modules/memory/shared";
import {
  checkMcpServer,
  createMcpServer,
  deleteMcpServer,
  disableTool,
  discoverMcpServerTools,
  enableTool,
  listMcpServersPage,
  listToolAssetsPage,
  updateMcpServer,
  updateMcpServerTools,
  type McpServerAsset,
  type McpServerDraft,
  type McpToolAsset,
} from "@/modules/memory/toolApi";

type ToolView = "builtin" | "mcp";

interface ToolManagementSectionProps {
  view: ToolView;
}

const DEFAULT_TOOL_PAGE_SIZE = 6;
const TOOL_PAGE_SIZE_OPTIONS = [6, 12, 20, 50];

const getMcpActionKey = (action: string, id: string) => `${action}:${id}`;
const getMcpToolId = (tool: McpToolAsset) => tool.id || tool.name;
const normalizeMcpTransportValue = (value?: string) =>
  value === "streamable_http" ? "http" : value || "sse";

const getMcpTransportLabel = (value?: string) => {
  const normalizedValue = normalizeMcpTransportValue(value);
  return normalizedValue === "http" ? "Streamable HTTP" : "SSE";
};

const resolveAllowedMcpToolIds = (server: McpServerAsset, tools: McpToolAsset[]) => {
  const toolIds = tools.map(getMcpToolId).filter(Boolean);
  if (!server.allowedTools) {
    return toolIds;
  }

  const allowedToolSet = new Set(server.allowedTools);
  return toolIds.filter((toolId) => allowedToolSet.has(toolId));
};

export default function ToolManagementSection({ view }: ToolManagementSectionProps) {
  const { t } = useTranslation();
  const [searchInput, setSearchInput] = useState("");
  const [query, setQuery] = useState("");
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_TOOL_PAGE_SIZE);
  const [toolAssets, setToolAssets] = useState<StructuredAsset[]>([]);
  const [toolListTotal, setToolListTotal] = useState(0);
  const [toolLoading, setToolLoading] = useState(false);
  const [toolActionLoading, setToolActionLoading] = useState<Set<string>>(new Set());
  const [mcpServers, setMcpServers] = useState<McpServerAsset[]>([]);
  const [mcpListTotal, setMcpListTotal] = useState(0);
  const [mcpLoading, setMcpLoading] = useState(false);
  const [mcpActionLoading, setMcpActionLoading] = useState<Set<string>>(new Set());
  const [mcpModalOpen, setMcpModalOpen] = useState(false);
  const [mcpModalMode, setMcpModalMode] = useState<"add" | "edit">("add");
  const [mcpEditingServer, setMcpEditingServer] = useState<McpServerAsset | null>(null);
  const [mcpSaving, setMcpSaving] = useState(false);
  const [mcpToolsDrawerOpen, setMcpToolsDrawerOpen] = useState(false);
  const [mcpToolTarget, setMcpToolTarget] = useState<McpServerAsset | null>(null);
  const [mcpToolDraftIds, setMcpToolDraftIds] = useState<string[]>([]);
  const [mcpToolSaving, setMcpToolSaving] = useState(false);
  const [mcpForm] = Form.useForm<McpServerDraft>();

  const listOptions = useMemo(
    () => ({ keyword: query, page: currentPage, pageSize }),
    [currentPage, pageSize, query],
  );

  const markToolActionLoading = useCallback((key: string, loading: boolean) => {
    setToolActionLoading((previous) => {
      const next = new Set(previous);
      if (loading) {
        next.add(key);
      } else {
        next.delete(key);
      }
      return next;
    });
  }, []);

  const markMcpActionLoading = useCallback((key: string, loading: boolean) => {
    setMcpActionLoading((previous) => {
      const next = new Set(previous);
      if (loading) {
        next.add(key);
      } else {
        next.delete(key);
      }
      return next;
    });
  }, []);

  const submitSearch = useCallback((value: string) => {
    setQuery(value.trim());
    setCurrentPage(1);
  }, []);

  const refreshToolAssets = useCallback(async () => {
    setToolLoading(true);
    try {
      const result = await listToolAssetsPage(listOptions);
      setToolAssets(result.records);
      setToolListTotal(result.total);
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("modelProvider.external.toolLoadFailed")) ||
          t("modelProvider.external.toolLoadFailed"),
      );
    } finally {
      setToolLoading(false);
    }
  }, [listOptions, t]);

  const refreshMcpServers = useCallback(async () => {
    setMcpLoading(true);
    try {
      const result = await listMcpServersPage(listOptions);
      setMcpServers(result.records);
      setMcpListTotal(result.total);
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryMcpLoadFailed")) ||
          t("admin.memoryMcpLoadFailed"),
      );
    } finally {
      setMcpLoading(false);
    }
  }, [listOptions, t]);

  useEffect(() => {
    if (view === "builtin") {
      void refreshToolAssets();
      return;
    }
    void refreshMcpServers();
  }, [refreshMcpServers, refreshToolAssets, view]);

  useEffect(() => {
    setCurrentPage(1);
  }, [query, view]);

  const activeTotal = view === "mcp" ? mcpListTotal : toolListTotal;

  useEffect(() => {
    const maxPage = Math.max(1, Math.ceil(activeTotal / pageSize));
    if (currentPage > maxPage) {
      setCurrentPage(maxPage);
    }
  }, [activeTotal, currentPage, pageSize]);

  const handleToggleTool = useCallback(
    async (record: StructuredAsset, checked: boolean) => {
      const actionKey = record.id;
      markToolActionLoading(actionKey, true);
      try {
        if (checked) {
          await enableTool(record.id);
        } else {
          await disableTool(record.id);
        }
        await refreshToolAssets();
        message.success(
          checked
            ? t("admin.memoryToolEnableSuccess")
            : t("admin.memoryToolDisableSuccess"),
        );
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("modelProvider.external.toolToggleFailed")) ||
            t("modelProvider.external.toolToggleFailed"),
        );
      } finally {
        markToolActionLoading(actionKey, false);
      }
    },
    [markToolActionLoading, refreshToolAssets, t],
  );

  const openMcpToolsDrawer = useCallback((server: McpServerAsset) => {
    const tools = server.tools || [];
    setMcpToolTarget(server);
    setMcpToolDraftIds(resolveAllowedMcpToolIds(server, tools));
    setMcpToolsDrawerOpen(true);
  }, []);

  const openMcpCreateModal = useCallback(() => {
    setMcpModalMode("add");
    setMcpEditingServer(null);
    mcpForm.resetFields();
    mcpForm.setFieldsValue({
      name: "",
      url: "",
      transport: "sse",
      apiKey: "",
      timeout: 30,
      enabled: false,
    });
    setMcpModalOpen(true);
  }, [mcpForm]);

  const openMcpEditModal = useCallback(
    (server: McpServerAsset) => {
      setMcpModalMode("edit");
      setMcpEditingServer(server);
      mcpForm.resetFields();
      mcpForm.setFieldsValue({
        name: server.name,
        url: server.url,
        transport: normalizeMcpTransportValue(server.transport),
        apiKey: "",
        timeout: server.timeout,
        enabled: server.enabled,
      });
      setMcpModalOpen(true);
    },
    [mcpForm],
  );

  const closeMcpModal = useCallback(() => {
    if (!mcpSaving) {
      setMcpModalOpen(false);
    }
  }, [mcpSaving]);

  const saveMcpServer = useCallback(async () => {
    try {
      const values = await mcpForm.validateFields();
      const draft: McpServerDraft = {
        name: values.name.trim(),
        url: values.url.trim(),
        transport: normalizeMcpTransportValue(String(values.transport || "sse")),
        apiKey: values.apiKey?.trim() || "",
        timeout: Number(values.timeout || 30),
        enabled:
          mcpModalMode === "edit" && Boolean(mcpEditingServer?.isVerified)
            ? Boolean(values.enabled)
            : false,
      };

      setMcpSaving(true);
      if (mcpModalMode === "edit" && mcpEditingServer) {
        await updateMcpServer(mcpEditingServer.id, draft);
        message.success(t("admin.memoryMcpUpdateSuccess"));
      } else {
        await createMcpServer(draft);
        message.success(t("admin.memoryMcpCreateSuccess"));
      }
      setMcpModalOpen(false);
      await refreshMcpServers();
    } catch (error) {
      if (error && typeof error === "object" && "errorFields" in error) {
        return;
      }
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryMcpSaveFailed")) ||
          t("admin.memoryMcpSaveFailed"),
      );
    } finally {
      setMcpSaving(false);
    }
  }, [mcpEditingServer, mcpForm, mcpModalMode, refreshMcpServers, t]);

  const handleToggleMcpServer = useCallback(
    async (server: McpServerAsset, checked: boolean) => {
      const actionKey = getMcpActionKey("toggle", server.id);
      markMcpActionLoading(actionKey, true);
      try {
        await updateMcpServer(server.id, {
          name: server.name,
          url: server.url,
          transport: normalizeMcpTransportValue(server.transport),
          apiKey: "",
          timeout: server.timeout,
          enabled: checked,
        });
        await refreshMcpServers();
        message.success(
          checked
            ? t("admin.memoryMcpEnableSuccess")
            : t("admin.memoryMcpDisableSuccess"),
        );
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryMcpToggleFailed")) ||
            t("admin.memoryMcpToggleFailed"),
        );
      } finally {
        markMcpActionLoading(actionKey, false);
      }
    },
    [markMcpActionLoading, refreshMcpServers, t],
  );

  const handleCheckMcpServer = useCallback(
    async (server: McpServerAsset) => {
      const actionKey = getMcpActionKey("check", server.id);
      markMcpActionLoading(actionKey, true);
      try {
        const result = await checkMcpServer(server.id);
        if (result.success) {
          message.success(t("admin.memoryMcpCheckResult", { count: result.toolCount }));
        } else {
          message.warning(result.message || t("admin.memoryMcpCheckFailed"));
        }
        await refreshMcpServers();
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryMcpCheckFailed")) ||
            t("admin.memoryMcpCheckFailed"),
        );
      } finally {
        markMcpActionLoading(actionKey, false);
      }
    },
    [markMcpActionLoading, refreshMcpServers, t],
  );

  const handleDiscoverMcpTools = useCallback(
    async (server: McpServerAsset) => {
      const actionKey = getMcpActionKey("discover", server.id);
      markMcpActionLoading(actionKey, true);
      try {
        const result = await discoverMcpServerTools(server.id);
        const nextServer = {
          ...server,
          toolCount: result.tools.length,
          tools: result.tools,
        };
        setMcpServers((previous) =>
          previous.map((item) => (item.id === server.id ? nextServer : item)),
        );
        openMcpToolsDrawer(nextServer);
        message.success(t("admin.memoryMcpDiscoverSuccess", { count: result.tools.length }));
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryMcpDiscoverFailed")) ||
            t("admin.memoryMcpDiscoverFailed"),
        );
      } finally {
        markMcpActionLoading(actionKey, false);
      }
    },
    [markMcpActionLoading, openMcpToolsDrawer, t],
  );

  const handleDeleteMcpServer = useCallback(
    async (server: McpServerAsset) => {
      const actionKey = getMcpActionKey("delete", server.id);
      markMcpActionLoading(actionKey, true);
      try {
        await deleteMcpServer(server.id);
        await refreshMcpServers();
        message.success(t("admin.memoryMcpDeleteSuccess"));
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryMcpDeleteFailed")) ||
            t("admin.memoryMcpDeleteFailed"),
        );
      } finally {
        markMcpActionLoading(actionKey, false);
      }
    },
    [markMcpActionLoading, refreshMcpServers, t],
  );

  const closeMcpToolsDrawer = useCallback(() => {
    if (!mcpToolSaving) {
      setMcpToolsDrawerOpen(false);
    }
  }, [mcpToolSaving]);

  const saveMcpServerTools = useCallback(async () => {
    if (!mcpToolTarget) {
      return;
    }

    setMcpToolSaving(true);
    try {
      await updateMcpServerTools(mcpToolTarget.id, mcpToolDraftIds);
      setMcpToolsDrawerOpen(false);
      await refreshMcpServers();
      message.success(t("admin.memoryMcpToolsSaveSuccess"));
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryMcpToolsSaveFailed")) ||
          t("admin.memoryMcpToolsSaveFailed"),
      );
    } finally {
      setMcpToolSaving(false);
    }
  }, [mcpToolDraftIds, mcpToolTarget, refreshMcpServers, t]);

  const renderManagedToolSummary = (primary?: string, secondary?: string) => {
    const text = [primary, secondary].filter(Boolean).join("\n");
    return (
      <Tooltip
        title={text ? <div className="model-provider-tool-popover-content">{text}</div> : undefined}
        overlayClassName="model-provider-tool-popover"
        placement="topLeft"
      >
        <span className="model-provider-service-summary-wrap">
          <p className="model-provider-service-summary">
            {primary || secondary || t("common.noData")}
          </p>
        </span>
      </Tooltip>
    );
  };

  const renderBuiltInToolCard = (tool: StructuredAsset) => (
    <article className="model-provider-service-card model-provider-managed-tool-card" key={tool.id}>
      <span className="model-provider-service-logo model-provider-service-logo-green">
        <span className="model-provider-service-logo-icon"><ToolOutlined /></span>
      </span>
      <div className="model-provider-service-card-copy">
        <div className="model-provider-service-title-row">
          <h4>{tool.name || tool.id}</h4>
          <Tag className="model-provider-service-status" color={tool.isEnabled ? "success" : "default"}>
            {tool.isEnabled ? t("common.enabled") : t("common.disabled")}
          </Tag>
        </div>
        {renderManagedToolSummary(tool.description, tool.content)}
      </div>
      <div className="model-provider-managed-tool-actions">
        <Switch
          checked={Boolean(tool.isEnabled)}
          checkedChildren={t("common.enabled")}
          disabled={Boolean(tool.readonly)}
          loading={toolActionLoading.has(tool.id)}
          unCheckedChildren={t("common.disabled")}
          onChange={(checked) => {
            void handleToggleTool(tool, checked);
          }}
        />
      </div>
    </article>
  );

  const renderMcpServerCard = (server: McpServerAsset) => {
    const enableDisabled = !server.isVerified && !server.enabled;
    const allowedCount =
      server.allowedTools === undefined ? server.toolCount : server.allowedTools.length;
    const transportLabel = getMcpTransportLabel(server.transport);
    const switchNode = (
      <Switch
        checked={server.enabled}
        checkedChildren={t("common.enabled")}
        disabled={enableDisabled}
        loading={mcpActionLoading.has(getMcpActionKey("toggle", server.id))}
        size="small"
        unCheckedChildren={t("common.disabled")}
        onChange={(checked) => {
          void handleToggleMcpServer(server, checked);
        }}
      />
    );

    return (
      <article className="model-provider-service-card model-provider-managed-tool-card" key={server.id}>
        <span className="model-provider-service-logo model-provider-service-logo-blue">
          <span className="model-provider-service-logo-icon"><CloudServerOutlined /></span>
        </span>
        <div className="model-provider-service-card-copy">
          <div className="model-provider-service-title-row">
            <h4>{server.name}</h4>
          </div>
          <div className="model-provider-managed-tool-status-row">
            <Tag className="model-provider-service-status" color={server.isVerified ? "blue" : "warning"}>
              {server.isVerified ? t("admin.memoryMcpVerified") : t("admin.memoryMcpUnverified")}
            </Tag>
            <Tag className="model-provider-service-status" color={server.enabled ? "success" : "default"}>
              {server.enabled ? t("common.enabled") : t("common.disabled")}
            </Tag>
          </div>
          {renderManagedToolSummary(
            server.url,
            `${transportLabel} · ${t("admin.memoryMcpTimeoutSeconds", { count: server.timeout })} · ${t("admin.memoryMcpAllowedToolsCount", { count: allowedCount })}`,
          )}
        </div>
        <div className="model-provider-managed-tool-actions">
          {enableDisabled ? (
            <Tooltip title={t("admin.memoryMcpEnableRequiresVerified")}>
              <span>{switchNode}</span>
            </Tooltip>
          ) : switchNode}
          <Space className="model-provider-managed-tool-links" size={0} wrap>
            <Button
              loading={mcpActionLoading.has(getMcpActionKey("check", server.id))}
              size="small"
              type="link"
              onClick={() => void handleCheckMcpServer(server)}
            >
              {t("admin.memoryMcpCheck")}
            </Button>
            <Button
              loading={mcpActionLoading.has(getMcpActionKey("discover", server.id))}
              size="small"
              type="link"
              onClick={() => void handleDiscoverMcpTools(server)}
            >
              {t("admin.memoryMcpDiscover")}
            </Button>
            <Button size="small" type="link" onClick={() => openMcpEditModal(server)}>
              {t("common.edit")}
            </Button>
            <Popconfirm
              cancelText={t("common.cancel")}
              okText={t("common.delete")}
              okButtonProps={{
                danger: true,
                loading: mcpActionLoading.has(getMcpActionKey("delete", server.id)),
              }}
              title={t("admin.memoryMcpDeleteConfirm", { name: server.name })}
              onConfirm={() => void handleDeleteMcpServer(server)}
            >
              <Button danger size="small" type="link">
                {t("common.delete")}
              </Button>
            </Popconfirm>
          </Space>
        </div>
      </article>
    );
  };

  const mcpToolIds = (mcpToolTarget?.tools || []).map(getMcpToolId).filter(Boolean);
  const selectedMcpToolSet = new Set(mcpToolDraftIds);
  const allMcpToolsSelected =
    mcpToolIds.length > 0 && mcpToolIds.every((toolId) => selectedMcpToolSet.has(toolId));
  const hasPartialMcpToolsSelected =
    mcpToolIds.some((toolId) => selectedMcpToolSet.has(toolId)) && !allMcpToolsSelected;

  return (
    <section className="model-provider-service-category model-provider-tool-management-section">
      <div className="model-provider-tool-management-head">
        <div className="model-provider-service-category-head model-provider-tool-category-title">
          <span>{view === "mcp" ? <CloudServerOutlined /> : <ToolOutlined />}</span>
          <div>
            <h3>
              {view === "mcp"
                ? t("modelProvider.external.mcpToolManagementTitle")
                : t("modelProvider.external.toolManagementTitle")}
            </h3>
            <p>
              {view === "mcp"
                ? t("modelProvider.external.mcpToolManagementDesc")
                : t("modelProvider.external.toolManagementDesc")}
            </p>
          </div>
        </div>
        {view === "mcp" ? (
          <Button
            className="model-provider-tool-primary-button"
            icon={<PlusOutlined />}
            type="primary"
            onClick={openMcpCreateModal}
          >
            {t("admin.memoryMcpCreateButton")}
          </Button>
        ) : null}
      </div>

      <div className="model-provider-tool-toolbar">
        <Input
          allowClear
          className="model-provider-tool-search"
          placeholder={
            view === "mcp"
              ? t("modelProvider.external.mcpToolSearchPlaceholder")
              : t("modelProvider.external.toolSearchPlaceholder")
          }
          suffix={
            <Tooltip title={t("common.search")}>
              <Button
                aria-label={t("common.search")}
                className="model-provider-tool-search-button"
                icon={<SearchOutlined />}
                size="small"
                type="text"
                onClick={() => submitSearch(searchInput)}
              />
            </Tooltip>
          }
          value={searchInput}
          onChange={(event) => {
            const nextValue = event.target.value;
            setSearchInput(nextValue);
            if (!nextValue.trim()) {
              submitSearch("");
            }
          }}
          onPressEnter={(event) => {
            submitSearch(event.currentTarget.value);
          }}
        />
      </div>

      <Spin spinning={view === "mcp" ? mcpLoading : toolLoading}>
        {activeTotal === 0 && !(view === "mcp" ? mcpLoading : toolLoading) ? (
          <div className="model-provider-managed-tool-empty">
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={view === "mcp" ? t("admin.memoryMcpEmpty") : t("admin.memoryEmpty")}
            />
          </div>
        ) : (
          <div className="model-provider-service-grid model-provider-managed-tool-grid">
            {view === "mcp"
              ? mcpServers.map((server) => renderMcpServerCard(server))
              : toolAssets.map((tool) => renderBuiltInToolCard(tool))}
          </div>
        )}
      </Spin>

      {activeTotal > 0 ? (
        <Pagination
          className="model-provider-managed-tool-pagination"
          current={currentPage}
          pageSize={pageSize}
          pageSizeOptions={TOOL_PAGE_SIZE_OPTIONS.map(String)}
          showSizeChanger
          showTotal={(total) => t("common.totalItems", { total })}
          total={activeTotal}
          onChange={(page, nextPageSize) => {
            setCurrentPage(page);
            setPageSize(nextPageSize);
          }}
          onShowSizeChange={(_current, nextPageSize) => {
            setCurrentPage(1);
            setPageSize(nextPageSize);
          }}
        />
      ) : null}

      <Drawer
        className="model-provider-mcp-drawer"
        destroyOnHidden
        footer={
          <div className="model-provider-mcp-drawer-footer">
            <Button onClick={closeMcpModal}>{t("common.cancel")}</Button>
            <Button loading={mcpSaving} type="primary" onClick={() => void saveMcpServer()}>
              {t("common.save")}
            </Button>
          </div>
        }
        open={mcpModalOpen}
        title={
          mcpModalMode === "add"
            ? t("admin.memoryMcpCreateTitle")
            : t("admin.memoryMcpEditTitle")
        }
        width={560}
        onClose={closeMcpModal}
      >
        <Form<McpServerDraft>
          className="model-provider-mcp-form"
          form={mcpForm}
          layout="vertical"
        >
          <Form.Item
            label={t("admin.memoryMcpName")}
            name="name"
            rules={[
              {
                required: true,
                whitespace: true,
                message: t("admin.memoryMcpNameRequired"),
              },
            ]}
          >
            <Input maxLength={80} placeholder={t("admin.memoryMcpNamePlaceholder")} />
          </Form.Item>
          <Form.Item
            label={t("admin.memoryMcpUrl")}
            name="url"
            rules={[
              {
                required: true,
                whitespace: true,
                message: t("admin.memoryMcpUrlRequired"),
              },
              { type: "url", message: t("admin.memoryMcpUrlInvalid") },
            ]}
          >
            <Input placeholder="https://example.com/mcp" />
          </Form.Item>
          <div className="model-provider-mcp-form-grid">
            <Form.Item
              extra={
                mcpModalMode === "edit"
                  ? t("admin.memoryMcpTransportEditHint")
                  : undefined
              }
              label={t("admin.memoryMcpTransport")}
              name="transport"
              rules={[
                {
                  required: true,
                  message: t("admin.memoryMcpTransportRequired"),
                },
              ]}
            >
              <Select
                disabled={mcpModalMode === "edit"}
                options={[
                  { label: "SSE", value: "sse" },
                  { label: "Streamable HTTP", value: "http" },
                ]}
              />
            </Form.Item>
            <Form.Item
              label={t("admin.memoryMcpTimeout")}
              name="timeout"
              rules={[
                {
                  required: true,
                  message: t("admin.memoryMcpTimeoutRequired"),
                },
              ]}
            >
              <InputNumber max={600} min={1} placeholder="30" />
            </Form.Item>
          </div>
          <Form.Item
            extra={
              mcpModalMode === "edit"
                ? t("admin.memoryMcpApiKeyEditHint", {
                    preview:
                      mcpEditingServer?.apiKeyPreview ||
                      t("admin.memoryMcpApiKeyHidden"),
                  })
                : undefined
            }
            label={t("admin.memoryMcpApiKey")}
            name="apiKey"
            rules={
              mcpModalMode === "add"
                ? [
                    {
                      required: true,
                      whitespace: true,
                      message: t("admin.memoryMcpApiKeyRequired"),
                    },
                  ]
                : []
            }
          >
            <Input.Password
              autoComplete="new-password"
              placeholder={
                mcpModalMode === "add"
                  ? t("admin.memoryMcpApiKeyPlaceholder")
                  : t("admin.memoryMcpApiKeyEditPlaceholder")
              }
            />
          </Form.Item>
          <Form.Item
            label={t("admin.memoryMcpEnabled")}
            name="enabled"
            tooltip={
              mcpModalMode === "add" ||
              (mcpModalMode === "edit" && !mcpEditingServer?.isVerified)
                ? t("admin.memoryMcpEnableRequiresVerified")
                : undefined
            }
            valuePropName="checked"
          >
            <Switch
              checkedChildren={t("common.enabled")}
              disabled={
                mcpModalMode === "add" ||
                (mcpModalMode === "edit" && !mcpEditingServer?.isVerified)
              }
              unCheckedChildren={t("common.disabled")}
            />
          </Form.Item>
        </Form>
      </Drawer>

      <Drawer
        className="model-provider-mcp-drawer"
        footer={
          <div className="model-provider-mcp-drawer-footer">
            <Button onClick={closeMcpToolsDrawer}>{t("common.cancel")}</Button>
            <Button
              disabled={!mcpToolTarget?.tools.length}
              loading={mcpToolSaving}
              type="primary"
              onClick={() => void saveMcpServerTools()}
            >
              {t("common.save")}
            </Button>
          </div>
        }
        open={mcpToolsDrawerOpen}
        title={t("admin.memoryMcpToolsTitle", { name: mcpToolTarget?.name || "" })}
        width={620}
        onClose={closeMcpToolsDrawer}
      >
        {mcpToolTarget ? (
          <div className="model-provider-mcp-tools-panel">
            <div className="model-provider-mcp-tools-summary">
              <div>
                <strong>{mcpToolTarget.name}</strong>
                <span>{mcpToolTarget.url}</span>
              </div>
              <Tag color={mcpToolTarget.isVerified ? "blue" : "warning"}>
                {mcpToolTarget.isVerified
                  ? t("admin.memoryMcpVerified")
                  : t("admin.memoryMcpUnverified")}
              </Tag>
            </div>
            {mcpToolTarget.tools.length ? (
              <>
                <Checkbox
                  checked={allMcpToolsSelected}
                  indeterminate={hasPartialMcpToolsSelected}
                  onChange={(event) =>
                    setMcpToolDraftIds(event.target.checked ? mcpToolIds : [])
                  }
                >
                  {t("admin.memoryMcpSelectAllTools")}
                </Checkbox>
                <Checkbox.Group
                  className="model-provider-mcp-tool-group"
                  value={mcpToolDraftIds}
                  onChange={(values) => setMcpToolDraftIds(values.map(String))}
                >
                  {mcpToolTarget.tools.map((toolItem) => {
                    const toolId = getMcpToolId(toolItem);
                    return (
                      <div className="model-provider-mcp-tool-option" key={toolId}>
                        <Checkbox value={toolId} />
                        <div className="model-provider-mcp-tool-option-copy">
                          <strong>{toolItem.name || toolId}</strong>
                          <span>{toolItem.description || "-"}</span>
                        </div>
                      </div>
                    );
                  })}
                </Checkbox.Group>
              </>
            ) : (
              <div className="model-provider-mcp-empty-tools">
                <CloudServerOutlined />
                <strong>{t("admin.memoryMcpNoToolsTitle")}</strong>
                <span>{t("admin.memoryMcpNoToolsDesc")}</span>
              </div>
            )}
          </div>
        ) : null}
      </Drawer>
    </section>
  );
}
