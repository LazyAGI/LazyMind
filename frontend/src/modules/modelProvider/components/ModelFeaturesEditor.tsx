import { Form, Select, Switch } from "antd";
import { useTranslation } from "react-i18next";
import { modelFeatures, type ModelSettings } from "../modelSettings";
import "./ModelFeaturesEditor.scss";

export function ModelFeaturesEditor({ disabled = false, legacyVision = false, visualModel = false }: {
  disabled?: boolean;
  legacyVision?: boolean;
  visualModel?: boolean;
}) {
  const { t } = useTranslation();
  const form = Form.useFormInstance();
  const settings = Form.useWatch("modelSettings", form) as ModelSettings | undefined;
  return <div className="model-features-editor" role="group" aria-label={t("modelProvider.editor.features")}>
    <div className="model-features-heading"><span>{t("modelProvider.editor.features")}</span><span>{t("modelProvider.editor.support")}</span><span>{t("modelProvider.editor.enabled")}</span></div>
    {modelFeatures.map((feature) => {
      const declaredSupport = settings?.feature_support?.[feature];
      const unsupported = declaredSupport === false;
      const fallbackSupport = feature === "vision" && visualModel ? true : undefined;
      return <div className="model-feature-row" key={feature}>
        <span>{t(`modelProvider.editor.feature.${feature}`)}</span>
        <Form.Item name={["modelSettings", "feature_support", feature]}
          getValueProps={(value: boolean | undefined) => ({ value: value === undefined ? fallbackSupport === true ? "supported" : "unknown" : value ? "supported" : "unsupported" })}
          normalize={(value: string) => value === "unknown" ? undefined : value === "supported"}>
          <Select aria-label={t(`modelProvider.editor.featureSupport.${feature}`)} disabled={disabled}
            options={[{ value: "unknown", label: t("modelProvider.editor.unknownSupport") }, { value: "supported", label: t("modelProvider.editor.supported") }, { value: "unsupported", label: t("modelProvider.editor.unsupported"), disabled: feature === "vision" && visualModel }]}
            onChange={(value) => {
              if (value === "unsupported" && settings?.feature_enabled?.[feature] === true)
                form.setFieldValue(["modelSettings", "feature_enabled", feature], false);
            }} />
        </Form.Item>
        <Form.Item name={["modelSettings", "feature_enabled", feature]}
          getValueProps={(value: boolean | undefined) => ({ checked: !unsupported && (value ?? declaredSupport ?? (feature === "vision" ? visualModel || legacyVision : true)) })}
          trigger="onChange" getValueFromEvent={(value: boolean) => value}>
          <Switch disabled={disabled || unsupported} aria-label={feature === "vision" ? t("modelProvider.vision") : t(`modelProvider.editor.featureEnabled.${feature}`)} />
        </Form.Item>
      </div>;
    })}
    <p className="model-features-hint">{t("modelProvider.editor.featureHint")}</p>
    <p className="model-features-hint">{t("modelProvider.editor.reasoningHint")}</p>
  </div>;
}
