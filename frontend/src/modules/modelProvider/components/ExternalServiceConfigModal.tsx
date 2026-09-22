import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Alert, Spin, AutoComplete, Button, Form, Input, Modal, Space, Tag, message } from "antd";
import {
  DeleteOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { localizeErrorCode } from "@/components/request";
import {
  modelProvidersApi,
  modelProvidersDefaultApi,
  unwrapModelProviderData,
  withModelProviderJsonOptions,
  type StoredProviderKey,
} from "../api";

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

interface ApiExternalGroup {
  has_api_key?: boolean;
  keys?: StoredProviderKey[];
  id: string;
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

function normalizeProviderName(value: string) {
  return value.trim().toLowerCase().replace(/[^a-z0-9]+/g, "");
}

function validateHttpBaseUrl(value?: string) {
  const normalizedValue = (value || "").trim();
  if (!normalizedValue) {
    return false;
  }
  try {
    const parsedUrl = new URL(normalizedValue);
    return parsedUrl.protocol === "http:" || parsedUrl.protocol === "https:";
  } catch {
    return false;
  }
}

function isFormValidationError(error: unknown) {
  return (
    !!error &&
    typeof error === "object" &&
    Array.isArray((error as { errorFields?: unknown[] }).errorFields)
  );
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
  const [keyList, setKeyList] = useState<StoredProviderKey[]>([]);
  const [newKeyValue, setNewKeyValue] = useState("");
  const [newKeyEngineId, setNewKeyEngineId] = useState("");
  const [keysLoaded, setKeysLoaded] = useState(false);
  const [keysError, setKeysError] = useState(false);
  const [group, setGroup] = useState<ApiExternalGroup | null>(null);
  const displayStatus = keysLoaded && service?.fields.includes("apiKey") && !group?.has_api_key && keyList.length === 0 ? "missing" : service?.status;

  const loadGroupKeys = useCallback(async (cancelled: () => boolean = () => false) => {
    if (!service) return;
    setKeysError(false);
    const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet({ modelProviderId: service.key }).catch((error) => {
      if (!cancelled()) setKeysError(true);
      throw error;
    });
    if (cancelled()) return;
    const data = unwrapModelProviderData<{ groups?: ApiExternalGroup[] }>(response.data);
    const nextGroup = data.groups?.[0] || null;
    setGroup(nextGroup);
    setKeyList(nextGroup?.keys || []);
    setKeysLoaded(true);
  }, [service]);

  useEffect(() => {
    if (!open || !service) {
      return;
    }
    setKeyList([]);
    setNewKeyValue("");
    setNewKeyEngineId("");
    setKeysLoaded(false);
    setGroup(null);

    let cancelled = false;
    void loadGroupKeys(() => cancelled).catch(() => {});

    if (service.fields.includes("baseUrl")) {
      window.setTimeout(() => {
        const currentBaseUrl = form.getFieldValue([service.key, "baseUrl"]);
        if (!currentBaseUrl && service.baseUrl) {
          form.setFieldValue([service.key, "baseUrl"], service.baseUrl);
        }
      }, 0);
    }
    return () => { cancelled = true; };
  }, [form, open, service, loadGroupKeys]);

  function handleClose() {
    if (addingKey) {
      return;
    }
    onClose();
  }

  async function handleAddKey() {
    if (!service || !keysLoaded || keysError) return;
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
        const savedGroup = unwrapModelProviderData<SaveExternalGroupResponse>((await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsPost(
          {
            modelProviderId: service.key,
            createModelProviderGroupOpenAPIRequest: payload as {
              api_key?: string;
              base_url: string;
              name: string;
              verify: boolean;
            },
          },
          { timeout: 3 * 60 * 1000 },
        )).data);
        if (savedGroup.check && savedGroup.check.success !== true) {
          message.error(localizeErrorCode("2000509"));
          return;
        }
        setGroup(savedGroup);
        await modelProvidersApi.apiCoreModelProvidersSelectedProvidersPut({
          setSelectedProviderOpenAPIRequest: {
            selections: [{ category: getServiceProviderCategory(service), group_id: savedGroup.id }],
          },
        });
      } else {
        await modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost(
          {
            modelProviderId: service.key,
            groupId: group.id,
          },
          withModelProviderJsonOptions({
            data: { api_key: apiKey },
            timeout: 3 * 60 * 1000,
          }),
        );
      }
      await loadGroupKeys();
      setNewKeyValue("");
      setNewKeyEngineId("");
      onChanged?.();
    } catch (error) {
      if (isFormValidationError(error)) {
        return;
      }
    } finally {
      setAddingKey(false);
    }
  }

  async function handleRemoveKey(targetKey: StoredProviderKey) {
    if (!service || !group) return;
    try {
      await modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete(
        {
          modelProviderId: service.key,
          groupId: group.id,
        },
        withModelProviderJsonOptions({ data: { key_id: targetKey.id } }),
      );
      await loadGroupKeys();
      onChanged?.();
    } catch (error) {
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
                  {
                    validator: (_, value?: string) =>
                      validateHttpBaseUrl(value)
                        ? Promise.resolve()
                        : Promise.reject(new Error(t("modelProvider.validation.baseUrlInvalid"))),
                  },
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
            {keysError ? (
              <Alert
                type="error"
                message={localizeErrorCode("2000509")}
                action={<Button onClick={() => void loadGroupKeys().catch(() => {})}>{t("common.retry")}</Button>}
              />
            ) : !keysLoaded ? <Spin size="small" /> : keyList.length === 0 ? (
              <div className="model-provider-key-empty">{t("modelProvider.external.noKeysConfigured")}</div>
            ) : (
              keyList.map((key) => (
                <div className="model-provider-key-item" key={key.id}>
                  <span className="model-provider-key-value" title={key.masked}>
                    {key.masked}
                  </span>
                  <div className="model-provider-key-actions">
                    <Tag color="success">{t("modelProvider.external.keyVerified")}</Tag>
                    <Button size="small" type="text" danger icon={<DeleteOutlined />} aria-label={t("common.delete")} onClick={() => handleRemoveKey(key)} />
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
                  disabled={!keysLoaded || keysError}
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
