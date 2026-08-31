import { useCallback, useEffect, useMemo, useState } from "react";
import { Button, Modal, Select, Tooltip, message } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type {
  ListModelProviderGroupModelsOpenAPIItem,
  SelectedModelOpenAPIItem,
} from "@/api/generated/core-client";
import {
  modelProvidersApi,
  unwrapModelProviderData,
} from "@/modules/modelProvider/api";
import { runtimeFeatures } from "@/runtime/features";

type QuickModelCapability = "llm" | "embed_main";

interface SelectedModelWithShare extends SelectedModelOpenAPIItem {
  share?: boolean;
}

interface QuickModelSettingsProps {
  canConfigureEmbedding: boolean;
  onSaved?: () => void | Promise<void>;
}

function modelValue(model: {
  id?: string;
  model_id?: string;
  user_model_provider_group_id: string;
  user_model_provider_id: string;
}) {
  return `${model.user_model_provider_id}:${model.user_model_provider_group_id}:${model.id || model.model_id || ""}`;
}

function modelID(value: string) {
  return value.split(":").slice(2).join(":");
}

function modelLabel(model: { group_name: string; name: string; provider_name: string }) {
  const source = model.group_name || model.provider_name;
  return source ? `${model.name} · ${source}` : model.name;
}

export default function QuickModelSettings({ canConfigureEmbedding, onSaved }: QuickModelSettingsProps) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<Partial<Record<QuickModelCapability, string>>>({});
  const [shared, setShared] = useState<Partial<Record<QuickModelCapability, boolean>>>({});
  const [options, setOptions] = useState<Partial<Record<QuickModelCapability, Array<{ label: string; value: string }>>>>({});
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [saving, setSaving] = useState<QuickModelCapability | null>(null);

  const capabilities = useMemo(() => [
    { key: "llm" as const, title: t("settingsPage.models.llmTitle"), description: t("settingsPage.models.llmDesc") },
    { key: "embed_main" as const, title: t("settingsPage.models.embedTitle"), description: t("settingsPage.models.embedDesc") },
  ], [t]);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError(false);
    try {
      const [selectedResponse, llmResponse, embeddingResponse] = await Promise.all([
        modelProvidersApi.apiCoreModelProvidersSelectedModelsGet(),
        modelProvidersApi.apiCoreModelProvidersModelsGet({ modelType: "llm" }),
        modelProvidersApi.apiCoreModelProvidersModelsGet({ modelType: "embed_main" }),
      ]);
      const selectedItems = unwrapModelProviderData<{ selections?: SelectedModelWithShare[] }>(selectedResponse.data).selections || [];
      const modelLists: Record<QuickModelCapability, ListModelProviderGroupModelsOpenAPIItem[]> = {
        llm: unwrapModelProviderData<{ models?: ListModelProviderGroupModelsOpenAPIItem[] }>(llmResponse.data).models || [],
        embed_main: unwrapModelProviderData<{ models?: ListModelProviderGroupModelsOpenAPIItem[] }>(embeddingResponse.data).models || [],
      };
      const nextSelected: Partial<Record<QuickModelCapability, string>> = {};
      const nextShared: Partial<Record<QuickModelCapability, boolean>> = {};
      const nextOptions: Partial<Record<QuickModelCapability, Array<{ label: string; value: string }>>> = {};

      (["llm", "embed_main"] as QuickModelCapability[]).forEach((key) => {
        const current = selectedItems.find((item) => item.model_key === key);
        const available = modelLists[key].map((item) => ({
          label: modelLabel(item),
          value: modelValue(item),
        }));
        if (current) {
          const value = modelValue(current);
          nextSelected[key] = value;
          nextShared[key] = Boolean(current.share);
          if (!available.some((item) => item.value === value)) {
            available.unshift({ label: modelLabel(current), value });
          }
        }
        nextOptions[key] = available;
      });

      setSelected(nextSelected);
      setShared(nextShared);
      setOptions(nextOptions);
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async (capability: QuickModelCapability, value: string) => {
    const previous = selected[capability];
    setSelected((current) => ({ ...current, [capability]: value }));
    setSaving(capability);
    try {
      await modelProvidersApi.apiCoreModelProvidersSelectedModelsPut({
        setSelectedModelsOpenAPIRequest: {
          selections: [{ model_key: capability, model_id: modelID(value) }],
        },
      });
      await onSaved?.();
      message.success(capability === "llm" ? t("settingsPage.models.llmUpdated") : t("settingsPage.models.embedUpdated"));
    } catch {
      setSelected((current) => ({ ...current, [capability]: previous }));
      message.error(t("settingsPage.models.saveFailed"));
    } finally {
      setSaving(null);
    }
  };

  const requestChange = (capability: QuickModelCapability, value: string) => {
    if (
      !runtimeFeatures.hideUserGroupSurfaces &&
      capability === "embed_main" &&
      selected.embed_main &&
      selected.embed_main !== value &&
      shared.embed_main
    ) {
      Modal.confirm({
        title: t("settingsPage.models.switchSharedTitle"),
        content: t("settingsPage.models.switchSharedContent"),
        okText: t("settingsPage.models.confirmSwitch"),
        cancelText: t("settingsPage.cancel"),
        okButtonProps: { danger: true },
        onOk: () => save(capability, value),
      });
      return;
    }
    void save(capability, value);
  };

  return <>
    {capabilities.map(({ key, title, description }) => <div className="settings-dashboard-config-row settings-dashboard-model-row" key={key}>
      <div className="settings-dashboard-copy"><span>{t("settingsPage.models.moduleLabel")}</span><strong>{title}</strong><p>{description}</p></div>
      <div className="settings-dashboard-control settings-dashboard-model-control">
        {loadError ? <Button size="small" icon={<ReloadOutlined />} onClick={() => void load()}>{t("settingsPage.retry")}</Button> : <Select
          aria-label={t("settingsPage.models.quickConfigAria", { title })}
          className="settings-dashboard-quick-select"
          disabled={saving !== null || (key === "embed_main" && !canConfigureEmbedding)}
          loading={loading || saving === key}
          labelRender={({ label }) => (
            <Tooltip mouseEnterDelay={0} title={label}><span>{label}</span></Tooltip>
          )}
          notFoundContent={t("settingsPage.models.noModels")}
          onChange={(value: string) => requestChange(key, value)}
          optionFilterProp="label"
          optionRender={(option) => (
            <Tooltip mouseEnterDelay={0} title={option.label}><span>{option.label}</span></Tooltip>
          )}
          options={options[key] || []}
          placeholder={key === "embed_main" && !canConfigureEmbedding ? t("settingsPage.models.adminOnly") : t("settingsPage.models.selectModel")}
          showSearch
          value={selected[key]}
        />}
      </div>
    </div>)}
  </>;
}
