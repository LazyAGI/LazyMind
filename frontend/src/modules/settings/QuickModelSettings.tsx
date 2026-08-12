import { useCallback, useEffect, useState } from "react";
import { Button, Modal, Select, message } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import type {
  ListModelProviderGroupModelsOpenAPIItem,
  SelectedModelOpenAPIItem,
} from "@/api/generated/core-client";
import {
  modelProvidersApi,
  unwrapModelProviderData,
} from "@/modules/modelProvider/api";

type QuickModelCapability = "llm" | "embed_main";

interface SelectedModelWithShare extends SelectedModelOpenAPIItem {
  share?: boolean;
}

interface QuickModelSettingsProps {
  canConfigureEmbedding: boolean;
  onSaved?: () => void | Promise<void>;
}

const capabilities: Array<{
  description: string;
  key: QuickModelCapability;
  title: string;
}> = [
  { key: "llm", title: "大模型（对话）", description: "用于对话与核心推理" },
  { key: "embed_main", title: "向量模型", description: "用于知识库检索召回" },
];

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
  const [selected, setSelected] = useState<Partial<Record<QuickModelCapability, string>>>({});
  const [shared, setShared] = useState<Partial<Record<QuickModelCapability, boolean>>>({});
  const [options, setOptions] = useState<Partial<Record<QuickModelCapability, Array<{ label: string; value: string }>>>>({});
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [saving, setSaving] = useState<QuickModelCapability | null>(null);

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

      capabilities.forEach(({ key }) => {
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
      message.success(capability === "llm" ? "对话模型已更新" : "向量模型已更新");
    } catch {
      setSelected((current) => ({ ...current, [capability]: previous }));
      message.error("模型保存失败，已恢复原选择");
    } finally {
      setSaving(null);
    }
  };

  const requestChange = (capability: QuickModelCapability, value: string) => {
    if (capability === "embed_main" && selected.embed_main && selected.embed_main !== value && shared.embed_main) {
      Modal.confirm({
        title: "切换共享向量模型",
        content: "当前向量模型已被组织共享。切换后，使用该共享配置的知识库检索可能受到影响。",
        okText: "确认切换",
        cancelText: "取消",
        okButtonProps: { danger: true },
        onOk: () => save(capability, value),
      });
      return;
    }
    void save(capability, value);
  };

  return <>
    {capabilities.map(({ key, title, description }) => <div className="settings-dashboard-config-row" key={key}>
      <div className="settings-dashboard-copy"><span>模型与服务</span><strong>{title}</strong><p>{description}</p></div>
      <div className="settings-dashboard-control settings-dashboard-model-control">
        {loadError ? <Button size="small" icon={<ReloadOutlined />} onClick={() => void load()}>重试</Button> : <Select
          aria-label={`${title}快速配置`}
          className="settings-dashboard-quick-select"
          disabled={saving !== null || (key === "embed_main" && !canConfigureEmbedding)}
          loading={loading || saving === key}
          notFoundContent="暂无可用模型"
          onChange={(value) => requestChange(key, value)}
          optionFilterProp="label"
          options={options[key] || []}
          placeholder={key === "embed_main" && !canConfigureEmbedding ? "仅管理员可配置" : "请选择模型"}
          showSearch
          value={selected[key]}
        />}
      </div>
    </div>)}
  </>;
}
