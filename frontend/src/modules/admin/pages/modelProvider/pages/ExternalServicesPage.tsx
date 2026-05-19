import { Button, Form, Input, Select, Tag, Tooltip, message } from "antd";
import type { FormInstance } from "antd";
import {
  KeyOutlined,
  QuestionCircleOutlined,
  SaveOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";

type ExternalServiceKey =
  | "embedding"
  | "mineru"
  | "paddleocr"
  | "searchEngine"
  | "webCrawler";

interface ExternalServiceConfig {
  key: ExternalServiceKey;
  titleKey: string;
  descKey: string;
  tagKey: string;
  providerOptions: string[];
}

interface ExternalServiceFormValues {
  provider?: string;
  baseUrl?: string;
  apiKey?: string;
}

const externalServiceConfigs: ExternalServiceConfig[] = [
  {
    key: "embedding",
    titleKey: "modelProvider.external.embeddingTitle",
    descKey: "modelProvider.external.embeddingDesc",
    tagKey: "modelProvider.external.tagRetrieval",
    providerOptions: ["Qwen", "OpenAI", "SiliconFlow", "Custom"],
  },
  {
    key: "mineru",
    titleKey: "modelProvider.external.mineruTitle",
    descKey: "modelProvider.external.mineruDesc",
    tagKey: "modelProvider.external.tagParsing",
    providerOptions: ["MinerU", "Custom"],
  },
  {
    key: "paddleocr",
    titleKey: "modelProvider.external.paddleTitle",
    descKey: "modelProvider.external.paddleDesc",
    tagKey: "modelProvider.external.tagParsing",
    providerOptions: ["PaddleOCR", "Custom"],
  },
  {
    key: "searchEngine",
    titleKey: "modelProvider.external.searchTitle",
    descKey: "modelProvider.external.searchDesc",
    tagKey: "modelProvider.external.tagTool",
    providerOptions: ["Google CSE", "Bocha", "Bing", "Custom"],
  },
  {
    key: "webCrawler",
    titleKey: "modelProvider.external.crawlerTitle",
    descKey: "modelProvider.external.crawlerDesc",
    tagKey: "modelProvider.external.tagTool",
    providerOptions: ["Firecrawl", "Jina", "Custom"],
  },
];

function maskKeyPreview(value?: string) {
  const normalized = (value || "").trim();
  if (!normalized) {
    return "";
  }
  if (normalized.length <= 8) {
    return "********";
  }
  return `${normalized.slice(0, 4)}...${normalized.slice(-4)}`;
}

function ServiceKeyPreview({
  form,
  serviceKey,
}: {
  form: FormInstance<Record<ExternalServiceKey, ExternalServiceFormValues>>;
  serviceKey: ExternalServiceKey;
}) {
  const { t } = useTranslation();
  const apiKey = Form.useWatch([serviceKey, "apiKey"], form);
  const preview = maskKeyPreview(apiKey) || "********";

  return (
    <span>
      <KeyOutlined />
      {t("modelProvider.external.previewOnly", { preview })}
    </span>
  );
}

export default function ExternalServicesPage() {
  const { t } = useTranslation();
  const [form] = Form.useForm<Record<ExternalServiceKey, ExternalServiceFormValues>>();

  const handleSave = () => {
    const values = form.getFieldsValue();
    const configured = Object.values(values).filter((item) => item?.provider || item?.baseUrl || item?.apiKey).length;
    message.success(t("modelProvider.external.saveMockMessage", { count: configured }));
  };

  return (
    <div className="model-provider-service-page">
      <section className="model-provider-service-hero">
        <div>
          <h2>{t("modelProvider.external.title")}</h2>
          <p>{t("modelProvider.external.subtitle")}</p>
        </div>
        <Button type="primary" icon={<SaveOutlined />} onClick={handleSave}>
          {t("modelProvider.external.savePolicy")}
        </Button>
      </section>

      <Form form={form} layout="vertical" className="model-provider-service-grid">
        {externalServiceConfigs.map((service) => (
          <section className="model-provider-service-card" key={service.key}>
            <div className="model-provider-service-card-head">
              <div>
                <div className="model-provider-service-title-row">
                  <h3>{t(service.titleKey)}</h3>
                  <Tag>{t(service.tagKey)}</Tag>
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
                  autoComplete="off"
                  maxLength={512}
                  placeholder={t("modelProvider.external.keyPlaceholder")}
                  visibilityToggle={false}
                />
              </Form.Item>
            </div>

            <div className="model-provider-service-footer">
              <ServiceKeyPreview form={form} serviceKey={service.key} />
              <Tooltip title={t("modelProvider.external.frontendOnlyTip")}>
                <Button icon={<QuestionCircleOutlined />} />
              </Tooltip>
            </div>
          </section>
        ))}
      </Form>
    </div>
  );
}
