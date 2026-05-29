import { useState } from "react";
import { Alert, AutoComplete, Button, Form, Input, Modal, Tag } from "antd";
import {
  CloudServerOutlined,
  CompassOutlined,
  FilePdfOutlined,
  GoogleOutlined,
  RightOutlined,
  ScanOutlined,
  SearchOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import { useOutletContext } from "react-router-dom";
import { useTranslation } from "react-i18next";

type ExternalServiceKey =
  | "mineru"
  | "paddleocr"
  | "bingSearch"
  | "googleSearch"
  | "tavily";

type ServiceCategoryKey = "parsing" | "tools";

interface ExternalServiceConfig {
  key: ExternalServiceKey;
  titleKey: string;
  descKey: string;
  summaryKey: string;
  category: ServiceCategoryKey;
  fields: Array<keyof ExternalServiceFormValues>;
  logo: JSX.Element;
  logoUrl: string;
  tone: "blue" | "cyan" | "green" | "red" | "violet";
  status: "configured" | "missing" | "tbd";
  baseUrlPresets?: BaseUrlPreset[];
}

interface ExternalServiceFormValues {
  baseUrl?: string;
  apiKey?: string;
}

interface ModelProviderOutletContext {
  externalServiceSearchValue?: string;
}

interface BaseUrlPreset {
  labelKey: string;
  descKey: string;
  value: string;
}

const mineruDockerComposeBaseUrl = "http://host.docker.internal:8000/api/v1/pdf_parse";
const mineruOfficialBaseUrl = "https://mineru.example.com/api/v1/pdf_parse";

const serviceCategories: Array<{
  key: ServiceCategoryKey;
  titleKey: string;
  descKey: string;
  icon: JSX.Element;
}> = [
  {
    key: "parsing",
    titleKey: "modelProvider.external.parsingCategoryTitle",
    descKey: "modelProvider.external.parsingCategoryDesc",
    icon: <CloudServerOutlined />,
  },
  {
    key: "tools",
    titleKey: "modelProvider.external.toolsCategoryTitle",
    descKey: "modelProvider.external.toolsCategoryDesc",
    icon: <ToolOutlined />,
  },
];

const externalServiceConfigs: ExternalServiceConfig[] = [
  {
    key: "mineru",
    titleKey: "modelProvider.external.mineruTitle",
    descKey: "modelProvider.external.mineruDesc",
    summaryKey: "modelProvider.external.mineruSummary",
    category: "parsing",
    fields: ["baseUrl", "apiKey"],
    logo: <FilePdfOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=mineru.net&sz=96",
    tone: "blue",
    status: "configured",
    baseUrlPresets: [
      {
        labelKey: "modelProvider.external.mineruDockerComposePreset",
        descKey: "modelProvider.external.mineruDockerComposePresetDesc",
        value: mineruDockerComposeBaseUrl,
      },
      {
        labelKey: "modelProvider.external.mineruOfficialPreset",
        descKey: "modelProvider.external.mineruOfficialPresetDesc",
        value: mineruOfficialBaseUrl,
      },
    ],
  },
  {
    key: "paddleocr",
    titleKey: "modelProvider.external.paddleTitle",
    descKey: "modelProvider.external.paddleDesc",
    summaryKey: "modelProvider.external.paddleSummary",
    category: "parsing",
    fields: ["baseUrl", "apiKey"],
    logo: <ScanOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=paddleocr.ai&sz=96",
    tone: "cyan",
    status: "tbd",
  },
  {
    key: "bingSearch",
    titleKey: "modelProvider.external.bingTitle",
    descKey: "modelProvider.external.bingDesc",
    summaryKey: "modelProvider.external.bingSummary",
    category: "tools",
    fields: ["apiKey"],
    logo: <SearchOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=bing.com&sz=96",
    tone: "green",
    status: "missing",
  },
  {
    key: "googleSearch",
    titleKey: "modelProvider.external.googleTitle",
    descKey: "modelProvider.external.googleDesc",
    summaryKey: "modelProvider.external.googleSummary",
    category: "tools",
    fields: ["apiKey"],
    logo: <GoogleOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=google.com&sz=96",
    tone: "red",
    status: "configured",
  },
  {
    key: "tavily",
    titleKey: "modelProvider.external.tavilyTitle",
    descKey: "modelProvider.external.tavilyDesc",
    summaryKey: "modelProvider.external.tavilySummary",
    category: "tools",
    fields: ["apiKey"],
    logo: <CompassOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=tavily.com&sz=96",
    tone: "violet",
    status: "missing",
  },
];

function ExternalServiceLogo({ service }: { service: ExternalServiceConfig }) {
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

export default function ExternalServicesPage() {
  const { t } = useTranslation();
  const { externalServiceSearchValue = "" } = useOutletContext<ModelProviderOutletContext>();
  const [form] = Form.useForm<Record<ExternalServiceKey, ExternalServiceFormValues>>();
  const [activeService, setActiveService] = useState<ExternalServiceConfig | null>(null);
  const normalizedSearchValue = externalServiceSearchValue.trim().toLowerCase();

  const closeConfigModal = () => {
    setActiveService(null);
  };

  const openConfigModal = (service: ExternalServiceConfig) => {
    setActiveService(service);
    if (service.key === "mineru") {
      window.setTimeout(() => {
        const currentBaseUrl = form.getFieldValue([service.key, "baseUrl"]);
        if (!currentBaseUrl) {
          form.setFieldValue([service.key, "baseUrl"], mineruDockerComposeBaseUrl);
        }
      }, 0);
    }
  };

  const handleSaveConfig = async () => {
    if (!activeService) {
      return;
    }

    try {
      await form.validateFields(activeService.fields.map((field) => [activeService.key, field]));
    } catch {
      return;
    }

    closeConfigModal();
  };

  const matchesSearch = (values: string[]) => (
    !normalizedSearchValue ||
    values.some((value) => value.toLowerCase().includes(normalizedSearchValue))
  );

  return (
    <div className="model-provider-service-page">
      <div className="model-provider-service-stack">
        {serviceCategories.map((category) => {
          const categoryTitle = t(category.titleKey);
          const categoryDesc = t(category.descKey);
          const categoryMatches = matchesSearch([categoryTitle, categoryDesc]);
          const categoryServices = externalServiceConfigs.filter((service) => service.category === category.key);
          const matchedServices = categoryServices.filter((service) =>
            matchesSearch([
              service.key,
              t(service.titleKey),
              t(service.descKey),
              t(service.summaryKey),
              t(`modelProvider.external.status.${service.status}`),
            ])
          );
          const services = normalizedSearchValue && matchedServices.length
            ? matchedServices
            : categoryMatches
              ? categoryServices
              : matchedServices;

          if (!services.length) {
            return null;
          }

          return (
            <section className="model-provider-service-category" key={category.key}>
              <div className="model-provider-service-category-head">
                <span>{category.icon}</span>
                <div>
                  <h3>{categoryTitle}</h3>
                  <p>{categoryDesc}</p>
                </div>
              </div>

              <div className="model-provider-service-grid">
                {services.map((service) => (
                  <button
                    aria-label={t("modelProvider.external.configModalTitle", { name: t(service.titleKey) })}
                    className="model-provider-service-card"
                    key={service.key}
                    onClick={() => openConfigModal(service)}
                    type="button"
                  >
                    <ExternalServiceLogo service={service} />
                    <div className="model-provider-service-card-copy">
                      <div>
                        <div className="model-provider-service-title-row">
                          <h4>{t(service.titleKey)}</h4>
                          <Tag
                            className="model-provider-service-status"
                            color={
                              service.status === "configured"
                                ? "success"
                                : service.status === "tbd"
                                  ? "warning"
                                  : "default"
                            }
                          >
                            {t(`modelProvider.external.status.${service.status}`)}
                          </Tag>
                        </div>
                        <p>{t(service.summaryKey)}</p>
                      </div>
                    </div>
                    <span className="model-provider-service-card-arrow" aria-hidden="true">
                      <RightOutlined />
                    </span>
                  </button>
                ))}
              </div>
            </section>
          );
        })}
      </div>

      <Modal
        className="model-provider-service-config-modal"
        destroyOnClose
        onCancel={closeConfigModal}
        open={!!activeService}
        title={
          activeService
            ? t("modelProvider.external.configModalTitle", { name: t(activeService.titleKey) })
            : t("modelProvider.external.configureAction")
        }
        footer={[
          <Button key="cancel" onClick={closeConfigModal}>
            {t("common.cancel")}
          </Button>,
          <Button key="save" onClick={handleSaveConfig} type="primary">
            {t("modelProvider.external.saveConfig")}
          </Button>,
        ]}
      >
        {activeService && (
          <>
            <div className="model-provider-service-config-identity">
              <ExternalServiceLogo service={activeService} />
              <div>
                <div className="model-provider-service-title-row">
                  <h4>{t(activeService.titleKey)}</h4>
                  <Tag
                    color={
                      activeService.status === "configured"
                        ? "success"
                        : activeService.status === "tbd"
                          ? "warning"
                          : "default"
                    }
                  >
                    {t(`modelProvider.external.status.${activeService.status}`)}
                  </Tag>
                </div>
                <p>{t(activeService.descKey)}</p>
              </div>
            </div>
            <Form form={form} layout="vertical">
              {activeService.fields.includes("baseUrl") ? (
                <Form.Item
                  extra={
                    activeService.key === "mineru"
                      ? t("modelProvider.external.mineruBaseUrlPresetExtra")
                      : undefined
                  }
                  label="Base URL"
                  name={[activeService.key, "baseUrl"]}
                  normalize={(value: string | undefined) => value?.trim()}
                  rules={[
                    { required: true, message: t("modelProvider.validation.baseUrlRequired") },
                    { type: "url", message: t("modelProvider.validation.baseUrlInvalid") },
                    { max: 512, message: t("modelProvider.validation.baseUrlMax") },
                  ]}
                >
                  {activeService.baseUrlPresets?.length ? (
                    <AutoComplete
                      allowClear
                      filterOption={false}
                      options={activeService.baseUrlPresets.map((preset) => ({
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
              <Form.Item
                extra={t("modelProvider.external.keyExtra")}
                label="API Key"
                name={[activeService.key, "apiKey"]}
                normalize={(value: string | undefined) => value?.trim()}
                rules={[
                  { max: 512, message: t("modelProvider.validation.apiKeyMax") },
                  {
                    validator: (_, value?: string) =>
                      /\s/.test((value || "").trim())
                        ? Promise.reject(new Error(t("modelProvider.validation.apiKeyNoSpaces")))
                        : Promise.resolve(),
                  },
                ]}
              >
                <Input.Password
                  autoComplete="new-password"
                  maxLength={512}
                  placeholder={t("modelProvider.external.keyPlaceholder")}
                  visibilityToggle={false}
                />
              </Form.Item>
            </Form>
          </>
        )}
      </Modal>

      <Alert
        className="model-provider-service-alert"
        type="info"
        showIcon
        message={t("modelProvider.external.apiContractTitle")}
        description={t("modelProvider.external.apiContractDesc")}
      />
    </div>
  );
}
