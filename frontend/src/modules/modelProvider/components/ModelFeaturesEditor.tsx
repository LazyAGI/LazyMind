import { BulbOutlined, EyeOutlined, ToolOutlined } from "@ant-design/icons";
import { Button, Form, Tooltip } from "antd";
import type { ComponentType } from "react";
import { useTranslation } from "react-i18next";
import { modelFeatures, type ModelFeature, type ModelSettings } from "../modelSettings";
import "./ModelFeaturesEditor.scss";

const featureIcons: Record<ModelFeature, ComponentType> = {
  vision: EyeOutlined,
  tool_calling: ToolOutlined,
  reasoning: BulbOutlined,
};

export function ModelFeaturesEditor({ disabled = false, legacyVision = false, visualModel = false }: {
  disabled?: boolean;
  legacyVision?: boolean;
  visualModel?: boolean;
}) {
  const { t } = useTranslation();
  const form = Form.useFormInstance();
  const settings = Form.useWatch("modelSettings", form) as ModelSettings | undefined;
  return <div className="model-features-editor" role="group" aria-label={t("modelProvider.editor.features")}>
    <div className="model-features-heading">{t("modelProvider.editor.features")}</div>
    <div className="model-feature-buttons">
      {modelFeatures.map((feature) => {
        const Icon = featureIcons[feature];
        const explicitEnabled = settings?.feature_enabled?.[feature] ?? form.getFieldValue(["modelSettings", "feature_enabled", feature]);
        const declaredSupport = settings?.feature_support?.[feature] ?? form.getFieldValue(["modelSettings", "feature_support", feature]);
        const legacyFallback = feature === "vision" ? visualModel || legacyVision : true;
        const featureEnabled = explicitEnabled ?? declaredSupport ?? legacyFallback;
        const path = ["modelSettings", "feature_enabled", feature] as const;
        return <Tooltip key={feature} title={t(`modelProvider.editor.featureTooltip.${feature}`)}>
          <Form.Item noStyle name={path} trigger="onClick" getValueFromEvent={() => !featureEnabled}
            getValueProps={(value: boolean | undefined) => ({ "aria-pressed": value ?? declaredSupport ?? legacyFallback })}>
            <Button
              type="text"
              className={`model-feature-button${featureEnabled ? " is-active" : ""}`}
              aria-label={t(`modelProvider.editor.featureEnabled.${feature}`)}
              aria-pressed={featureEnabled}
              disabled={disabled}
              icon={<Icon aria-hidden="true" />}
            >
              <span className="model-feature-label">{t(`modelProvider.editor.feature.${feature}`)}</span>
            </Button>
          </Form.Item>
        </Tooltip>;
      })}
    </div>
  </div>;
}
