import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { AutoComplete, Button, Form, Input, Modal, Space, Tag, Tooltip, message } from "antd";
import {
  CopyOutlined,
  DeleteOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { BASE_URL, axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import type { RawAxiosRequestConfig } from "axios";

import "../index.scss";

export type ExternalServiceCategory = "parsing" | "tools";
export type ExternalServiceProviderCategory = "ocr" | "search" | "datasource";
export type ExternalServiceTone = "blue" | "cyan" | "green" | "red" | "violet";

export interface ExternalServiceConfigModalService {
  key: string;
  name: string;
  description: string;
  fields: Array<"baseUrl" | "apiKey" | "searchEngineId">;
  logo: ReactNode;
  logoUrl: string;
  tone: ExternalServiceTone;
  status: "configured" | "missing" | "tbd";
  category?: ExternalServiceCategory;
  providerCategory?: ExternalServiceProviderCategory;
  baseUrl?: string;
  baseUrlPresets?: Array<{
    labelKey: string;
    descKey: string;
    value: string;
  }>;
}

interface ApiEnvelope<T> {
  data?: T;
}

interface ApiExternalGroup {
  id: string;
  api_key?: string;
  base_url?: string;
  name?: string;
}

interface CheckExternalServiceResult {
  success: boolean;
  message?: string;
}

interface SaveExternalGroupResponse extends ApiExternalGroup {
  check?: CheckExternalServiceResult;
}

interface ExternalServiceConfigModalProps {
  open: boolean;
  service: ExternalServiceConfigModalService | null;
  onClose: () => void;
  onChanged?: () => void;
}

function unwrapResponse<T>(payload: ApiEnvelope<T> | T): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

function getApiBaseUrl() {
  return `${BASE_URL || window.location.origin}/api/core`;
}

function getRequestHeaders() {
  return {
    "Content-Type": "application/json",
    ...AgentAppsAuth.getAuthHeaders(),
  };
}

async function modelProviderRequest<T>(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  data?: unknown,
  options?: RawAxiosRequestConfig
) {
  const response = await axiosInstance.request<ApiEnvelope<T> | T>({
    method,
    url: `${getApiBaseUrl()}${path}`,
    data,
    headers: getRequestHeaders(),
    ...options,
  });
  return unwrapResponse<T>(response.data);
}

function normalizeProviderName(value: string) {
  return value.trim().toLowerCase().replace(/[^a-z0-9]+/g, "");
}

function isGoogleCustomSearch(service?: ExternalServiceConfigModalService | null) {
  return normalizeProviderName(service?.name || "") === "googlecustomsearch";
}

function getServiceProviderCategory(service: ExternalServiceConfigModalService): ExternalServiceProviderCategory {
  if (service.providerCategory) {
    return service.providerCategory;
  }
  return service.category === "parsing" ? "ocr" : "search";
}

function getCheckFailureMessage(checkResult?: CheckExternalServiceResult): string | undefined {
  if (!checkResult || typeof checkResult !== "object") {
    return undefined;
  }
  if (typeof checkResult.message === "string" && checkResult.message.trim()) {
    return checkResult.message.trim();
  }
  return undefined;
}

function maskAPIKey(raw: string) {
  const trimmed = raw.trim();
  if (trimmed.length <= 8) {
    return "*".repeat(trimmed.length);
  }
  return `${trimmed.slice(0, 4)}****...${trimmed.slice(-4)}`;
}

function ExternalServiceLogo({ service }: { service: ExternalServiceConfigModalService }) {
  const [imageReady, setImageReady] = useState(false);

  return (
    <span className={`model-provider-service-logo model-provider-service-logo-${service.tone}`}>
      {!imageReady ? <span className="model-provider-service-logo-icon">{service.logo}</span> : null}
      <img
        alt=""
        className={imageReady ? "is-loaded" : undefined}
        loading="lazy"
        referrerPolicy="no-referrer"
        src={service.logoUrl}
        onLoad={() => setImageReady(true)}
        onError={(event) => {
          event.currentTarget.style.display = "none";
        }}
      />
    </span>
  );
}

export default function ExternalServiceConfigModal({
  open,
  service,
  onClose,
  onChanged,
}: ExternalServiceConfigModalProps) {
  const { t } = useTranslation();
  const [form] = Form.useForm<Record<string, { baseUrl?: string }>>();
  const [addingKey, setAddingKey] = useState(false);
  const [keyList, setKeyList] = useState<string[]>([]);
  const [newKeyValue, setNewKeyValue] = useState("");
  const [newKeyEngineId, setNewKeyEngineId] = useState("");
  const [visibleKeys, setVisibleKeys] = useState<Set<number>>(new Set());
  const [group, setGroup] = useState<ApiExternalGroup | null>(null);
  const displayStatus = service?.fields.includes("apiKey") && keyList.length === 0 ? "missing" : service?.status;

  useEffect(() => {
    if (!open || !service) {
      return;
    }
    setKeyList([]);
    setNewKeyValue("");
    setNewKeyEngineId("");
    setVisibleKeys(new Set());
    setGroup(null);

    void modelProviderRequest<{ groups?: ApiExternalGroup[] }>(
      "GET",
      `/model_providers/${encodeURIComponent(service.key)}/groups`
    )
      .then((groupData) => {
        const nextGroup = (groupData.groups || [])[0] || null;
        setGroup(nextGroup);
        const rawKey = nextGroup?.api_key || "";
        setKeyList(rawKey.split("\n").map((key) => key.trim()).filter(Boolean));
      })
      .catch(() => {
        setGroup(null);
        setKeyList([]);
      });

    if (service.fields.includes("baseUrl")) {
      window.setTimeout(() => {
        const currentBaseUrl = form.getFieldValue([service.key, "baseUrl"]);
        if (!currentBaseUrl && service.baseUrl) {
          form.setFieldValue([service.key, "baseUrl"], service.baseUrl);
        }
      }, 0);
    }
  }, [form, open, service]);

  function handleClose() {
    if (addingKey) {
      return;
    }
    onClose();
  }

  function toggleKeyVisibility(idx: number) {
    setVisibleKeys((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) next.delete(idx);
      else next.add(idx);
      return next;
    });
  }

  async function copyKeyToClipboard(key: string) {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(key);
      } else {
        const textarea = document.createElement("textarea");
        textarea.value = key;
        textarea.style.position = "fixed";
        textarea.style.left = "-9999px";
        document.body.appendChild(textarea);
        textarea.focus();
        textarea.select();
        const copied = document.execCommand("copy");
        document.body.removeChild(textarea);
        if (!copied) throw new Error("Copy command failed");
      }
      message.success(t("common.copySuccess"));
    } catch {
      message.error(t("common.copyFailedManual"));
    }
  }

  async function handleAddKey() {
    if (!service) return;
    const rawKey = newKeyValue.trim();
    if (!rawKey) return;
    const engineId = newKeyEngineId.trim();
    if (isGoogleCustomSearch(service) && !engineId) return;
    const apiKey = isGoogleCustomSearch(service) ? `${rawKey}|${engineId}` : rawKey;

    setAddingKey(true);
    try {
      if (!group) {
        const payload: Record<string, unknown> = {
          name: service.name,
          base_url: service.baseUrl || "",
          api_key: apiKey,
          verify: true,
        };
        const savedGroup = await modelProviderRequest<SaveExternalGroupResponse>(
          "POST",
          `/model_providers/${encodeURIComponent(service.key)}/groups`,
          payload,
          { timeout: 3 * 60 * 1000 }
        );
        if (savedGroup.check && savedGroup.check.success !== true) {
          message.error(getCheckFailureMessage(savedGroup.check) || t("modelProvider.external.checkFailed"));
          return;
        }
        setGroup(savedGroup);
        setKeyList([apiKey]);
        await modelProviderRequest("PUT", "/model_providers/selected_providers", {
          selections: [{ category: getServiceProviderCategory(service), group_id: savedGroup.id }],
        });
      } else {
        await modelProviderRequest(
          "POST",
          `/model_providers/${encodeURIComponent(service.key)}/groups/${encodeURIComponent(group.id)}/keys`,
          { api_key: apiKey },
          { timeout: 3 * 60 * 1000 }
        );
        setKeyList((prev) => [...prev, apiKey]);
      }
      setNewKeyValue("");
      setNewKeyEngineId("");
      onChanged?.();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
    } finally {
      setAddingKey(false);
    }
  }

  async function handleRemoveKey(targetKey: string) {
    if (!service || !group) return;
    try {
      await modelProviderRequest(
        "DELETE",
        `/model_providers/${encodeURIComponent(service.key)}/groups/${encodeURIComponent(group.id)}/keys`,
        { api_key: targetKey }
      );
      setKeyList((prev) => prev.filter((key) => key !== targetKey));
      onChanged?.();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
    }
  }

  return (
    <Modal
      className="model-provider-service-config-modal"
      destroyOnClose
      footer={[
        <Button key="close" onClick={handleClose}>
          {t("common.close")}
        </Button>,
      ]}
      onCancel={handleClose}
      open={open}
      title={service ? t("modelProvider.external.configModalTitle", { name: service.name }) : t("modelProvider.external.configureAction")}
      width={600}
    >
      {service ? (
        <>
          <div className="model-provider-service-config-identity">
            <ExternalServiceLogo service={service} />
            <div>
              <div className="model-provider-service-title-row">
                <h4>{service.name}</h4>
                <Tag color={displayStatus === "configured" ? "success" : displayStatus === "tbd" ? "warning" : "default"}>
                  {t(`modelProvider.external.status.${displayStatus}`)}
                </Tag>
              </div>
              <p>{service.description}</p>
            </div>
          </div>
          <Form form={form} layout="vertical">
            {service.fields.includes("baseUrl") ? (
              <Form.Item
                label="Base URL"
                name={[service.key, "baseUrl"]}
                normalize={(value: string | undefined) => value?.trim()}
                rules={[
                  { required: true, message: t("modelProvider.validation.baseUrlRequired") },
                  { type: "url", message: t("modelProvider.validation.baseUrlInvalid") },
                  { max: 512, message: t("modelProvider.validation.baseUrlMax") },
                ]}
              >
                {service.baseUrlPresets?.length ? (
                  <AutoComplete
                    allowClear
                    filterOption={false}
                    options={service.baseUrlPresets.map((preset) => ({
                      value: preset.value,
                      label: (
                        <span className="model-provider-service-preset-option">
                          <strong>{t(preset.labelKey)}</strong>
                          <small>{preset.value}</small>
                          <small>{t(preset.descKey)}</small>
                        </span>
                      ),
                    }))}
                    placeholder="https://api.example.com"
                    popupClassName="model-provider-service-preset-dropdown"
                  />
                ) : (
                  <Input maxLength={512} placeholder="https://api.example.com" />
                )}
              </Form.Item>
            ) : null}
          </Form>

          <div className="model-provider-key-list">
            <div className="model-provider-key-list-label">API Keys</div>
            {keyList.length === 0 ? (
              <div className="model-provider-key-empty">{t("modelProvider.external.noKeysConfigured")}</div>
            ) : (
              keyList.map((key, idx) => (
                <div className="model-provider-key-item" key={key}>
                  <span className="model-provider-key-value" title={visibleKeys.has(idx) ? key : maskAPIKey(key)}>
                    {visibleKeys.has(idx) ? key : maskAPIKey(key)}
                  </span>
                  <div className="model-provider-key-actions">
                    <Tooltip title="复制">
                      <Button size="small" type="text" icon={<CopyOutlined />} onClick={() => copyKeyToClipboard(key)} />
                    </Tooltip>
                    <Tooltip title={visibleKeys.has(idx) ? "隐藏" : "显示"}>
                      <Button
                        size="small"
                        type="text"
                        icon={visibleKeys.has(idx) ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                        onClick={() => toggleKeyVisibility(idx)}
                      />
                    </Tooltip>
                    <Tag color="success">{t("modelProvider.external.keyVerified")}</Tag>
                    <Button size="small" type="text" danger icon={<DeleteOutlined />} onClick={() => handleRemoveKey(key)} />
                  </div>
                </div>
              ))
            )}
            <div className="model-provider-key-add">
              <Space direction="vertical" size={10} style={{ width: "100%" }}>
                {isGoogleCustomSearch(service) ? (
                  <Space className="model-provider-key-input-row">
                    <Input.Password
                      autoComplete="new-password"
                      maxLength={512}
                      placeholder={t("modelProvider.external.keyPlaceholder")}
                      value={newKeyValue}
                      onChange={(event) => setNewKeyValue(event.target.value)}
                      visibilityToggle={false}
                    />
                    <Input
                      autoComplete="off"
                      maxLength={512}
                      placeholder={t("modelProvider.external.googleSearchEngineIdPlaceholder")}
                      value={newKeyEngineId}
                      onChange={(event) => setNewKeyEngineId(event.target.value)}
                    />
                  </Space>
                ) : (
                  <div className="model-provider-key-input-row">
                    <Input.Password
                      autoComplete="new-password"
                      maxLength={512}
                      placeholder={t("modelProvider.external.keyPlaceholder")}
                      value={newKeyValue}
                      onChange={(event) => setNewKeyValue(event.target.value)}
                      visibilityToggle={false}
                    />
                  </div>
                )}
                <div className="model-provider-key-extra">{t("modelProvider.external.keyExtra")}</div>
                <Button
                  className="model-provider-key-add-button"
                  icon={<PlusOutlined />}
                  loading={addingKey}
                  onClick={handleAddKey}
                  type="primary"
                >
                  {t("modelProvider.external.verifyAndAddKey")}
                </Button>
              </Space>
            </div>
          </div>
        </>
      ) : null}
    </Modal>
  );
}
