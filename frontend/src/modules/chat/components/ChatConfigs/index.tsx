import { Form, Select, Space } from "antd";
import { SettingOutlined } from "@ant-design/icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import KnowledgeBaseConfigModal, {
  ConfigImperativeProps,
} from "../KnowledgeBaseConfigModal";
import {
  KnowledgeBaseServiceApi,
} from "@/modules/chat/utils/request";
import { Dataset, UserDatabaseSummary } from "@/api/generated/knowledge-client";

export interface ChatConfig {
  knowledgeBaseId?: string[];
  tags?: string[];
  creators?: string[];
  databaseBaseId?: string;
}

interface Props {
  configs: ChatConfig;
  onChange: (config: ChatConfig) => void;
}

const ChatConfigs = (props: Props) => {
  const { t } = useTranslation();
  const { configs, onChange } = props;

  const [form] = Form.useForm();

  const [knowledgeBaseList, setKnowledgeBaseList] = useState<Dataset[]>([]);
  const [databaseBaseList, setDatabaseBaseList] = useState<
    UserDatabaseSummary[]
  >([]);
  const [isAllSelected, setIsAllSelected] = useState<boolean>(false);

  const configRef = useRef<ConfigImperativeProps>(null);

  useEffect(() => {
    getKnowledgeBaseList();
    getDatabaseBaseList();
  }, []);

  useEffect(() => {
    form.setFieldsValue({
      knowledgeBaseId: configs.knowledgeBaseId,
      databaseBaseId: configs.databaseBaseId,
    });
  }, [configs]);

  function getDatabaseBaseList() {
    // DatabaseBaseServiceApi()
    //   .databaseServiceGetUserDatabaseSummaries({})
    //   .then((res) => {
    //     setDatabaseBaseList((res?.data as UserDatabaseSummary[]) || []);
    //   });
    setDatabaseBaseList([]);
  }

  function getKnowledgeBaseList() {
    KnowledgeBaseServiceApi()
      .datasetServiceListDatasets({ pageSize: 1000 })
      .then((res) => {
        setKnowledgeBaseList(res.data.datasets || []);
      });
  }

  function onKnowledgeBaseConfigChanged(configs: ChatConfig) {
    updateConfigs(configs);
  }

  function onFormChanged() {
    updateConfigs(form.getFieldsValue());
  }

  function updateConfigs(configs: ChatConfig) {
    onChange(configs);
  }

  function knowledgeAllSelected() {
    if (isAllSelected) {
      form.setFieldValue("knowledgeBaseId", []);
      updateConfigs({ ...form.getFieldsValue(), knowledgeBaseId: [] });
    } else {
      form.setFieldValue(
        "knowledgeBaseId",
        knowledgeBaseList.map((kb) => kb.dataset_id),
      );
      updateConfigs({
        ...form.getFieldsValue(),
        knowledgeBaseId: knowledgeBaseList.map((kb) => kb.dataset_id),
      });
    }
    setIsAllSelected(!isAllSelected);
  }

  return (
    <Form form={form} layout="vertical" onValuesChange={onFormChanged}>
      <Form.Item
        name="knowledgeBaseId"
        label={
          <Space>
            {t("chat.configKnowledgeBase")}
            <SettingOutlined
              onClick={() => configRef.current?.onOpen(configs)}
            />
            <div
              style={{ cursor: "pointer", color: "#006ae6" }}
              onClick={knowledgeAllSelected}
            >
              {isAllSelected ? t("chat.cancelSelectAll") : t("chat.selectAll")}
            </div>
          </Space>
        }
      >
        <Select
          options={knowledgeBaseList.map((knowledgeBase) => ({
            value: knowledgeBase.dataset_id,
            label: knowledgeBase.display_name,
          }))}
          mode="multiple"
          maxTagCount="responsive"
          showSearch
          allowClear
          placeholder={t("chat.knowledgeBaseName")}
          optionFilterProp="label"
          onChange={(value) => {
            if (value.length === knowledgeBaseList.length) {
              setIsAllSelected(true);
            } else {
              setIsAllSelected(false);
            }
          }}
        />
      </Form.Item>

      <Form.Item
        name="databaseBaseId"
        label={<Space>{t("chat.configDatabase")}</Space>}
      >
        <Select
          options={databaseBaseList.map((db) => {
            return {
              value: db.id,
              label: db.name,
            };
          })}
          showSearch
          allowClear
          placeholder={t("chat.databaseName")}
          optionFilterProp="label"
        />
      </Form.Item>

      <KnowledgeBaseConfigModal
        ref={configRef}
        onChange={onKnowledgeBaseConfigChanged}
      />
    </Form>
  );
};

export default ChatConfigs;
