import { useCallback, useEffect, useMemo, useState } from "react";
import type { Ref } from "react";
import { Alert, Button, Skeleton, Switch, Tag, message } from "antd";
import { ReloadOutlined, ToolOutlined } from "@ant-design/icons";
import {
  Configuration,
  PersonalizationApiFactory,
  type PersonalizationSettingOpenAPIResponse,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";
import {
  disableTool,
  enableTool,
  listToolAssets,
} from "@/modules/memory/toolApi";

type MemoryCapabilityID = "vocabulary" | "read" | "edit";
type ManagedToolID = "vocab_learn" | "memory";

interface ManagedToolState {
  available: boolean;
  enabled: boolean;
  readonly: boolean;
}

interface MemoryCapabilityState {
  personalizationEnabled: boolean;
  tools: Record<ManagedToolID, ManagedToolState>;
}

interface ApiEnvelope<T> {
  data?: T;
}

const personalizationApi = PersonalizationApiFactory(
  new Configuration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

const emptyToolState: ManagedToolState = {
  available: false,
  enabled: false,
  readonly: false,
};

function unwrap<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

async function fetchPersonalizationEnabled() {
  const response = await personalizationApi.apiCorePersonalizationSettingGet();
  return unwrap<PersonalizationSettingOpenAPIResponse>(response.data).enabled;
}

async function updatePersonalizationEnabled(enabled: boolean) {
  const response = await personalizationApi.apiCorePersonalizationSettingPut({
    personalizationSettingOpenAPIRequest: { enabled },
  });
  return unwrap<PersonalizationSettingOpenAPIResponse>(response.data).enabled;
}

interface MemoryCapabilitySettingsProps {
  headingRef?: Ref<HTMLHeadingElement>;
}

interface MemoryEditQuickControlProps {
  onSaved?: () => void | Promise<void>;
}

export function MemoryEditQuickControl({ onSaved }: MemoryEditQuickControlProps) {
  const [enabled, setEnabled] = useState(false);
  const [personalizationEnabled, setPersonalizationEnabled] = useState(false);
  const [available, setAvailable] = useState(false);
  const [readonly, setReadonly] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError(false);
    try {
      const [nextPersonalizationEnabled, assets] = await Promise.all([
        fetchPersonalizationEnabled(),
        listToolAssets({ silentError: true }),
      ]);
      const memory = assets.find((item) => item.id === "memory");
      setPersonalizationEnabled(nextPersonalizationEnabled);
      setAvailable(Boolean(memory));
      setEnabled(Boolean(memory?.isEnabled));
      setReadonly(Boolean(memory?.readonly));
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const update = async (checked: boolean) => {
    const previous = enabled;
    setEnabled(checked);
    setSaving(true);
    try {
      if (checked) await enableTool("memory");
      else await disableTool("memory");
      await onSaved?.();
      message.success(checked ? "记忆编辑已启用" : "记忆编辑已停用");
    } catch {
      setEnabled(previous);
      message.error("保存失败，已恢复原状态");
    } finally {
      setSaving(false);
    }
  };

  if (loadError) {
    return <Button size="small" icon={<ReloadOutlined />} onClick={() => void load()}>重试</Button>;
  }

  return <Switch
    aria-label="记忆编辑快速开关"
    checked={enabled}
    className="settings-ref-switch"
    disabled={loading || saving || !available || readonly || !personalizationEnabled}
    loading={loading || saving}
    onChange={(checked) => void update(checked)}
    title={!personalizationEnabled ? "请先在详细配置中启用记忆读取" : undefined}
  />;
}

export default function MemoryCapabilitySettings({ headingRef }: MemoryCapabilitySettingsProps) {
  const [state, setState] = useState<MemoryCapabilityState | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [updating, setUpdating] = useState<MemoryCapabilityID | null>(null);
  const [rowError, setRowError] = useState<MemoryCapabilityID | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError(false);
    try {
      const [personalizationEnabled, assets] = await Promise.all([
        fetchPersonalizationEnabled(),
        listToolAssets({ silentError: true }),
      ]);
      const findTool = (id: ManagedToolID): ManagedToolState => {
        const tool = assets.find((item) => item.id === id);
        return tool ? {
          available: true,
          enabled: Boolean(tool.isEnabled),
          readonly: Boolean(tool.readonly),
        } : emptyToolState;
      };
      setState({
        personalizationEnabled,
        tools: {
          vocab_learn: findTool("vocab_learn"),
          memory: findTool("memory"),
        },
      });
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const capabilities = useMemo(() => {
    if (!state) return [];
    const vocabulary = state.tools.vocab_learn;
    const memory = state.tools.memory;
    return [
      {
        id: "vocabulary" as const,
        title: "词汇学习",
        description: "学习用户专属的词汇映射和同义词",
        scope: "个人偏好",
        service: "个性化服务",
        checked: vocabulary.enabled,
        effective: vocabulary.enabled,
        available: vocabulary.available,
        readonly: vocabulary.readonly,
      },
      {
        id: "read" as const,
        title: "记忆读取",
        description: "在对话中读取并使用当前用户的记忆与偏好",
        scope: "记忆与对话",
        service: "记忆服务",
        checked: state.personalizationEnabled,
        effective: state.personalizationEnabled,
        available: true,
        readonly: false,
      },
      {
        id: "edit" as const,
        title: "记忆编辑",
        description: "记录和编辑跨会话的用户记忆和偏好",
        scope: "记忆与对话",
        service: "记忆服务",
        checked: memory.enabled,
        effective: state.personalizationEnabled && memory.enabled,
        available: memory.available,
        readonly: memory.readonly,
      },
    ];
  }, [state]);

  const enabledCount = capabilities.filter((item) => item.effective).length;

  const updateCapability = async (id: MemoryCapabilityID, enabled: boolean) => {
    if (!state || updating) return;
    const previous = state;
    setUpdating(id);
    setRowError(null);
    setState((current) => {
      if (!current) return current;
      if (id === "read") return { ...current, personalizationEnabled: enabled };
      const toolID: ManagedToolID = id === "vocabulary" ? "vocab_learn" : "memory";
      return {
        ...current,
        tools: {
          ...current.tools,
          [toolID]: { ...current.tools[toolID], enabled },
        },
      };
    });

    try {
      if (id === "read") {
        const saved = await updatePersonalizationEnabled(enabled);
        setState((current) => current ? { ...current, personalizationEnabled: saved } : current);
      } else {
        const toolID: ManagedToolID = id === "vocabulary" ? "vocab_learn" : "memory";
        if (enabled) await enableTool(toolID);
        else await disableTool(toolID);
      }
      message.success(enabled ? "能力已启用" : "能力已停用");
    } catch {
      setState(previous);
      setRowError(id);
      message.error("保存失败，已恢复原状态");
    } finally {
      setUpdating(null);
    }
  };

  return <section className="settings-memory-capabilities" aria-busy={loading}>
    <header className="settings-memory-capabilities-head">
      <span className="settings-memory-capabilities-title-icon" aria-hidden="true"><ToolOutlined /></span>
      <div>
        <h1 ref={headingRef} tabIndex={-1}>记忆与自进化</h1>
        <p>管理个人词汇学习，以及跨会话记忆的读取、记录和编辑能力。</p>
      </div>
      {!loading && !loadError ? <Tag className="settings-memory-count">{enabledCount} / 3 已启用</Tag> : null}
    </header>

    {loading ? <div className="settings-memory-loading"><Skeleton active avatar paragraph={{ rows: 4 }} /></div> : null}
    {!loading && loadError ? <Alert
      type="error"
      showIcon
      message="无法加载记忆能力设置"
      description="请检查连接后重试。"
      action={<Button size="small" icon={<ReloadOutlined />} onClick={() => void load()}>重试</Button>}
    /> : null}
    {!loading && !loadError ? <div className="settings-memory-capability-list">
      {capabilities.map((item) => {
        const suspended = item.id === "edit" && !state?.personalizationEnabled && item.checked;
        const disabled = Boolean(updating) || item.readonly || !item.available || (item.id === "edit" && !state?.personalizationEnabled);
        const statusText = !item.available
          ? "不可用"
          : suspended
            ? "随记忆读取暂停"
            : item.effective
              ? "已启用"
              : "已停用";
        return <article className={`settings-memory-capability-row${item.effective ? " is-enabled" : " is-disabled"}`} key={item.id}>
          <span className="settings-memory-capability-icon" aria-hidden="true"><ToolOutlined /></span>
          <div className="settings-memory-capability-copy">
            <h2>{item.title}</h2>
            <p>{item.description}</p>
            <span className="settings-memory-capability-meta">{item.scope}<i aria-hidden="true" />{item.service}</span>
            {rowError === item.id ? <span className="settings-memory-capability-error" role="alert">保存失败，已恢复原状态</span> : null}
          </div>
          <Tag className={`settings-memory-status${item.effective ? " is-enabled" : suspended ? " is-suspended" : ""}`}>{statusText}</Tag>
          <Switch
            className="settings-ref-switch"
            checked={item.checked}
            disabled={disabled}
            loading={updating === item.id}
            onChange={(checked) => void updateCapability(item.id, checked)}
            aria-label={`${item.title}开关`}
          />
        </article>;
      })}
    </div> : null}
    <div className="settings-screenreader-status" role="status" aria-live="polite">{updating ? "正在保存记忆能力设置" : ""}</div>
  </section>;
}
