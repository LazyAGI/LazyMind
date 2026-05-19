import { Alert, Button, Checkbox, Radio, Segmented, Select, Slider, Switch, Tag, message } from "antd";
import {
  CloudOutlined,
  DatabaseOutlined,
  EyeOutlined,
  SaveOutlined,
  SoundOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import DefaultModelConfigPanel from "../components/DefaultModelConfigPanel";

type RetrievalMode = "vector" | "bm25";
type OptionalModuleKey = "rerank" | "documentParsing" | "multimodal";

const optionalModules: Array<{
  key: OptionalModuleKey;
  titleKey: string;
  descKey: string;
  icon: ReactNode;
}> = [
  {
    key: "rerank",
    titleKey: "modelProvider.defaultServices.rerankTitle",
    descKey: "modelProvider.defaultServices.rerankDesc",
    icon: <ThunderboltOutlined />,
  },
  {
    key: "documentParsing",
    titleKey: "modelProvider.defaultServices.parsingTitle",
    descKey: "modelProvider.defaultServices.parsingDesc",
    icon: <CloudOutlined />,
  },
  {
    key: "multimodal",
    titleKey: "modelProvider.defaultServices.multimodalTitle",
    descKey: "modelProvider.defaultServices.multimodalDesc",
    icon: <EyeOutlined />,
  },
];

export default function DefaultServicesPage() {
  const { t } = useTranslation();
  const [retrievalMode, setRetrievalMode] = useState<RetrievalMode>("vector");
  const [denseEnabled, setDenseEnabled] = useState(true);
  const [sparseEnabled, setSparseEnabled] = useState(false);
  const [moduleEnabled, setModuleEnabled] = useState<Record<OptionalModuleKey, boolean>>({
    rerank: true,
    documentParsing: true,
    multimodal: false,
  });

  const vectorReady = retrievalMode === "bm25" || denseEnabled || sparseEnabled;
  const summaryItems = useMemo(
    () => [
      {
        icon: <DatabaseOutlined />,
        label: t("modelProvider.defaultServices.summaryRetrieval"),
        value: retrievalMode === "vector" ? t("modelProvider.defaultServices.vectorRecall") : "BM25",
      },
      {
        icon: <ThunderboltOutlined />,
        label: t("modelProvider.defaultServices.summaryRerank"),
        value: moduleEnabled.rerank ? t("common.enabled") : t("modelProvider.defaultServices.disabled"),
      },
      {
        icon: <SoundOutlined />,
        label: t("modelProvider.defaultServices.summaryParsing"),
        value: moduleEnabled.documentParsing ? "MinerU / PaddleOCR" : t("modelProvider.defaultServices.disabled"),
      },
      {
        icon: <EyeOutlined />,
        label: t("modelProvider.defaultServices.summaryMultimodal"),
        value: moduleEnabled.multimodal ? "VLM / SD / STT / TTS" : t("modelProvider.defaultServices.disabled"),
      },
    ],
    [moduleEnabled, retrievalMode, t],
  );

  const saveSettings = () => {
    if (!vectorReady) {
      message.error(t("modelProvider.defaultServices.vectorRequired"));
      return;
    }
    message.success(t("modelProvider.defaultServices.saveMockMessage"));
  };

  return (
    <div className="model-provider-service-page">
      <section className="model-provider-service-hero">
        <div>
          <h2>{t("modelProvider.defaultServices.title")}</h2>
          <p>{t("modelProvider.defaultServices.subtitle")}</p>
        </div>
        <Button type="primary" icon={<SaveOutlined />} onClick={saveSettings}>
          {t("modelProvider.defaultServices.save")}
        </Button>
      </section>

      <DefaultModelConfigPanel />

      <section className="model-provider-policy-grid">
        <div className="model-provider-policy-main">
          <section className="model-provider-policy-section">
            <div className="model-provider-policy-head">
              <div>
                <h3>{t("modelProvider.defaultServices.embeddingStrategy")}</h3>
                <p>{t("modelProvider.defaultServices.embeddingStrategyDesc")}</p>
              </div>
              <Segmented
                value={retrievalMode}
                options={[
                  { label: t("modelProvider.defaultServices.vectorRecall"), value: "vector" },
                  { label: "BM25", value: "bm25" },
                ]}
                onChange={(value) => setRetrievalMode(value as RetrievalMode)}
              />
            </div>

            {retrievalMode === "vector" ? (
              <div className="model-provider-vector-options">
                <Checkbox checked={denseEnabled} onChange={(event) => setDenseEnabled(event.target.checked)}>
                  {t("modelProvider.defaultServices.denseVector")}
                </Checkbox>
                <Checkbox checked={sparseEnabled} onChange={(event) => setSparseEnabled(event.target.checked)}>
                  {t("modelProvider.defaultServices.sparseVector")}
                </Checkbox>
                {!vectorReady ? (
                  <Alert type="error" showIcon message={t("modelProvider.defaultServices.vectorRequired")} />
                ) : null}
                <div className="model-provider-slider-row">
                  <span>{t("modelProvider.defaultServices.chunkSize")}</span>
                  <Slider min={256} max={4096} step={256} defaultValue={2048} />
                </div>
              </div>
            ) : (
              <Alert type="info" showIcon message={t("modelProvider.defaultServices.bm25OnlyDesc")} />
            )}
          </section>

          <section className="model-provider-policy-section">
            <div className="model-provider-policy-head">
              <div>
                <h3>{t("modelProvider.defaultServices.optionalModules")}</h3>
                <p>{t("modelProvider.defaultServices.optionalModulesDesc")}</p>
              </div>
            </div>
            <div className="model-provider-module-list">
              {optionalModules.map((module) => (
                <article className="model-provider-module-row" key={module.key}>
                  <span className="model-provider-module-icon">{module.icon}</span>
                  <div>
                    <h4>{t(module.titleKey)}</h4>
                    <p>{t(module.descKey)}</p>
                  </div>
                  <Switch
                    checked={moduleEnabled[module.key]}
                    onChange={(checked) =>
                      setModuleEnabled((current) => ({
                        ...current,
                        [module.key]: checked,
                      }))
                    }
                  />
                </article>
              ))}
            </div>
          </section>

          <section className="model-provider-policy-section">
            <div className="model-provider-policy-head">
              <div>
                <h3>{t("modelProvider.defaultServices.routingPolicy")}</h3>
                <p>{t("modelProvider.defaultServices.routingPolicyDesc")}</p>
              </div>
            </div>
            <Radio.Group defaultValue="personal-first" className="model-provider-radio-stack">
              <Radio value="personal-first">{t("modelProvider.defaultServices.personalFirst")}</Radio>
              <Radio value="admin-shared">{t("modelProvider.defaultServices.adminShared")}</Radio>
              <Radio value="locked">{t("modelProvider.defaultServices.lockedByBackend")}</Radio>
            </Radio.Group>
          </section>
        </div>

        <aside className="model-provider-policy-side">
          <h3>{t("modelProvider.defaultServices.summaryTitle")}</h3>
          <div className="model-provider-summary-list">
            {summaryItems.map((item) => (
              <div className="model-provider-summary-item" key={item.label}>
                <span>{item.icon}</span>
                <div>
                  <small>{item.label}</small>
                  <strong>{item.value}</strong>
                </div>
              </div>
            ))}
          </div>
          <div className="model-provider-limit-box">
            <h4>{t("modelProvider.defaultServices.backendLimitTitle")}</h4>
            <p>{t("modelProvider.defaultServices.backendLimitDesc")}</p>
            <div>
              <Tag>VLM</Tag>
              <Tag>SD</Tag>
              <Tag>STT</Tag>
              <Tag>TTS</Tag>
            </div>
          </div>
          <Select
            className="model-provider-default-service-select"
            defaultValue="balanced"
            options={[
              { value: "balanced", label: t("modelProvider.defaultServices.profileBalanced") },
              { value: "offline", label: t("modelProvider.defaultServices.profileOffline") },
              { value: "minimal", label: t("modelProvider.defaultServices.profileMinimal") },
            ]}
          />
        </aside>
      </section>
    </div>
  );
}
