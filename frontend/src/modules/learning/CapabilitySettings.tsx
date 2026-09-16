import { Form, Input, InputNumber, Select, Switch, Tabs, Tooltip, Typography } from "antd";
import { QuestionCircleOutlined } from "@ant-design/icons";
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
  return <Tabs
    className="capability-settings-tabs"
    size="small"
    tabBarExtraContent={<Typography.Text className="capability-settings-hint" type="secondary">{t("learning.capabilitySettingsHint")}</Typography.Text>}
    items={selectedKeys.flatMap(key=>{
      const capability=capabilities.find(item=>item.key===key);
      if(!capability) return [];
      return [{
        key,
        label:t(capability.name_i18n_key),
        children:<div className="capability-settings-panel">
          <Form.Item name={["learning_capability_settings",key,"cache_scope"]} label={t("learning.cacheScope")} initialValue={capability.cache_policy.default_scope}>
            <Select options={capability.cache_policy.allowed_scopes.map(scope=>({value:scope,label:t(`learning.scope.${scope}`)}))}/>
          </Form.Item>
          <Form.Item
            name={["learning_capability_settings",key,"allow_llm_fallback"]}
            label={<span>{t("learning.allowLlmFallback")} <Tooltip title={t("learning.allowLlmFallbackHint")}><QuestionCircleOutlined className="knowledge-create-help-icon" /></Tooltip></span>}
            valuePropName="checked"
            initialValue
          >
            <Switch/>
          </Form.Item>
          {key.includes("translation") && <Form.Item name={["learning_capability_settings",key,"target_language"]} label={t("learning.targetLanguage")}>
            <Input placeholder={t("learning.targetLanguagePlaceholder")}/>
          </Form.Item>}
          {(key === "chinese_definition" || key === "english_definition" || key === "classical_definition") && <Form.Item
            name={["learning_capability_settings",key,"output_language"]}
            label={<span>{t("learning.outputLanguage")} <Tooltip title={t("learning.outputLanguageHint")}><QuestionCircleOutlined className="knowledge-create-help-icon" /></Tooltip></span>}
            initialValue="auto"
          >
            <Select options={[
              {value:"auto",label:t("learning.outputLanguageOption.auto")},
              {value:"zh-Hans",label:t("learning.outputLanguageOption.zhHans")},
              {value:"en",label:t("learning.outputLanguageOption.en")},
              {value:"zh-Hans+en",label:t("learning.outputLanguageOption.bilingual")},
            ]}/>
          </Form.Item>}
          <Form.Item name={["learning_capability_settings",key,"max_selection_length"]} label={t("learning.maxSelectionLength")}>
            <InputNumber min={1} max={10000} style={{width:"100%"}} placeholder={t("learning.useCapabilityDefault")}/>
          </Form.Item>
          <Form.Item name={["learning_capability_settings",key,"max_candidates_per_block"]} label={t("learning.maxCandidatesPerBlock")}>
            <InputNumber min={1} max={100} precision={0} style={{width:"100%"}} placeholder={t("learning.capabilityDefaultValue",{value:capability.analysis?.max_candidates ?? 8})}/>
          </Form.Item>
          <Form.Item name={["learning_capability_settings",key,"max_document_candidates"]} label={t("learning.maxDocumentCandidates")}>
            <InputNumber min={1} max={1000} precision={0} style={{width:"100%"}} placeholder={t("learning.capabilityDefaultValue",{value:capability.analysis?.max_document_candidates ?? 20})}/>
          </Form.Item>
        </div>,
      }];
    })}
  />;
}
