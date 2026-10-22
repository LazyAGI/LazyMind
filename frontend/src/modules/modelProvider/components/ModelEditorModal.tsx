import { useEffect, useRef, useState } from "react";
import { Alert, Form, Input, Modal, Select, Switch } from "antd";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { useSettingsFormDraft } from "@/modules/settings/SettingsNavigationGuard";
import type { ModelCapability, ProviderModel } from "../pages/ModelProvidersPage";
import {
  LLM_MAX_INPUT_TOKENS_MAX_LENGTH,
  isLlmChatCapability,
  parseLlmMaxInputTokens,
  resolveLlmMaxInputTokens,
} from "../maxInputTokens";
import { supportsModelContext } from "../modelCapabilities";
import { getModelTaskType } from "../modelTaskTypes";
import { ModelTypeIcon } from "./ModelTypeBadge";
import "./ModelEditorModal.scss";

export interface ModelEditorValues {
  name: string;
  capability: ModelCapability;
  vision?: boolean;
  maxInputTokens?: string;
}

interface ModelEditorModalProps {
  model: ProviderModel | null;
  providerName: string;
  capabilities: readonly ModelCapability[];
  models: readonly ProviderModel[];
  onSave: (values: ModelEditorValues) => Promise<void>;
  onClose: () => void;
  capabilityLabel: (capability: ModelCapability) => string;
}

function normalizeValues(values: ModelEditorValues): ModelEditorValues {
  return {
    name: values.name?.trim() || "",
    capability: values.capability,
    ...(isLlmChatCapability(values.capability)
      ? { vision: values.vision === true }
      : {}),
    ...(supportsModelContext(values.capability)
      ? { maxInputTokens: values.maxInputTokens?.trim().toUpperCase() || "" }
      : {}),
  };
}

export function ModelEditorModal(props: ModelEditorModalProps) {
  const { t } = useTranslation();
  const text = (key: string, values?: Record<string, unknown>) =>
    t(`modelProvider.editor.${key}`, values);
  const [form] = Form.useForm<ModelEditorValues>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [initialValues, setInitialValues] = useState<ModelEditorValues | null>(null);
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
    const model = currentModel.current;
    if (model) {
      const values: ModelEditorValues = {
        name: model.name,
        capability: model.capability,
        vision: model.vision === true,
        maxInputTokens: resolveLlmMaxInputTokens(model.maxInputTokens),
      };
      form.resetFields();
      form.setFieldsValue(values);
      setInitialValues(normalizeValues(values));
      captureSavedValues();
    } else {
      setInitialValues(null);
    }
    return () => {
      generation.current += 1;
    };
  }, [modelId, form, captureSavedValues]);

  const values = watchedValues || form.getFieldsValue(true);
  const capability = values.capability || props.model?.capability;
  const hasChanges = Boolean(initialValues) &&
    JSON.stringify(normalizeValues(values)) !== JSON.stringify(initialValues);
  const options = [...new Set([
    ...props.capabilities,
    ...(props.model ? [props.model.capability] : []),
  ])].map((option) => ({
    value: option,
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
    const normalized = normalizeValues(submitted);
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
        disabled={saving}
        onFinish={(submitted) => void save(submitted)}
        onValuesChange={() => setError("")}
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
          rules={[{ required: true, message: t("modelProvider.validation.modelTypeRequired") }]}
        >
          <Select aria-label={t("modelProvider.modelType")} options={options} />
        </Form.Item>
        {supportsModelContext(capability) ? (
          <Form.Item
            label={t("modelProvider.maxInputTokensLabel")}
            name="maxInputTokens"
            rules={[
              { required: true, whitespace: true, message: t("modelProvider.validation.maxInputTokensRequired") },
              {
                validator: (_, value?: string) =>
                  !value?.trim() || parseLlmMaxInputTokens(value)
                    ? Promise.resolve()
                    : Promise.reject(new Error(t("modelProvider.validation.maxInputTokensInvalid"))),
              },
            ]}
          >
            <Input aria-label={t("modelProvider.maxInputTokensLabel")} maxLength={LLM_MAX_INPUT_TOKENS_MAX_LENGTH} autoComplete="off" placeholder={t("modelProvider.maxInputTokensPlaceholder")} />
          </Form.Item>
        ) : null}
        {isLlmChatCapability(capability) ? (
          <Form.Item label={t("modelProvider.vision")} name="vision" valuePropName="checked">
            <Switch aria-label={t("modelProvider.vision")} />
          </Form.Item>
        ) : null}
      </Form>
    </Modal>
  );
}

export default ModelEditorModal;
