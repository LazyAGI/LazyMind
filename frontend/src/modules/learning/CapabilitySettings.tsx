import { Card, Form, Input, InputNumber, Select, Space, Switch, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { CapabilityRef, LearningCapability } from "./api";

interface Props { capabilities:LearningCapability[]; selectedKeys:string[]; }

export const capabilityRefsFromForm = (keys:string[], settings:Record<string,Record<string,unknown>> = {}, capabilities:LearningCapability[] = []):CapabilityRef[] => keys.map((key,index) => ({
  key,
  version:capabilities.find(item=>item.key===key)?.version || 1,
  enabled:true,
  display_order:index+1,
  settings:settings[key] || {},
}));

export const parseCapabilitySettings = (items:Array<{capability_key:string;settings_json:string}>) => Object.fromEntries(items.map(item=>{
  try { return [item.capability_key, JSON.parse(item.settings_json || "{}")]; }
  catch { return [item.capability_key, {}]; }
}));

export default function CapabilitySettings({ capabilities, selectedKeys }:Props) {
  const { t } = useTranslation();
  if (!selectedKeys.length) return null;
  return <Space direction="vertical" style={{width:"100%"}} size="small">
    {selectedKeys.map(key=>{
      const capability=capabilities.find(item=>item.key===key);
      if(!capability) return null;
      return <Card key={key} size="small" title={t(capability.name_i18n_key)}>
        <Form.Item name={["learning_capability_settings",key,"cache_scope"]} label={t("learning.cacheScope")} initialValue={capability.cache_policy.default_scope}>
          <Select options={capability.cache_policy.allowed_scopes.map(scope=>({value:scope,label:t(`learning.scope.${scope}`)}))}/>
        </Form.Item>
        <Space wrap size="large">
          <Form.Item name={["learning_capability_settings",key,"allow_llm_fallback"]} label={t("learning.allowLlmFallback")} valuePropName="checked" initialValue>
            <Switch/>
          </Form.Item>
        </Space>
        {key.includes("translation") && <Form.Item name={["learning_capability_settings",key,"target_language"]} label={t("learning.targetLanguage")}>
          <Input placeholder={t("learning.targetLanguagePlaceholder")}/>
        </Form.Item>}
        <Form.Item name={["learning_capability_settings",key,"max_selection_length"]} label={t("learning.maxSelectionLength")}>
          <InputNumber min={1} max={10000} style={{width:"100%"}} placeholder={t("learning.useCapabilityDefault")}/>
        </Form.Item>
        <Typography.Text type="secondary">{t("learning.capabilitySettingsHint")}</Typography.Text>
      </Card>;
    })}
  </Space>;
}
