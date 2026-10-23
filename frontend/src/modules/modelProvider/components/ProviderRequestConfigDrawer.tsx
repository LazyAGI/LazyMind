import { useEffect, useId, useRef, useState } from "react";
import { Alert, Button, Drawer, Form, Input, Spin, Tag, message } from "antd";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { useSettingsFormDraft } from "@/modules/settings/SettingsNavigationGuard";
import { useFocusModality } from "../hooks/useFocusModality";
import {
  getProviderRequestConfig,
  updateProviderRequestConfig,
  type ProviderRequestConfig,
  type ProviderRequestConfigInput,
  type RequestProtocol,
} from "../api";
import "./ProviderRequestConfigDrawer.scss";
import { ProviderRequestAdvancedFields, advancedRequestFormValues, advancedRequestInput, type AdvancedRequestFormValues } from "./ProviderRequestAdvancedFields";

interface ProviderRequestConfigDrawerProps {
  providerId: string;
  providerName: string;
  open: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
}

type FormValues = {
  default_protocol: RequestProtocol;
  endpoints: { openai: string; anthropic: string };
  advanced: AdvancedRequestFormValues;
};

function editableProtocol(protocol: RequestProtocol): protocol is "openai" | "anthropic" {
  return protocol === "openai" || protocol === "anthropic";
}

export function ProviderRequestConfigDrawer({
  providerId,
  providerName,
  open,
  onClose,
  onSaved,
}: ProviderRequestConfigDrawerProps) {
  const {t} = useTranslation();
  const focusModality = useFocusModality();
  const text = (key: string, options?: Record<string, unknown>) => t(`modelProvider.requestConfig.${key}`, options);
  const [form] = Form.useForm<FormValues>();
  const [config, setConfig] = useState<ProviderRequestConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  const requestSequence = useRef(0);
  const fieldPrefix = useId();
  const defaultProtocol = Form.useWatch("default_protocol", form);
  const draft = useSettingsFormDraft(form, open && Boolean(config) && !loadError, saving);

  useEffect(() => {
    const sequence = ++requestSequence.current;
    let active = true;
    setConfig(null);
    setLoadError(null);
    setSaving(false);
    form.resetFields();
    if (!open || !providerId) {
      setLoading(false);
      return;
    }
    setLoading(true);
    void getProviderRequestConfig(providerId).then((nextConfig) => {
      if (!active || sequence !== requestSequence.current) return;
      const endpoints = {openai: "", anthropic: ""};
      for (const item of nextConfig.protocols) {
        if (editableProtocol(item.protocol)) endpoints[item.protocol] = item.base_url;
      }
      form.setFieldsValue({default_protocol: nextConfig.default_protocol, endpoints, advanced: advancedRequestFormValues(nextConfig.advanced)});
      draft.captureSavedValues();
      setConfig(nextConfig);
    }).catch((error) => {
      if (active && sequence === requestSequence.current) setLoadError(getLocalizedErrorMessage(error));
    }).finally(() => {
      if (active && sequence === requestSequence.current) setLoading(false);
    });
    return () => {
      active = false;
      ++requestSequence.current;
    };
  }, [open, providerId, retry, form, draft.captureSavedValues]);

  const validateEndpoint = (_rule: unknown, value?: string) => {
    const endpoint = value?.trim() || "";
    if (!endpoint) return Promise.resolve();
    if (endpoint.length > 512) return Promise.reject(new Error(text("baseUrlMax")));
    try {
      const url = new URL(endpoint);
      if (!/^https?:$/.test(url.protocol) || !url.hostname || url.search || url.hash) {
        return Promise.reject(new Error(text("baseUrlInvalid")));
      }
      if (url.username || url.password) return Promise.reject(new Error(text("baseUrlNoCredentials")));
    } catch {
      return Promise.reject(new Error(text("baseUrlInvalid")));
    }
    return Promise.resolve();
  };

  const save = async (values: FormValues) => {
    if (saving || loading || !config || loadError) return;
    const sequence = requestSequence.current;
    const body: ProviderRequestConfigInput = {
      default_protocol: values.default_protocol,
      endpoints: {
        openai: values.endpoints.openai?.trim() || "",
        anthropic: values.endpoints.anthropic?.trim() || "",
      },
      advanced: advancedRequestInput(values.advanced),
    };
    setSaving(true);
    try {
      await updateProviderRequestConfig(providerId, body);
      if (sequence !== requestSequence.current) return;
      draft.captureSavedValues();
      draft.acceptSaved();
      message.success(text("saved"));
      await onSaved();
      if (sequence === requestSequence.current) onClose();
    } catch (error) {
      if (sequence === requestSequence.current) message.error(getLocalizedErrorMessage(error));
    } finally {
      if (sequence === requestSequence.current) setSaving(false);
    }
  };

  const close = () => {
    if (!saving) draft.confirmClose(onClose);
  };

  return <Drawer
    rootClassName={`model-provider-focus-${focusModality}`}
    className="provider-request-config-drawer"
    title={text("title")}
    open={open}
    width="min(400px, 100vw)"
    onClose={close}
    closable={!saving}
    maskClosable={!saving}
    keyboard={!saving}
    forceRender
    footer={<div className="provider-request-config-footer">
      <Button disabled={saving} onClick={close}>{text("cancel")}</Button>
      <Button type="primary" loading={saving} disabled={loading || Boolean(loadError) || !config} onClick={() => form.submit()}>{text("save")}</Button>
    </div>}
  >
    <div className="provider-request-config-content" aria-label={providerName}>
      {loadError ? <Alert
        type="error"
        showIcon
        message={text("loadFailed")}
        description={loadError}
        action={<Button size="small" onClick={() => setRetry((value) => value + 1)}>{text("retry")}</Button>}
      /> : null}
      <Spin spinning={loading}>
        <Form form={form} layout="vertical" onFinish={(values) => void save(values)} disabled={loading || saving || Boolean(loadError)}>
          <Form.Item name="default_protocol" hidden><Input /></Form.Item>
          {config?.protocols.map((item) => {
            const supported = item.supported && editableProtocol(item.protocol);
            const fieldId = `${fieldPrefix}-${item.protocol}`;
            return <section key={item.protocol} className="provider-request-config-protocol" aria-label={item.name}>
              <div className="provider-request-config-label">
                <label htmlFor={fieldId}>{item.name}</label>
                {defaultProtocol === item.protocol ? <Tag>{text("default")}</Tag> : supported ? <Button
                  size="small"
                  type="text"
                  disabled={saving}
                  onClick={() => form.setFieldValue("default_protocol", item.protocol)}
                >{text("setDefault")}</Button> : null}
              </div>
              {supported ? <Form.Item name={["endpoints", item.protocol]} rules={[{validator: validateEndpoint}]}>
                <Input id={fieldId} aria-label={item.name} autoComplete="off" placeholder={item.default_base_url} maxLength={512} />
              </Form.Item> : <Input id={fieldId} aria-label={item.name} disabled value={item.base_url || item.default_base_url} />}
              <p className="provider-request-config-hint">{text(supported ? "endpointHint" : "unsupported")}</p>
              {supported && item.default_base_url ? <p className="provider-request-config-default-address">{text("defaultAddress", {address: item.default_base_url})}</p> : null}
            </section>;
          })}
          {config ? <ProviderRequestAdvancedFields form={form} /> : null}
        </Form>
      </Spin>
    </div>
  </Drawer>;
}

export default ProviderRequestConfigDrawer;
