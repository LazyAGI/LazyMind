import { Alert, Form, Input, Select, Tag } from "antd";
import { CloudServerOutlined, ToolOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";

type ExternalServiceKey =
  | "embedding"
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
  category: ServiceCategoryKey;
  providerOptions: string[];
  status: "configured" | "missing" | "tbd";
}

interface ExternalServiceFormValues {
  provider?: string;
  baseUrl?: string;
  apiKey?: string;
}

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
    category: "parsing",
    providerOptions: ["MinerU", "Custom"],
    status: "configured",
  },
  {
    key: "paddleocr",
    titleKey: "modelProvider.external.paddleTitle",
    descKey: "modelProvider.external.paddleDesc",
    category: "parsing",
    providerOptions: ["PaddleOCR", "Custom"],
    status: "tbd",
  },
  {
    key: "embedding",
    titleKey: "modelProvider.external.embeddingTitle",
    descKey: "modelProvider.external.embeddingDesc",
    category: "parsing",
    providerOptions: ["Qwen", "OpenAI", "SiliconFlow", "Custom"],
    status: "configured",
  },
  {
    key: "bingSearch",
    titleKey: "modelProvider.external.bingTitle",
    descKey: "modelProvider.external.bingDesc",
    category: "tools",
    providerOptions: ["Bing Search"],
    status: "missing",
  },
  {
    key: "googleSearch",
    titleKey: "modelProvider.external.googleTitle",
    descKey: "modelProvider.external.googleDesc",
    category: "tools",
    providerOptions: ["Google CSE"],
    status: "configured",
  },
  {
    key: "tavily",
    titleKey: "modelProvider.external.tavilyTitle",
    descKey: "modelProvider.external.tavilyDesc",
    category: "tools",
    providerOptions: ["Tavily"],
    status: "missing",
  },
];

export default function ExternalServicesPage() {
  const { t } = useTranslation();
  const [form] = Form.useForm<Record<ExternalServiceKey, ExternalServiceFormValues>>();

  return (
    <div className="model-provider-service-page">
      <Form form={form} layout="vertical" className="model-provider-service-stack">
        {serviceCategories.map((category) => (
          <section className="model-provider-service-category" key={category.key}>
            <div className="model-provider-service-category-head">
              <span>{category.icon}</span>
              <div>
                <h3>{t(category.titleKey)}</h3>
                <p>{t(category.descKey)}</p>
              </div>
            </div>

            <div className="model-provider-service-grid">
              {externalServiceConfigs
                .filter((service) => service.category === category.key)
                .map((service) => (
                  <article className="model-provider-service-card" key={service.key}>
                    <div className="model-provider-service-card-head">
                      <div>
                        <div className="model-provider-service-title-row">
                          <h4>{t(service.titleKey)}</h4>
                          <Tag
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
                        <p>{t(service.descKey)}</p>
                      </div>
                    </div>

                    <div className="model-provider-service-fields">
                      <Form.Item
                        label={t("modelProvider.external.provider")}
                        name={[service.key, "provider"]}
                        initialValue={service.providerOptions[0]}
                      >
                        <Select>
                          {service.providerOptions.map((provider) => (
                            <Select.Option key={provider} value={provider}>
                              {provider}
                            </Select.Option>
                          ))}
                        </Select>
                      </Form.Item>
                      <Form.Item label="Base URL" name={[service.key, "baseUrl"]}>
                        <Input placeholder="https://api.example.com" />
                      </Form.Item>
                      <Form.Item
                        className="model-provider-api-key-field"
                        extra={t("modelProvider.external.keyExtra")}
                        label="API Key"
                        name={[service.key, "apiKey"]}
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
                    </div>
                  </article>
                ))}
            </div>
          </section>
        ))}
      </Form>

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
