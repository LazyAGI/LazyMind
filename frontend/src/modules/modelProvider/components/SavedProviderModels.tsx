import { useRef, useState } from "react";
import {
  MinusOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { Button, Spin, Tooltip, message } from "antd";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import type {
  ModelCapability,
  ProviderModel,
} from "../pages/ModelProvidersPage";
import { sortCustomModelsFirst } from "../modelOrdering";
import { lookupUpstreamModel } from "../upstreamCatalog";
import {
  getModelTaskType,
  getModelTaskDescriptionKey,
} from "../modelTaskTypes";
import {
  isLlmChatCapability,
  resolveLlmMaxInputTokens,
} from "../maxInputTokens";
import { ModelIdentityIcon } from "./ModelIdentityIcon";
import { ModelTypeBadge } from "./ModelTypeBadge";
import "./SavedProviderModels.scss";

export interface SavedProviderModelsProps {
  models: ProviderModel[];
  loading: boolean;
  providerName: string;
  supplierName?: string;
  capabilityLabel: (capability: ModelCapability) => string;
  onEdit: (model: ProviderModel) => void;
  onRemove: (model: ProviderModel) => void | boolean | Promise<void | boolean>;
}

export function SavedProviderModels(props: SavedProviderModelsProps) {
  const { t } = useTranslation();
  const pending = useRef(new Set<string>());
  const [removing, setRemoving] = useState<Set<string>>(new Set());
  const remove = async (model: ProviderModel) => {
    if (pending.current.has(model.id)) return;
    pending.current.add(model.id);
    setRemoving(new Set(pending.current));
    try {
      await props.onRemove(model);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      pending.current.delete(model.id);
      setRemoving(new Set(pending.current));
    }
  };
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
    <ul className="saved-provider-models saved-provider-model-list">
      {sortCustomModelsFirst(props.models).map((model) => {
        const type = getModelTaskType(model.capability);
        const chat = isLlmChatCapability(model.capability);
        const showContext =
          chat ||
          (type === "chat" && Boolean(model.maxInputTokens?.trim()));
        const vision = model.capability === "VLM" || (
          model.vision !== undefined ? model.vision :
            lookupUpstreamModel(props.providerName, model.name)?.input.includes("image")
        );
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
            <Tooltip
              trigger={["hover", "focus", "click"]}
              title={
                <div className="saved-provider-model-detail">
                  <strong>{model.name}</strong>
                  <span>
                    {props.capabilityLabel(model.capability)}
                  </span>
                  <span>{t(getModelTaskDescriptionKey(type))}</span>
                  {vision ? (
                    <span>{t("modelProvider.visionSupported")}</span>
                  ) : null}
                  {showContext ? (
                    <span>
                      {t("modelProvider.workspace.context")}:{" "}
                      {resolveLlmMaxInputTokens(model.maxInputTokens)}
                    </span>
                  ) : null}
                </div>
              }
            >
              <button
                type="button"
                className="saved-provider-model-name"
              >
                {model.name}
              </button>
            </Tooltip>
            <span className="saved-provider-model-task">
              <ModelTypeBadge type={type} compact />
            </span>
            <span className="saved-provider-model-actions">
              <Tooltip
                title={t("modelProvider.editModelAria", {
                  name: model.name,
                })}
              >
                <Button
                  type="text"
                  aria-label={t("modelProvider.editModelAria", {
                    name: model.name,
                  })}
                  icon={<SettingOutlined aria-hidden="true" />}
                  disabled={removing.has(model.id)}
                  onClick={() => props.onEdit(model)}
                />
              </Tooltip>
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
                  loading={removing.has(model.id)}
                  disabled={removing.has(model.id)}
                  onClick={() => void remove(model)}
                />
              </Tooltip>
            </span>
          </li>
        );
      })}
    </ul>
  );
}
