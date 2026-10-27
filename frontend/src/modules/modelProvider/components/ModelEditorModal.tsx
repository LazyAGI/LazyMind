import { useEffect, useRef, useState } from "react";
import { Alert, Button, Form, Input, Modal, Select } from "antd";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { useSettingsFormDraft } from "@/modules/settings/SettingsNavigationGuard";
import { useFocusModality } from "../hooks/useFocusModality";
import type { ModelCapability, ProviderModel } from "../pages/ModelProvidersPage";
import {
  LLM_MAX_INPUT_TOKENS_MAX_LENGTH,
  isLlmChatCapability,
  parseLlmMaxInputTokens,
  resolveLlmMaxInputTokens,
  tokenLimitCount,
} from "../maxInputTokens";
import { getRuntimeModelTypeForCapability, isModelTypeSupportedByProtocol, supportsModelContext, supportsStoredModelContext } from "../modelCapabilities";
import { getModelTaskType } from "../modelTaskTypes";
import { ModelTypeIcon } from "./ModelTypeBadge";
import { ModelFeaturesEditor } from "./ModelFeaturesEditor";
import { EmbeddingOptionsEditor } from "./EmbeddingOptionsEditor";
import { declaredFeatureFlags, editableModelSettings, isModelFeatureEnabled, modelFeatures, type EmbeddingEditorValues, type ModelFeature, type ModelSettings } from "../modelSettings";
import "./ModelEditorModal.scss";

export interface ModelEditorValues extends EmbeddingEditorValues {
  name: string;
  capability: ModelCapability;
  vision?: boolean;
  maxInputTokens?: string;
  inputTokenLimit?: string;
  maxOutputTokens?: string;
  modelSettings?: ModelSettings;
}

function TokenLimitInput({ id, value, onChange, label, presets, placeholder, disabled }: {
  id?: string;
  value?: string;
  onChange?: (value: string) => void;
  label: string;
  presets: readonly string[];
  placeholder: string;
  disabled: boolean;
}) {
  return <div className="model-editor-token-control" role="group" aria-label={label}>
    <div className="model-editor-token-presets">
      {presets.map((preset) => <Button key={preset} size="small" shape="round"
        aria-pressed={Boolean(value && tokenLimitCount(value) === tokenLimitCount(preset))}
        disabled={disabled} onClick={() => onChange?.(preset)}>{preset}</Button>)}
    </div>
    <Input id={id} aria-label={label} value={value || ""} onChange={(event) => onChange?.(event.target.value)}
      maxLength={LLM_MAX_INPUT_TOKENS_MAX_LENGTH} autoComplete="off" placeholder={placeholder} disabled={disabled} />
  </div>;
}

interface ModelEditorModalProps {
  model: ProviderModel | null;
  providerName: string;
  protocolName?: string;
  capabilities: readonly ModelCapability[];
  models: readonly ProviderModel[];
  onSave: (values: ModelEditorValues) => Promise<void>;
  onClose: () => void;
  capabilityLabel: (capability: ModelCapability) => string;
}

function normalizeValues(values: ModelEditorValues, preserveEmptySettings = false, touchedFeatures?: ReadonlySet<ModelFeature>): ModelEditorValues {
  const modelSettings = editableModelSettings(values.capability, values.modelSettings, values, preserveEmptySettings, touchedFeatures);
  return {
    name: values.name?.trim() || "",
    capability: values.capability,
    ...(isLlmChatCapability(values.capability)
      ? { vision: isModelFeatureEnabled(modelSettings, "vision", values.vision === true) }
      : {}),
    ...(modelSettings !== undefined ? { modelSettings } : {}),
    ...(supportsStoredModelContext(values.capability)
      ? {
        maxInputTokens: values.maxInputTokens?.trim().toUpperCase() || "",
        ...(supportsModelContext(values.capability) && values.inputTokenLimit !== undefined ? { inputTokenLimit: values.inputTokenLimit.trim().toUpperCase() } : {}),
        ...(supportsModelContext(values.capability) && values.maxOutputTokens !== undefined ? { maxOutputTokens: values.maxOutputTokens.trim().toUpperCase() } : {}),
      }
      : {}),
  };
}

export function ModelEditorModal(props: ModelEditorModalProps) {
  const { t } = useTranslation();
  const focusModality = useFocusModality();
  const text = (key: string, values?: Record<string, unknown>) =>
    t(`modelProvider.editor.${key}`, values);
  const [form] = Form.useForm<ModelEditorValues>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [initialValues, setInitialValues] = useState<ModelEditorValues | null>(null);
  const [touchedFeatures, setTouchedFeatures] = useState<Set<ModelFeature>>(new Set());
  const savingRef = useRef(false);
  const generation = useRef(0);
  // A background refresh of the same model must not replace an unsaved draft.
  const currentModel = useRef(props.model);
  currentModel.current = props.model;
  const modelId = props.model?.id;
  const { captureSavedValues, confirmClose, acceptSaved } = useSettingsFormDraft(
    form,
    Boolean(props.model),
    saving,
  );
  const watchedValues = Form.useWatch([], form) as ModelEditorValues | undefined;

  useEffect(() => {
    generation.current += 1;
    savingRef.current = false;
    setSaving(false);
    setError("");
    setTouchedFeatures(new Set());
    const model = currentModel.current;
    if (model) {
      const values: ModelEditorValues = {
        name: model.name,
        capability: model.capability,
        vision: isModelFeatureEnabled(model.modelSettings, "vision", model.vision === true || model.capability === "VLM"),
        modelSettings: model.modelSettings,
        embeddingQueryPrefix: model.modelSettings?.embedding?.query_prefix,
        embeddingPassagePrefix: model.modelSettings?.embedding?.passage_prefix,
        embeddingBatchSize: model.modelSettings?.embedding?.batch_size ?? undefined,
        maxInputTokens: model.capability === "UNKNOWN" ? model.maxInputTokens?.trim().toUpperCase() || "" : resolveLlmMaxInputTokens(model.maxInputTokens),
        inputTokenLimit: model.inputTokenLimit || undefined,
        maxOutputTokens: model.maxOutputTokens || undefined,
      };
      form.resetFields();
      form.setFieldsValue(values);
      setInitialValues(normalizeValues(values, model.modelSettings !== undefined, new Set()));
      captureSavedValues();
    } else {
      setInitialValues(null);
    }
    return () => {
      generation.current += 1;
    };
  }, [modelId, form, captureSavedValues]);

  const values = { ...form.getFieldsValue(true), ...watchedValues };
  const normalize = (submitted: ModelEditorValues) => {
    const originalSettings = props.model?.modelSettings;
    const submittedFeatureEnabled = declaredFeatureFlags(submitted.modelSettings?.feature_enabled);
    const mergedSettings = originalSettings || submitted.modelSettings
      ? {
        ...originalSettings,
        ...submitted.modelSettings,
        ...(originalSettings?.feature_support && !submitted.modelSettings?.feature_support
          ? { feature_support: originalSettings.feature_support } : {}),
        ...((originalSettings?.feature_enabled || submittedFeatureEnabled)
          ? { feature_enabled: { ...originalSettings?.feature_enabled, ...submittedFeatureEnabled } } : {}),
      }
      : undefined;
    return normalizeValues({ ...submitted, modelSettings: mergedSettings }, props.model?.modelSettings !== undefined, touchedFeatures);
  };
  const capability = values.capability || props.model?.capability;
  const hasChanges = Boolean(initialValues) &&
    JSON.stringify(normalize(values)) !== JSON.stringify(initialValues);
  const options = [...new Set([
    ...props.capabilities,
    ...(props.model ? [props.model.capability] : []),
  ])].map((option) => ({
    value: option,
    disabled: option !== "UNKNOWN" && !isModelTypeSupportedByProtocol(getRuntimeModelTypeForCapability(option), props.protocolName, props.providerName),
    label: (
      <span className="model-editor-type-option">
        <ModelTypeIcon type={getModelTaskType(option)} size={18} />
        {props.capabilityLabel(option)}
      </span>
    ),
  }));

  const close = () => {
    if (!savingRef.current) confirmClose(props.onClose);
  };
  const save = async (submitted: ModelEditorValues) => {
    const normalized = normalize(submitted);
    if (!props.model || savingRef.current ||
      JSON.stringify(normalized) === JSON.stringify(initialValues)) return;
    const operation = generation.current;
    savingRef.current = true;
    setSaving(true);
    setError("");
    try {
      await props.onSave(normalized);
      if (operation !== generation.current) return;
      setInitialValues(normalized);
      captureSavedValues();
      acceptSaved();
      props.onClose();
    } catch (cause) {
      if (operation === generation.current) setError(getLocalizedErrorMessage(cause));
    } finally {
      if (operation === generation.current) {
        savingRef.current = false;
        setSaving(false);
      }
    }
  };

  return (
    <Modal
      rootClassName={`model-provider-focus-${focusModality}`}
      open={Boolean(props.model)}
      title={text("title", { name: props.model?.name || "", provider: props.providerName })}
      className="model-editor-modal"
      width={480}
      centered
      destroyOnHidden
      confirmLoading={saving}
      maskClosable={!saving}
      keyboard={!saving}
      closable={!saving}
      cancelButtonProps={{ disabled: saving }}
      okButtonProps={{ disabled: !hasChanges || saving }}
      okText={text("save")}
      cancelText={t("common.cancel")}
      onCancel={close}
      onOk={() => form.submit()}
    >
      {props.model?.builtIn ? (
        <Alert className="model-editor-info" type="info" showIcon message={text("customizeHint")} />
      ) : null}
      {error ? (
        <Alert className="model-editor-error" type="error" showIcon message={text("loadOrSaveFailed")} description={error} />
      ) : null}
      <Form
        form={form}
        name="edit-provider-model"
        layout="vertical"
        requiredMark={false}
        disabled={saving}
        onFinish={(submitted) => void save(submitted)}
        onValuesChange={(changedValues) => {
          setError("");
          const changedFeatures = changedValues.modelSettings?.feature_enabled;
          if (!changedFeatures || typeof changedFeatures !== "object") return;
          const featureNames = Object.keys(changedFeatures).filter((feature): feature is ModelFeature =>
            (modelFeatures as readonly string[]).includes(feature));
          if (!featureNames.length) return;
          setTouchedFeatures((current) => {
            const next = new Set(current);
            featureNames.forEach((feature) => next.add(feature));
            return next;
          });
        }}
      >
        <Form.Item
          label={text("apiModelId")}
          name="name"
          extra={text("idHint")}
          rules={[
            { required: true, whitespace: true, message: text("nameBytes") },
            {
              validator: (_, value?: string) => {
                const name = value?.trim() || "";
                if (new TextEncoder().encode(name).length > 512)
                  return Promise.reject(new Error(text("nameBytes")));
                if (props.models.some((candidate) =>
                  candidate.id !== props.model?.id &&
                  candidate.name.trim().toLowerCase() === name.toLowerCase()))
                  return Promise.reject(new Error(t("modelProvider.validation.duplicateModelName")));
                return Promise.resolve();
              },
            },
          ]}
        >
          <Input aria-label={text("apiModelId")} className="model-editor-id" maxLength={512} autoComplete="off" spellCheck={false} />
        </Form.Item>
        <Form.Item
          label={t("modelProvider.modelType")}
          name="capability"
          rules={[
            { required: true, message: t("modelProvider.validation.modelTypeRequired") },
            { validator: (_, value?: ModelCapability) => value && ((value === "UNKNOWN" && props.model?.capability === "UNKNOWN") || isModelTypeSupportedByProtocol(getRuntimeModelTypeForCapability(value), props.protocolName))
              ? Promise.resolve() : Promise.reject(new Error(t(value === "UNKNOWN" ? "modelProvider.catalog.unclassifiedType" : "modelProvider.catalog.unsupportedType"))) },
          ]}
        >
          <Select aria-label={t("modelProvider.modelType")} options={options} />
        </Form.Item>
        {supportsStoredModelContext(capability) ? (
          <>
          <Form.Item
            label={t("modelProvider.maxInputTokensLabel")}
            name="maxInputTokens"
            rules={[
              { required: capability !== "UNKNOWN", whitespace: true, message: t("modelProvider.validation.maxInputTokensRequired") },
              {
                validator: (_, value?: string) =>
                  !value?.trim() || parseLlmMaxInputTokens(value)
                    ? Promise.resolve()
                    : Promise.reject(new Error(t("modelProvider.validation.maxInputTokensInvalid"))),
              },
            ]}
          >
            <TokenLimitInput label={t("modelProvider.maxInputTokensLabel")} presets={["128K", "200K", "256K", "400K", "512K", "1M"]}
              placeholder={t("modelProvider.maxInputTokensPlaceholder")} disabled={saving} />
          </Form.Item>
          {supportsModelContext(capability) ? (["inputTokenLimit", "maxOutputTokens"] as const).map((field) => <Form.Item
            key={field} name={field} label={text(field)} dependencies={["maxInputTokens"]}
            rules={[{ validator: (_, value?: string) => {
              if (!value?.trim()) return Promise.resolve();
              const limit = tokenLimitCount(value);
              if (!limit) return Promise.reject(new Error(t("modelProvider.validation.maxInputTokensInvalid")));
              const window = tokenLimitCount(form.getFieldValue("maxInputTokens"));
              return window && limit > window
                ? Promise.reject(new Error(text("tokenLimitExceedsContext"))) : Promise.resolve();
            } }]}
          >
            <TokenLimitInput label={text(field)} disabled={saving}
              presets={field === "inputTokenLimit" ? ["128K", "200K", "256K", "512K", "1M"] : ["16K", "32K", "64K", "128K", "256K"]}
              placeholder={text(field === "inputTokenLimit" ? "inputTokenPlaceholder" : "outputTokenPlaceholder")} />
          </Form.Item>) : null}
          </>
        ) : null}
        {supportsModelContext(capability) ? <ModelFeaturesEditor disabled={saving} legacyVision={props.model?.vision === true} visualModel={capability === "VLM"} /> : null}
        {capability === "EMBEDDING" ? <EmbeddingOptionsEditor disabled={saving} /> : null}
      </Form>
    </Modal>
  );
}

export default ModelEditorModal;
