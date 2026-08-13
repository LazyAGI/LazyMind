import { useCallback, useEffect, useMemo, useState } from "react";
import type { Ref } from "react";
import { Alert, Button, Skeleton, Switch, Tag, message } from "antd";
import { ReloadOutlined, ToolOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
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

export default function MemoryCapabilitySettings({ headingRef }: MemoryCapabilitySettingsProps) {
  const { t } = useTranslation();
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
        title: t("settingsPage.memory.vocabulary.title"),
        description: t("settingsPage.memory.vocabulary.description"),
        scope: t("settingsPage.memory.vocabulary.scope"),
        service: t("settingsPage.memory.vocabulary.service"),
        checked: vocabulary.enabled,
        effective: vocabulary.enabled,
        available: vocabulary.available,
        readonly: vocabulary.readonly,
      },
      {
        id: "read" as const,
        title: t("settingsPage.memory.read.title"),
        description: t("settingsPage.memory.read.description"),
        scope: t("settingsPage.memory.read.scope"),
        service: t("settingsPage.memory.read.service"),
        checked: state.personalizationEnabled,
        effective: state.personalizationEnabled,
        available: true,
        readonly: false,
      },
      {
        id: "edit" as const,
        title: t("settingsPage.memory.edit.title"),
        description: t("settingsPage.memory.edit.description"),
        scope: t("settingsPage.memory.edit.scope"),
        service: t("settingsPage.memory.edit.service"),
        checked: memory.enabled,
        effective: state.personalizationEnabled && memory.enabled,
        available: memory.available,
        readonly: memory.readonly,
      },
    ];
  }, [state, t]);

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
      message.success(enabled ? t("settingsPage.memory.enabledToast") : t("settingsPage.memory.disabledToast"));
    } catch {
      setState(previous);
      setRowError(id);
      message.error(t("settingsPage.memory.saveFailed"));
    } finally {
      setUpdating(null);
    }
  };

  return <section className="settings-memory-capabilities" aria-busy={loading}>
    <header className="settings-memory-capabilities-head">
      <span className="settings-memory-capabilities-title-icon" aria-hidden="true"><ToolOutlined /></span>
      <div>
        <h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.memory.title")}</h1>
        <p>{t("settingsPage.memory.description")}</p>
      </div>
      {!loading && !loadError ? <Tag className="settings-memory-count">{t("settingsPage.memory.enabledCount", { count: enabledCount })}</Tag> : null}
    </header>

    {loading ? <div className="settings-memory-loading"><Skeleton active avatar paragraph={{ rows: 4 }} /></div> : null}
    {!loading && loadError ? <Alert
      type="error"
      showIcon
      message={t("settingsPage.memory.loadFailed")}
      description={t("settingsPage.memory.loadFailedDesc")}
      action={<Button size="small" icon={<ReloadOutlined />} onClick={() => void load()}>{t("settingsPage.retry")}</Button>}
    /> : null}
    {!loading && !loadError ? <div className="settings-memory-capability-list">
      {capabilities.map((item) => {
        const suspended = item.id === "edit" && !state?.personalizationEnabled && item.checked;
        const disabled = Boolean(updating) || item.readonly || !item.available || (item.id === "edit" && !state?.personalizationEnabled);
        const statusText = !item.available
          ? t("settingsPage.memory.unavailable")
          : suspended
            ? t("settingsPage.memory.suspendedWithRead")
            : item.effective
              ? t("settingsPage.enabled")
              : t("settingsPage.disabled");
        return <article className={`settings-memory-capability-row${item.effective ? " is-enabled" : " is-disabled"}`} key={item.id}>
          <span className="settings-memory-capability-icon" aria-hidden="true"><ToolOutlined /></span>
          <div className="settings-memory-capability-copy">
            <h2>{item.title}</h2>
            <p>{item.description}</p>
            <span className="settings-memory-capability-meta">{item.scope}<i aria-hidden="true" />{item.service}</span>
            {rowError === item.id ? <span className="settings-memory-capability-error" role="alert">{t("settingsPage.memory.saveFailed")}</span> : null}
          </div>
          <Tag className={`settings-memory-status${item.effective ? " is-enabled" : suspended ? " is-suspended" : ""}`}>{statusText}</Tag>
          <Switch
            className="settings-ref-switch"
            checked={item.checked}
            disabled={disabled}
            loading={updating === item.id}
            onChange={(checked: boolean) => void updateCapability(item.id, checked)}
            aria-label={t("settingsPage.memory.switchAria", { title: item.title })}
          />
        </article>;
      })}
    </div> : null}
    <div className="settings-screenreader-status" role="status" aria-live="polite">{updating ? t("settingsPage.memory.savingStatus") : ""}</div>
  </section>;
}
