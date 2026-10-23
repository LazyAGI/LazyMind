import { useEffect, useId, useMemo, useRef, useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Empty,
  Input,
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
import { isModelTypeSupportedByProtocol, normalizeRuntimeModelType } from "../modelCapabilities";
import { listRemoteGroupModels, type RemoteGroupModel } from "../api";
import type { ProviderModel } from "../pages/ModelProvidersPage";
import { getModelIdentity } from "../modelIdentity";
import { lookupUpstreamModel } from "../upstreamCatalog";
import {
  availableModelTaskTypes,
  getModelTaskType,
  getModelTaskLabelKey,
  type ModelTaskType,
} from "../modelTaskTypes";
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
  unsupportedType: boolean;
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
  const [pending, setPending] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const busyRequest = useRef(false);
  const [messageApi, messageContext] = message.useMessage();
  const sequence = useRef(0);
  const filterId = useId();
  const tabRefs = useRef(new Map<Category, HTMLButtonElement>());

  useEffect(() => {
    if (!props.open) return;
    const request = ++sequence.current;
    setSearch("");
    setCategory("all");
    setCollapsed(new Set());
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
      const declaredType = normalizeRuntimeModelType(model.model_type?.trim() || upstream?.model_type?.trim() || "");
      const modelType = existing
        ? savedModelType(existing)
        : declaredType;
      unique.set(key, {
        name: existing?.name || model.name,
        model_type: modelType,
        max_input_tokens: existing?.maxInputTokens || model.max_input_tokens,
        vision: existing ? existing.vision : modelType === "llm" && upstream?.input.includes("image"),
        existing,
        missing: false,
        vendor: getModelIdentity(model.name, props.providerName).vendor,
        unsupportedType: Boolean(modelType) && !isModelTypeSupportedByProtocol(modelType, props.protocolName),
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
          unsupportedType: !isModelTypeSupportedByProtocol(savedModelType(model), props.protocolName),
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
  ]);

  const matchesCategory = (model: Candidate, target: Category) =>
    target === "all" ||
    (target === "unavailable"
      ? model.missing
      : getModelTaskType(model.model_type) === target);
  const categoryCounts = useMemo(() => {
    const counts = new Map<Category, number>([
      ["all", candidates.length],
      ["unavailable", candidates.filter((model) => model.missing).length],
    ]);
    for (const model of candidates) {
      const type = getModelTaskType(model.model_type);
      counts.set(type, (counts.get(type) || 0) + 1);
    }
    return counts;
  }, [candidates]);
  const categories = useMemo<Category[]>(
    () => [
      "all",
      "unavailable",
      ...availableModelTaskTypes.filter((type) => categoryCounts.get(type)),
      ...(categoryCounts.get("unknown") ? ["unknown" as const] : []),
    ],
    [categoryCounts],
  );
  useEffect(() => {
    if (!categories.includes(category)) setCategory("all");
  }, [categories, category]);
  const visible = candidates.filter(
    (model) =>
      matchesCategory(model, category) &&
      `${model.name} ${model.vendor}`
        .toLowerCase()
        .includes(search.trim().toLowerCase()),
  );
  const byTask = new Map<
    string,
    { key: string; task: ModelTaskType; custom: boolean; models: Candidate[] }
  >();
  for (const model of visible) {
    const task = getModelTaskType(model.model_type);
    const custom = Boolean(model.existing && !model.existing.builtIn);
    const key = JSON.stringify([custom ? "custom" : "catalog", task]);
    const group = byTask.get(key) || { key, task, custom, models: [] };
    group.models.push(model);
    byTask.set(key, group);
  }
  const groups = [...byTask.values()];
  const unavailable = candidates.filter(
    (model) => model.missing && model.existing,
  );
  const addable = candidates.filter(
    (model) => !model.existing && !model.missing && !model.unsupportedType,
  );

  const runChanges = async (requested: Candidate[], remove: boolean) => {
    const items = remove ? requested : requested.filter((model) => !model.unsupportedType);
    if (busyRequest.current || props.modelsLoading || !items.length) return;
    busyRequest.current = true;
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
              // The API requires an executable type; unclassified models start as
              // editable chat models until the user sets their type in model settings.
              model_type: model.model_type || "llm",
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
      busyRequest.current = false;
      setBusy(false);
    }
  };

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
              onClick={() => void runChanges(unavailable, true)}
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
            id={`${filterId}-${item}`}
            aria-controls={`${filterId}-results`}
            aria-selected={item === category}
            tabIndex={item === category ? 0 : -1}
            ref={(element) => {
              if (element) tabRefs.current.set(item, element);
              else tabRefs.current.delete(item);
            }}
            className={item === category ? "is-active" : ""}
            onClick={() => setCategory(item)}
            onKeyDown={(event) => {
              const current = categories.indexOf(item);
              let next: number;
              if (event.key === "ArrowRight")
                next = (current + 1) % categories.length;
              else if (event.key === "ArrowLeft")
                next = (current + categories.length - 1) % categories.length;
              else if (event.key === "Home") next = 0;
              else if (event.key === "End") next = categories.length - 1;
              else return;
              event.preventDefault();
              setCategory(categories[next]);
              tabRefs.current.get(categories[next])?.focus();
            }}
          >
            {item !== "all" && item !== "unavailable" ? <ModelTypeIcon type={item} size={16} /> : null}
            {item === "all" || item === "unavailable" ? copy(item) : t(getModelTaskLabelKey(item))}
            <small>{categoryCounts.get(item) || 0}</small>
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
        <div
          className="provider-model-catalog-list"
          id={`${filterId}-results`}
          role="tabpanel"
          aria-labelledby={`${filterId}-${category}`}
          aria-busy={working}
          tabIndex={0}
        >
          {!loading && !visible.length ? (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={copy("empty")}
            />
          ) : null}
          {groups.map(({ key: groupKey, task, custom, models }) => {
            const taskLabel = t(getModelTaskLabelKey(task));
            const groupLabel = custom
              ? `${copy("customGroup")} · ${taskLabel}`
              : taskLabel;
            const missing = models.filter(
              (model) => !model.existing && !model.missing && !model.unsupportedType,
            );
            const selected = models.filter((model) => model.existing);
            const removing = !missing.length && selected.length > 0;
            return (
              <section
                className="provider-model-catalog-group"
                aria-label={groupLabel}
                key={groupKey}
              >
                <div className="provider-model-catalog-group-header">
                  <button
                    type="button"
                    aria-label={groupLabel}
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
                    {custom ? (
                      <span className="provider-model-catalog-group-kind">
                        {copy("customGroup")}
                      </span>
                    ) : null}
                  </button>
                  <Button
                    type="text"
                    size="small"
                    aria-label={copy(removing ? "removeGroup" : "addGroup", {
                      name: groupLabel,
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
                        ? void runChanges(selected, true)
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
                            <Tooltip
                              title={model.name}
                              placement="topLeft"
                              trigger={["hover", "focus", "click"]}
                            >
                              <button type="button">{model.name}</button>
                            </Tooltip>
                            {model.missing ? (
                              <small>{copy("missingRemote")}</small>
                            ) : !model.existing && model.unsupportedType ? (
                              <small>{copy("unsupportedType")}</small>
                            ) : null}
                          </div>
                          <ModelTypeBadge type={getModelTaskType(model.model_type)} compact />
                          <Button
                            type="text"
                            aria-label={copy(
                              model.existing ? "removeModel" : "addModel",
                              { name: model.name },
                            )}
                            loading={pending.has(key)}
                            disabled={working || (!model.existing && model.unsupportedType)}
                            icon={
                              model.existing ? (
                                <MinusOutlined aria-hidden="true" />
                              ) : (
                                <PlusOutlined aria-hidden="true" />
                              )
                            }
                            onClick={() =>
                              model.existing
                                ? void runChanges([model], true)
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
