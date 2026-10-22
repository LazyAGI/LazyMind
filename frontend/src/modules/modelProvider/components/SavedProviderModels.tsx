import { useEffect, useId, useMemo, useState } from "react";
import {
  DownOutlined,
  EyeOutlined,
  MinusOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { Button, Popconfirm, Spin, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import type {
  ModelCapability,
  ProviderModel,
} from "../pages/ModelProvidersPage";
import { getModelIdentity } from "../modelIdentity";
import { sortCustomModelsFirst } from "../modelOrdering";
import { lookupUpstreamModel } from "../upstreamCatalog";
import {
  isLlmChatCapability,
  resolveLlmMaxInputTokens,
} from "../maxInputTokens";
import { ModelIdentityIcon } from "./ModelIdentityIcon";
import { ModelModalityMarks } from "./ModelModalityMarks";
import "./SavedProviderModels.scss";

export interface SavedProviderModelsProps {
  models: ProviderModel[];
  loading: boolean;
  providerName: string;
  supplierName?: string;
  capabilityLabel: (capability: ModelCapability) => string;
  onEdit: (model: ProviderModel) => void;
  onRemove: (model: ProviderModel) => void | Promise<void>;
}

function capabilityClass(capability: ModelCapability) {
  if (
    capability === "EMBEDDING" ||
    capability === "MULTIMODAL_EMBEDDING" ||
    capability === "RERANK"
  )
    return "embedding";
  if (capability === "ASR" || capability === "TTS") return "audio";
  if (
    capability === "TEXT_TO_IMAGE" ||
    capability === "IMAGE_EDITING" ||
    capability === "VLM"
  )
    return "image";
  if (capability === "TEXT_TO_VIDEO") return "video";
  return "other";
}

export function SavedProviderModels(props: SavedProviderModelsProps) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const groupPrefix = useId();
  useEffect(() => {
    setCollapsed(new Set());
  }, [props.providerName, props.supplierName]);
  const groups = useMemo(() => {
    const result = new Map<
      string,
      { key: string; vendor: string; entries: ProviderModel[] }
    >();
    for (const model of sortCustomModelsFirst(props.models)) {
      const vendor = getModelIdentity(model.name, props.supplierName).vendor;
      const key = JSON.stringify([model.builtIn, vendor]);
      const group = result.get(key) || { key, vendor, entries: [] };
      group.entries.push(model);
      result.set(key, group);
    }
    return [...result.values()];
  }, [props.models, props.supplierName]);

  if (props.loading)
    return (
      <div
        className="saved-provider-models-state"
        role="status"
        aria-label={t("common.loading")}
      >
        <Spin size="small" />
      </div>
    );
  if (!props.models.length)
    return (
      <div className="saved-provider-models-state">
        {t("modelProvider.noModels")}
      </div>
    );

  return (
    <div className="saved-provider-models">
      {groups.map(({ key, vendor, entries }, index) => {
        const listId = `${groupPrefix}-${index}`;
        const expanded = !collapsed.has(key);
        return (
          <section
            className="saved-provider-model-group"
            aria-label={vendor}
            key={key}
          >
            <button
              type="button"
              className="saved-provider-model-group-toggle"
              aria-expanded={expanded}
              aria-controls={listId}
              onClick={() =>
                setCollapsed((current) => {
                  const next = new Set(current);
                  if (next.has(key)) next.delete(key);
                  else next.add(key);
                  return next;
                })
              }
            >
              <DownOutlined
                aria-hidden="true"
                className={expanded ? "" : "is-collapsed"}
              />
              <span>{vendor}</span>
              <small>{entries.length}</small>
            </button>
            {expanded ? (
              <ul id={listId} className="saved-provider-model-list">
                {entries.map((model) => {
                  const chat = isLlmChatCapability(model.capability);
                  const vision =
                    model.vision &&
                    chat &&
                    !lookupUpstreamModel(
                      props.providerName,
                      model.name,
                    )?.input.includes("image");
                  return (
                    <li
                      key={model.id}
                      className="saved-provider-model-row"
                      aria-label={model.name}
                    >
                      <ModelIdentityIcon
                        modelName={model.name}
                        fallbackVendor={props.supplierName}
                        size={24}
                      />
                      <Tooltip title={model.name}>
                        <span className="saved-provider-model-name">
                          {model.name}
                        </span>
                      </Tooltip>
                      <span className="saved-provider-model-meta">
                        {!chat ? (
                          <span
                            className={`saved-provider-model-type is-${capabilityClass(model.capability)}`}
                          >
                            {props.capabilityLabel(model.capability)}
                          </span>
                        ) : null}
                        <ModelModalityMarks
                          providerName={props.providerName}
                          modelName={model.name}
                          compact
                        />
                        {vision ? (
                          <Tooltip title={t("modelProvider.visionSupported")}>
                            <span
                              className="saved-provider-model-vision"
                              aria-label={t("modelProvider.visionSupported")}
                            >
                              <EyeOutlined aria-hidden="true" />
                            </span>
                          </Tooltip>
                        ) : null}
                        {chat ? (
                          <span
                            className="saved-provider-model-context"
                            title={`${t("modelProvider.workspace.context")}: ${resolveLlmMaxInputTokens(model.maxInputTokens)}`}
                          >
                            {resolveLlmMaxInputTokens(model.maxInputTokens)}
                          </span>
                        ) : null}
                      </span>
                      <span className="saved-provider-model-actions">
                        {!model.builtIn && chat ? (
                          <Tooltip
                            title={t("modelProvider.editModelWindowAria", {
                              name: model.name,
                            })}
                          >
                            <Button
                              type="text"
                              aria-label={t(
                                "modelProvider.editModelWindowAria",
                                { name: model.name },
                              )}
                              icon={<SettingOutlined aria-hidden="true" />}
                              onClick={() => props.onEdit(model)}
                            />
                          </Tooltip>
                        ) : null}
                        <Popconfirm
                          title={t("modelProvider.confirmDeleteModel", {
                            name: model.name,
                          })}
                          description={t(
                            model.capability === "EMBEDDING"
                              ? "modelProvider.confirmDeleteEmbeddingDesc"
                              : "modelProvider.confirmDeleteModelDesc",
                          )}
                          okText={t("common.delete")}
                          cancelText={t("common.cancel")}
                          okButtonProps={{ danger: true }}
                          onConfirm={() => props.onRemove(model)}
                        >
                          <Tooltip
                            title={t("modelProvider.deleteModelAria", {
                              name: model.name,
                            })}
                          >
                            <Button
                              type="text"
                              aria-label={t("modelProvider.deleteModelAria", {
                                name: model.name,
                              })}
                              icon={<MinusOutlined aria-hidden="true" />}
                            />
                          </Tooltip>
                        </Popconfirm>
                      </span>
                    </li>
                  );
                })}
              </ul>
            ) : null}
          </section>
        );
      })}
    </div>
  );
}
