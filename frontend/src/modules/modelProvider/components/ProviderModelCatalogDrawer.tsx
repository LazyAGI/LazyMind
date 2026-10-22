import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Empty,
  Input,
  Modal,
  Select,
  Spin,
  Tooltip,
  message,
} from "antd";
import {
  DownOutlined,
  MinusOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  DeleteOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { listRemoteGroupModels, type RemoteGroupModel } from "../api";
import type { ProviderModel } from "../pages/ModelProvidersPage";
import { getModelIdentity } from "../modelIdentity";
import { lookupUpstreamModel } from "../upstreamCatalog";
import { availableModelTaskTypes, getModelTaskType, getModelTaskLabelKey, type ModelTaskType } from "../modelTaskTypes";
import { ModelTypeBadge, ModelTypeIcon } from "./ModelTypeBadge";
import { ModelIdentityIcon } from "./ModelIdentityIcon";
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

type Category = "all" | "unavailable" | ModelTaskType;
const categories: Category[] = ["all", "unavailable", ...availableModelTaskTypes, "unknown"];
const normalizedName = (name: string) => name.trim().toLowerCase();

function savedModelType(model: ProviderModel) {
  const types: Record<ProviderModel["capability"], string> = {
    LLM_CHAT: "llm",
    LLM_SELF_EVOLUTION: "llm",
    EMBEDDING: "embed",
    VLM: "vlm",
    RERANK: "rerank",
    ASR: "stt",
    TTS: "tts",
    TEXT_TO_IMAGE: "text2image",
    TEXT_TO_VIDEO: "text2video",
    MULTIMODAL_EMBEDDING: "multimodal_embedding",
    IMAGE_EDITING: "image_editing",
  };
  return types[model.capability];
}

interface Candidate extends CatalogModelChoice {
  existing?: ProviderModel;
  missing: boolean;
  vendor: string;
  unknownType: boolean;
  requiresTypeSelection: boolean;
}

export function ProviderModelCatalogDrawer(props: Props) {
  const { t } = useTranslation();
  const copy = (key: string, values?: Record<string, unknown>) =>
    t(`modelProvider.catalog.${key}`, values);
  const [remote, setRemote] = useState<RemoteGroupModel[]>([]);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState<Category>("all");
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [typeOverrides, setTypeOverrides] = useState<Record<string, string>>(
    {},
  );
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
      return () => {
        sequence.current += 1;
      };
    }
    void listRemoteGroupModels(props.providerId, props.groupId)
      .then((data) => {
        if (request !== sequence.current) return;
        setRemote(data.models || []);
        setLoaded(true);
      })
      .catch((cause) => {
        if (request === sequence.current)
          setError(getLocalizedErrorMessage(cause));
      })
      .finally(() => {
        if (request === sequence.current) setLoading(false);
      });
    return () => {
      sequence.current += 1;
    };
  }, [
    props.open,
    props.providerId,
    props.groupId,
    props.requiresVerification,
    refresh,
  ]);

  const candidates = useMemo(() => {
    const saved = new Map(
      props.models.map((model) => [normalizedName(model.name), model]),
    );
    const unique = new Map<string, Candidate>();
    for (const model of remote) {
      const key = normalizedName(model.name);
      if (!key || unique.has(key)) continue;
      const existing = saved.get(key);
      const upstream = lookupUpstreamModel(props.protocolName, model.name);
      const declaredType = model.model_type?.trim() || upstream?.model_type?.trim() || "";
      const modelType = existing
        ? savedModelType(existing)
        : typeOverrides[key] || declaredType;
      unique.set(key, {
        name: existing?.name || model.name,
        model_type: modelType,
        max_input_tokens: existing?.maxInputTokens || model.max_input_tokens,
        vision: existing ? existing.vision : upstream?.input.includes("image"),
        existing,
        missing: false,
        vendor: getModelIdentity(model.name, props.providerName).vendor,
        unknownType: getModelTaskType(modelType) === "unknown",
        requiresTypeSelection: !existing && getModelTaskType(declaredType) === "unknown",
      });
    }
    for (const model of props.models) {
      const key = normalizedName(model.name);
      if (!unique.has(key))
        unique.set(key, {
          name: model.name,
          model_type: savedModelType(model),
          max_input_tokens: model.maxInputTokens,
          vision: model.vision,
          existing: model,
          missing: loaded,
          vendor: getModelIdentity(model.name, props.providerName).vendor,
          unknownType: getModelTaskType(savedModelType(model)) === "unknown",
          requiresTypeSelection: false,
        });
    }
    return [...unique.values()].sort(
      (left, right) =>
        Number(!(left.existing && !left.existing.builtIn)) -
        Number(!(right.existing && !right.existing.builtIn)),
    );
  }, [
    remote,
    props.models,
    props.protocolName,
    props.providerName,
    loaded,
    typeOverrides,
  ]);

  const matchesCategory = (model: Candidate, target: Category) =>
    target === "all" ||
    (target === "unavailable"
      ? model.missing
      : getModelTaskType(model.model_type) === target);
  const visible = candidates.filter(
    (model) =>
      matchesCategory(model, category) &&
      `${model.name} ${model.vendor}`
        .toLowerCase()
        .includes(search.trim().toLowerCase()),
  );
  const byTask = new Map<string, { key: string; task: ModelTaskType; models: Candidate[] }>();
  for (const model of visible) {
    const task = getModelTaskType(model.model_type);
    const key = JSON.stringify([model.existing && !model.existing.builtIn ? "custom" : "catalog", task]);
    const group = byTask.get(key) || { key, task, models: [] };
    group.models.push(model);
    byTask.set(key, group);
  }
  const groups = [...byTask.values()];
  const unavailable = candidates.filter(
    (model) => model.missing && model.existing,
  );
  const addable = candidates.filter(
    (model) => !model.existing && !model.missing && !model.unknownType,
  );

  const runChanges = async (requested: Candidate[], remove: boolean) => {
    const items = remove ? requested : requested.filter((model) => !model.unknownType);
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
            if (!(await props.onRemove(model.existing)))
              throw new Error(copy("removeFailed"));
          } else {
            await props.onAdd({
              name: model.name,
              model_type: model.model_type,
              max_input_tokens: model.max_input_tokens,
              vision: model.vision,
            });
          }
          succeeded += 1;
        } catch (cause) {
          failures.push(getLocalizedErrorMessage(cause));
        } finally {
          setPending((current) => {
            const next = new Set(current);
            next.delete(key);
            return next;
          });
        }
      }
    };
    try {
      await Promise.all(
        Array.from({ length: Math.min(items.length, 4) }, worker),
      );
      if (succeeded) props.onChanged?.();
      if (failures.length) {
        messageApi.error(
          copy("partialFailure", {
            success: succeeded,
            failed: failures.length,
          }),
        );
      } else {
        messageApi.success(
          copy(remove ? "removed" : "added", { count: succeeded }),
        );
      }
    } finally {
      setBusy(false);
    }
  };

  const removeModels = (items: Candidate[]) => {
    const includesEmbedding = items.some(
      (model) => model.existing?.capability === "EMBEDDING",
    );
    modal.confirm({
      title: copy("confirmRemove", { count: items.length }),
      content: includesEmbedding
        ? t("modelProvider.confirmDeleteEmbeddingDesc")
        : copy("removeDescription"),
      okText: t("common.delete"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: () => runChanges(items, true),
    });
  };

  const typeOptions = [
    ["llm", "llmChat"],
    ["embed", "embedding"],
    ["vlm", "vlm"],
    ["multimodal_embedding", "multimodalEmbedding"],
    ["rerank", "rerank"],
    ["text2image", "textToImage"],
    ["image_editing", "imageEditing"],
    ["text2video", "textToVideo"],
    ["stt", "asr"],
    ["tts", "tts"],
  ].map(([value, key]) => ({
    value,
    label: t(`modelProvider.capability.${key}`),
  }));
  const working = busy || loading || props.modelsLoading === true;

  return (
    <Drawer
      className="provider-model-catalog-drawer"
      width="min(660px, 100vw)"
      open={props.open}
      placement="right"
      onClose={() => {
        if (!busy) props.onClose();
      }}
      maskClosable={!busy}
      keyboard={!busy}
      closable={!busy}
      destroyOnHidden
      title={
        <span className="provider-model-catalog-title">
          <span title={props.providerName}>
            {copy("title", { name: props.providerName })}
          </span>
          <small>{candidates.length}</small>
        </span>
      }
      extra={
        <div className="provider-model-catalog-toolbar">
          <Tooltip title={copy("clearUnavailableHint")}>
            <Button
              type="text"
              icon={<DeleteOutlined aria-hidden="true" />}
              disabled={working || !unavailable.length || !loaded}
              onClick={() => removeModels(unavailable)}
            >
              {copy("clearUnavailable")}
            </Button>
          </Tooltip>
          <Tooltip title={copy("addAll")}>
            <Button
              type="text"
              icon={<PlusOutlined aria-hidden="true" />}
              disabled={working || !addable.length}
              onClick={() => void runChanges(addable, false)}
            >
              {copy("addAll")}
            </Button>
          </Tooltip>
        </div>
      }
    >
      {modalContext}
      {messageContext}
      <div className="provider-model-catalog-search">
        <Input
          prefix={<SearchOutlined aria-hidden="true" />}
          allowClear
          placeholder={copy("search")}
          aria-label={copy("search")}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
        <Tooltip title={copy("refresh")}>
          <Button
            aria-label={copy("refresh")}
            icon={<ReloadOutlined aria-hidden="true" />}
            loading={loading}
            disabled={busy || props.requiresVerification}
            onClick={() => {
              props.onRefreshModels();
              setRefresh((current) => current + 1);
            }}
          />
        </Tooltip>
      </div>
      <div
        className="provider-model-catalog-tabs"
        role="tablist"
        aria-label={copy("filter")}
      >
        {categories.map((item) => (
          <button
            key={item}
            type="button"
            role="tab"
            aria-selected={item === category}
            className={item === category ? "is-active" : ""}
            onClick={() => setCategory(item)}
          >
            {item !== "all" && item !== "unavailable" ? <ModelTypeIcon type={item} size={16} /> : null}
            {item === "all" || item === "unavailable" ? copy(item) : t(getModelTaskLabelKey(item))}
            <small>
              {
                candidates.filter((model) => matchesCategory(model, item))
                  .length
              }
            </small>
          </button>
        ))}
      </div>
      {props.requiresVerification ? (
        <Alert
          type="info"
          showIcon
          message={copy("verifyBeforeSync")}
          action={
            <Button
              size="small"
              disabled={!props.onCheckConnection}
              onClick={props.onCheckConnection}
            >
              {t("modelProvider.workspace.checkConnection")}
            </Button>
          }
        />
      ) : null}
      {error ? (
        <Alert
          type="error"
          showIcon
          message={t("modelProvider.error.loadRemoteModelsFailed")}
          description={error}
          action={
            <Button
              size="small"
              onClick={() => setRefresh((current) => current + 1)}
            >
              {t("common.retry")}
            </Button>
          }
        />
      ) : null}
      <Spin spinning={loading || props.modelsLoading === true}>
        <div className="provider-model-catalog-list" aria-busy={working}>
          {!loading && !visible.length ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={copy("empty")}
            />
          ) : null}
          {groups.map(({ key: groupKey, task, models }) => {
            const taskLabel = t(getModelTaskLabelKey(task));
            const missing = models.filter(
              (model) => !model.existing && !model.missing && !model.unknownType,
            );
            const selected = models.filter((model) => model.existing);
            const removing = !missing.length && selected.length > 0;
            return (
              <section
                className="provider-model-catalog-group"
                aria-label={taskLabel}
                key={groupKey}
              >
                <div className="provider-model-catalog-group-header">
                  <button
                    type="button"
                    aria-label={taskLabel}
                    aria-expanded={!collapsed.has(groupKey)}
                    onClick={() =>
                      setCollapsed((current) => {
                        const next = new Set(current);
                        if (next.has(groupKey)) next.delete(groupKey);
                        else next.add(groupKey);
                        return next;
                      })
                    }
                  >
                    <DownOutlined
                      aria-hidden="true"
                      className={collapsed.has(groupKey) ? "is-collapsed" : ""}
                    />
                    <ModelTypeIcon type={task} size={18} />
                    <strong>{taskLabel}</strong>
                    <small>{models.length}</small>
                  </button>
                  <Button
                    type="text"
                    size="small"
                    aria-label={copy(removing ? "removeGroup" : "addGroup", {
                      name: taskLabel,
                    })}
                    icon={
                      removing ? (
                        <MinusOutlined aria-hidden="true" />
                      ) : (
                        <PlusOutlined aria-hidden="true" />
                      )
                    }
                    disabled={working || (!missing.length && !selected.length)}
                    onClick={() =>
                      removing
                        ? removeModels(selected)
                        : void runChanges(missing, false)
                    }
                  />
                </div>
                {!collapsed.has(groupKey)
                  ? models.map((model) => {
                      const key = normalizedName(model.name);
                      return (
                        <div
                          className={`provider-model-catalog-row${model.existing ? " is-added" : ""}`}
                          key={key}
                        >
                          <ModelIdentityIcon
                            modelName={model.name}
                            fallbackVendor={props.providerName}
                            size={24}
                            className="provider-model-catalog-logo"
                          />
                          <div className="provider-model-catalog-model-name">
                            <Tooltip title={model.name} placement="topLeft">
                              <span>{model.name}</span>
                            </Tooltip>
                            {model.missing ? (
                              <small>{copy("missingRemote")}</small>
                            ) : null}
                          </div>
                          {model.requiresTypeSelection ? (
                            <Select
                              size="small"
                              className="provider-model-catalog-type"
                              aria-label={copy("modelType", { name: model.name })}
                              placeholder={t(getModelTaskLabelKey("unknown"))}
                              value={model.unknownType ? undefined : model.model_type}
                              options={typeOptions}
                              disabled={busy}
                              onChange={(value) => setTypeOverrides((current) => ({ ...current, [key]: value }))}
                            />
                          ) : (
                            <ModelTypeBadge type={getModelTaskType(model.model_type)} compact />
                          )}
                          <Button
                            type="text"
                            aria-label={copy(
                              model.existing ? "removeModel" : "addModel",
                              { name: model.name },
                            )}
                            loading={pending.has(key)}
                            disabled={working || (!model.existing && model.unknownType)}
                            icon={
                              model.existing ? (
                                <MinusOutlined aria-hidden="true" />
                              ) : (
                                <PlusOutlined aria-hidden="true" />
                              )
                            }
                            onClick={() =>
                              model.existing
                                ? removeModels([model])
                                : void runChanges([model], false)
                            }
                          />
                        </div>
                      );
                    })
                  : null}
              </section>
            );
          })}
        </div>
      </Spin>
    </Drawer>
  );
}
