import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Drawer, Empty, Input, Modal, Select, Spin, Tooltip, message } from "antd";
import { DownOutlined, MinusOutlined, PlusOutlined, ReloadOutlined, SearchOutlined, DeleteOutlined, FontSizeOutlined, PictureOutlined, ClusterOutlined, AudioOutlined, VideoCameraOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { listRemoteGroupModels, type RemoteGroupModel } from "../api";
import type { ProviderModel } from "../pages/ModelProvidersPage";
import { getProviderLogoUrl } from "../providerBranding";
import { lookupUpstreamModel, type UpstreamModelRecord } from "../upstreamCatalog";
import { ModelModalityMarks } from "./ModelModalityMarks";
import "./ProviderModelCatalogDrawer.scss";

export interface CatalogModelChoice {
  name: string;
  model_type: string;
  max_input_tokens?: string;
  vision?: boolean;
}

interface Props {
  open: boolean;
  providerId: string;
  providerName: string;
  protocolName: string;
  groupId: string;
  models: ProviderModel[];
  modelsLoading?: boolean;
  requiresVerification?: boolean;
  onCheckConnection?: () => void;
  onClose: () => void;
  onRefreshModels: () => void;
  onAdd: (model: CatalogModelChoice) => Promise<void>;
  onRemove: (model: ProviderModel) => Promise<boolean>;
  onChanged?: () => void;
}

type Category = "all" | "unavailable" | "text" | "image" | "embedding" | "audio" | "video";
const categories: Category[] = ["all", "unavailable", "text", "image", "embedding", "audio", "video"];
const normalizedName = (name: string) => name.trim().toLowerCase();

function savedModelType(model: ProviderModel) {
  const types: Record<ProviderModel["capability"], string> = {
    LLM_CHAT: "llm", LLM_SELF_EVOLUTION: "llm", EMBEDDING: "embed", VLM: "vlm",
    RERANK: "rerank", ASR: "stt", TTS: "tts", TEXT_TO_IMAGE: "text2image",
    TEXT_TO_VIDEO: "text2video", MULTIMODAL_EMBEDDING: "multimodal_embedding", IMAGE_EDITING: "image_editing",
  };
  return types[model.capability];
}

function modelCategory(modelType: string): Exclude<Category, "all" | "unavailable"> {
  const value = modelType.toLowerCase();
  if (/embed|rerank/.test(value)) return "embedding";
  if (/video/.test(value)) return "video";
  if (/image|vlm|vision/.test(value)) return "image";
  if (/tts|stt|asr|audio/.test(value)) return "audio";
  return "text";
}

function modelVendor(name: string, fallback: string) {
  const known: Array<[RegExp, string]> = [
    [/deepseek/i, "DeepSeek"], [/claude|anthropic/i, "Anthropic"], [/gpt-|o[134]-|openai|chatgpt/i, "OpenAI"],
    [/gemini|gemma|google/i, "Google"], [/qwen|qwq|qvq/i, "Qwen"], [/doubao|bytedance|seed-/i, "ByteDance"],
    [/^baai\b|bge-/i, "BAAI"], [/llama|meta-/i, "Meta"], [/mistral|mixtral/i, "Mistral"],
    [/glm-|zhipu/i, "GLM"], [/kimi|moonshot/i, "Kimi"], [/minimax/i, "Minimax"],
  ];
  return known.find(([pattern]) => pattern.test(name))?.[1] || (name.includes("/") ? name.split("/")[0] : fallback);
}

function catalogModelType(model?: UpstreamModelRecord) {
  if (model?.output.includes("video")) return "text2video";
  if (model?.output.includes("image")) return "text2image";
  if (model?.output.includes("audio")) return "tts";
  if (model?.input.includes("audio") && model.output.includes("text")) return "stt";
  if (model?.output.includes("text")) return "llm";
  return "";
}

interface Candidate extends CatalogModelChoice {
  existing?: ProviderModel;
  missing: boolean;
  vendor: string;
  unknownType: boolean;
}

export function ProviderModelCatalogDrawer(props: Props) {
  const { t } = useTranslation();
  const copy = (key: string, values?: Record<string, unknown>) => t(`modelProvider.catalog.${key}`, values);
  const [remote, setRemote] = useState<RemoteGroupModel[]>([]);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState<Category>("all");
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [typeOverrides, setTypeOverrides] = useState<Record<string, string>>({});
  const [pending, setPending] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [modal, modalContext] = Modal.useModal();
  const [messageApi, messageContext] = message.useMessage();
  const sequence = useRef(0);

  useEffect(() => {
    if (!props.open) return;
    const request = ++sequence.current;
    setSearch("");
    setCategory("all");
    setCollapsed(new Set());
    setTypeOverrides({});
    setRemote([]);
    setLoaded(false);
    setLoading(true);
    setError("");
    if (props.requiresVerification) {
      setLoading(false);
      return () => { sequence.current += 1; };
    }
    void listRemoteGroupModels(props.providerId, props.groupId).then((data) => {
      if (request !== sequence.current) return;
      setRemote(data.models || []);
      setLoaded(true);
    }).catch((cause) => {
      if (request === sequence.current) setError(getLocalizedErrorMessage(cause));
    }).finally(() => {
      if (request === sequence.current) setLoading(false);
    });
    return () => { sequence.current += 1; };
  }, [props.open, props.providerId, props.groupId, props.requiresVerification, refresh]);

  const candidates = useMemo(() => {
    const saved = new Map(props.models.map((model) => [normalizedName(model.name), model]));
    const unique = new Map<string, Candidate>();
    for (const model of remote) {
      const key = normalizedName(model.name);
      if (!key || unique.has(key)) continue;
      const existing = saved.get(key);
      const upstream = lookupUpstreamModel(props.protocolName, model.name);
      const inferredType = catalogModelType(upstream);
      unique.set(key, {
        name: model.name,
        model_type: typeOverrides[key] || model.model_type || (existing ? savedModelType(existing) : inferredType || "llm"),
        max_input_tokens: model.max_input_tokens,
        vision: upstream?.input.includes("image"),
        existing,
        missing: false,
        vendor: modelVendor(model.name, props.providerName),
        unknownType: !model.model_type && !existing && !inferredType,
      });
    }
    for (const model of props.models) {
      const key = normalizedName(model.name);
      if (!unique.has(key)) unique.set(key, {
        name: model.name, model_type: savedModelType(model), max_input_tokens: model.maxInputTokens,
        vision: model.vision, existing: model, missing: loaded,
        vendor: modelVendor(model.name, props.providerName), unknownType: false,
      });
    }
    return [...unique.values()].sort((left, right) =>
      Number(!(left.existing && !left.existing.builtIn)) - Number(!(right.existing && !right.existing.builtIn)),
    );
  }, [remote, props.models, props.protocolName, props.providerName, loaded, typeOverrides]);

  const matchesCategory = (model: Candidate, target: Category) => target === "all" || (target === "unavailable" ? model.missing : modelCategory(model.model_type) === target);
  const visible = candidates.filter((model) => matchesCategory(model, category) && `${model.name} ${model.vendor}`.toLowerCase().includes(search.trim().toLowerCase()));
  const byVendor = new Map<string, Candidate[]>();
  for (const model of visible) {
    const group = byVendor.get(model.vendor) || [];
    group.push(model);
    byVendor.set(model.vendor, group);
  }
  const groups = [...byVendor];
  const unavailable = candidates.filter((model) => model.missing && model.existing);
  const addable = candidates.filter((model) => !model.existing && !model.missing);

  const runChanges = async (items: Candidate[], remove: boolean) => {
    if (busy || props.modelsLoading || !items.length) return;
    setBusy(true);
    let index = 0;
    let succeeded = 0;
    const failures: string[] = [];
    const worker = async () => {
      while (index < items.length) {
        const model = items[index++];
        const key = normalizedName(model.name);
        setPending((current) => new Set(current).add(key));
        try {
          if (remove && model.existing) {
            if (!await props.onRemove(model.existing)) throw new Error(copy("removeFailed"));
          } else {
            await props.onAdd({ name: model.name, model_type: model.model_type, max_input_tokens: model.max_input_tokens, vision: model.vision });
          }
          succeeded += 1;
        } catch (cause) {
          failures.push(getLocalizedErrorMessage(cause));
        } finally {
          setPending((current) => { const next = new Set(current); next.delete(key); return next; });
        }
      }
    };
    try {
      await Promise.all(Array.from({ length: Math.min(items.length, 4) }, worker));
      if (succeeded) props.onChanged?.();
      if (failures.length) {
        messageApi.error(copy("partialFailure", { success: succeeded, failed: failures.length }));
      } else {
        messageApi.success(copy(remove ? "removed" : "added", { count: succeeded }));
      }
    } finally {
      setBusy(false);
    }
  };

  const removeModels = (items: Candidate[]) => {
    const includesEmbedding = items.some((model) => model.existing?.capability === "EMBEDDING");
    modal.confirm({
      title: copy("confirmRemove", { count: items.length }),
      content: includesEmbedding ? t("modelProvider.confirmDeleteEmbeddingDesc") : copy("removeDescription"),
      okText: t("common.delete"), cancelText: t("common.cancel"), okButtonProps: { danger: true },
      onOk: () => runChanges(items, true),
    });
  };

  const typeOptions = [
    ["llm", "llmChat"], ["embed", "embedding"], ["vlm", "vlm"], ["rerank", "rerank"],
    ["text2image", "textToImage"], ["image_editing", "imageEditing"], ["text2video", "textToVideo"], ["stt", "asr"], ["tts", "tts"],
  ].map(([value, key]) => ({ value, label: t(`modelProvider.capability.${key}`) }));
  const categoryIcons = { text: <FontSizeOutlined aria-hidden="true" />, image: <PictureOutlined aria-hidden="true" />, embedding: <ClusterOutlined aria-hidden="true" />, audio: <AudioOutlined aria-hidden="true" />, video: <VideoCameraOutlined aria-hidden="true" /> };
  const working = busy || loading || props.modelsLoading === true;

  return <Drawer
    className="provider-model-catalog-drawer"
    width="min(660px, 100vw)"
    open={props.open}
    placement="right"
    onClose={() => { if (!busy) props.onClose(); }}
    maskClosable={!busy}
    keyboard={!busy}
    closable={!busy}
    destroyOnHidden
    title={<span className="provider-model-catalog-title">{copy("title", { name: props.providerName })}<small>{candidates.length}</small></span>}
    extra={<div className="provider-model-catalog-toolbar">
      <Tooltip title={copy("clearUnavailableHint")}><Button type="text" icon={<DeleteOutlined aria-hidden="true" />} disabled={working || !unavailable.length || !loaded} onClick={() => removeModels(unavailable)}>{copy("clearUnavailable")}</Button></Tooltip>
      <Button type="text" icon={<PlusOutlined aria-hidden="true" />} disabled={working || !addable.length} onClick={() => void runChanges(addable, false)}>{copy("addAll")}</Button>
    </div>}
  >
    {modalContext}
    {messageContext}
    <div className="provider-model-catalog-search">
      <Input prefix={<SearchOutlined />} allowClear placeholder={copy("search")} aria-label={copy("search")} value={search} onChange={(event) => setSearch(event.target.value)} />
      <Tooltip title={copy("refresh")}><Button aria-label={copy("refresh")} icon={<ReloadOutlined />} loading={loading} disabled={busy || props.requiresVerification} onClick={() => { props.onRefreshModels(); setRefresh((current) => current + 1); }} /></Tooltip>
    </div>
    <div className="provider-model-catalog-tabs" role="tablist" aria-label={copy("filter")}>
      {categories.map((item) => <button key={item} type="button" role="tab" aria-selected={item === category} className={item === category ? "is-active" : ""} onClick={() => setCategory(item)}>
        {item !== "all" && item !== "unavailable" ? categoryIcons[item] : null}{copy(item)}<small>{candidates.filter((model) => matchesCategory(model, item)).length}</small>
      </button>)}
    </div>
    {props.requiresVerification ? <Alert type="info" showIcon message={copy("verifyBeforeSync")} action={<Button size="small" disabled={!props.onCheckConnection} onClick={props.onCheckConnection}>{t("modelProvider.workspace.checkConnection")}</Button>} /> : null}
    {error ? <Alert type="error" showIcon message={t("modelProvider.error.loadRemoteModelsFailed")} description={error} action={<Button size="small" onClick={() => setRefresh((current) => current + 1)}>{t("common.retry")}</Button>} /> : null}
    <Spin spinning={loading || props.modelsLoading}>
      <div className="provider-model-catalog-list" aria-busy={loading || busy}>
        {!loading && !visible.length ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={copy("empty")} /> : null}
        {groups.map(([vendor, models]) => {
          const missing = models.filter((model) => !model.existing && !model.missing);
          const selected = models.filter((model) => model.existing);
          const removing = !missing.length && selected.length > 0;
          return <section className="provider-model-catalog-group" aria-label={vendor} key={vendor}>
            <div className="provider-model-catalog-group-header">
              <button type="button" aria-expanded={!collapsed.has(vendor)} onClick={() => setCollapsed((current) => { const next = new Set(current); if (next.has(vendor)) next.delete(vendor); else next.add(vendor); return next; })}>
                <DownOutlined className={collapsed.has(vendor) ? "is-collapsed" : ""} /><strong>{vendor}</strong><small>{models.length}</small>
              </button>
              <Button type="text" size="small" aria-label={copy(removing ? "removeGroup" : "addGroup", { name: vendor })} icon={removing ? <MinusOutlined aria-hidden="true" /> : <PlusOutlined aria-hidden="true" />} disabled={working || (!missing.length && !selected.length)} onClick={() => removing ? removeModels(selected) : void runChanges(missing, false)} />
            </div>
            {!collapsed.has(vendor) ? models.map((model) => {
              const key = normalizedName(model.name);
              const logo = getProviderLogoUrl(model.vendor) || "/provider-icons/custom.svg";
              return <div className={`provider-model-catalog-row${model.existing ? " is-added" : ""}`} key={key}>
                <span className="provider-model-catalog-logo" aria-hidden="true"><img src={logo} alt="" /></span>
                <div className="provider-model-catalog-model-name"><span title={model.name}>{model.name}</span>{model.missing ? <small>{copy("missingRemote")}</small> : null}</div>
                {model.unknownType ? <Select size="small" className="provider-model-catalog-type" aria-label={copy("modelType", { name: model.name })} value={model.model_type} options={typeOptions} disabled={busy} onChange={(value) => setTypeOverrides((current) => ({ ...current, [key]: value }))} /> : <ModelModalityMarks providerName={props.protocolName} modelName={model.name} compact />}
                <Button type="text" aria-label={copy(model.existing ? "removeModel" : "addModel", { name: model.name })} loading={pending.has(key)} disabled={working} icon={model.existing ? <MinusOutlined aria-hidden="true" /> : <PlusOutlined aria-hidden="true" />} onClick={() => model.existing ? removeModels([model]) : void runChanges([model], false)} />
              </div>;
            }) : null}
          </section>;
        })}
      </div>
    </Spin>
  </Drawer>;
}
