import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useState,
} from "react";
import { Modal, Form, Input, Select, Tabs, Typography, Button } from "antd";
import { useTranslation } from "react-i18next";
import { Dataset, Algo } from "@/api/generated/knowledge-client";
import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";
import {
  KNOWLEDGE_BASE_NAME_MAX_LENGTH,
  KNOWLEDGE_BASE_NAME_PATTERN,
} from "@/modules/knowledge/constants/validation";
import DataSourceProviderPicker from "@/modules/dataSource/components/management/DataSourceProviderPicker";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import TagSelect from "../TagSelect";
import "@/modules/dataSource/index.scss";
import "./index.scss";

const { TextArea } = Input;
const { Paragraph } = Typography;
const KNOWLEDGE_TAG_MAX_LENGTH = 20;
const CREATE_MODAL_WIDTH = 576;

type CreateTab = "direct" | "cloud";

export interface CreateKnowledgeBaseModalProps {
  onCreate: (dataset: Dataset) => Promise<void>;
  syncCreateVm: SyncKnowledgeBaseCreationVm;
}

export interface CreateKnowledgeBaseModalRef {
  onOpen: (tab?: CreateTab) => void;
}

const CreateKnowledgeBaseModal = forwardRef<
  CreateKnowledgeBaseModalRef,
  CreateKnowledgeBaseModalProps
>(({ onCreate, syncCreateVm }, ref) => {
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);
  const [activeTab, setActiveTab] = useState<CreateTab>("direct");
  const [tags, setTags] = useState<string[]>([]);
  const [algorithm, setAlgorithm] = useState<Algo[]>([]);
  const [hasTagLengthError, setHasTagLengthError] = useState(false);
  const [form] = Form.useForm();

  useImperativeHandle(ref, () => ({
    onOpen,
  }));

  useEffect(() => {
    if (!visible || algorithm.length !== 1) {
      return;
    }
    const currentAlgoId = form.getFieldValue("algo_id");
    if (!currentAlgoId) {
      form.setFieldsValue({ algo_id: algorithm[0].algo_id });
    }
  }, [algorithm, visible, form]);

  useEffect(() => {
    if (!visible) {
      return;
    }
    if (syncCreateVm.wizardOpen || syncCreateVm.authSelectModalOpen) {
      setVisible(false);
    }
  }, [visible, syncCreateVm.wizardOpen, syncCreateVm.authSelectModalOpen]);

  function loadFormData() {
    KnowledgeBaseServiceApi()
      .datasetServiceAllDatasetTags()
      .then((res) => {
        setTags(res.data.tags || []);
      });

    return KnowledgeBaseServiceApi()
      .datasetServiceListAlgos()
      .then((res) => {
        const list = res.data.algos;
        setAlgorithm(list || []);
        if (list?.length === 1) {
          form.setFieldsValue({ algo_id: list[0].algo_id });
        }
      })
      .catch((err) => {
        console.error("Failed to load algorithm list:", err);
      });
  }

  function onOpen(tab: CreateTab = "direct") {
    setActiveTab(tab);
    setHasTagLengthError(false);
    form.resetFields();
    loadFormData().finally(() => {
      setVisible(true);
    });
  }

  function onCancel() {
    form.resetFields();
    setHasTagLengthError(false);
    setActiveTab("direct");
    setVisible(false);
  }

  const handleTagLengthErrorChange = useCallback(
    (hasError: boolean) => {
      setHasTagLengthError(hasError);
      form.setFields([
        {
          name: "tags",
          errors: hasError ? [t("knowledge.knowledgeTagMaxLength")] : [],
        },
      ]);
    },
    [form, t],
  );

  function onOk() {
    if (activeTab !== "direct") {
      return;
    }

    if (hasTagLengthError) {
      form.setFields([
        { name: "tags", errors: [t("knowledge.knowledgeTagMaxLength")] },
      ]);
      return;
    }

    form.validateFields().then(async (values) => {
      const params = { ...values };
      const selectedAlgoId =
        params.algo_id ||
        (algorithm.length === 1 ? algorithm[0]?.algo_id : undefined);
      params.algo = algorithm.find((item) => item.algo_id === selectedAlgoId);
      if (selectedAlgoId) {
        params.algo_id = selectedAlgoId;
      }
      delete params.algo_id;

      if (loading) {
        return;
      }

      setLoading(true);
      try {
        await onCreate(params);
        onCancel();
      } catch (error) {
        console.error("Create knowledge base error: ", error);
      } finally {
        setLoading(false);
      }
    });
  }

  return (
    <Modal
      open={visible}
      title={t("knowledge.createKnowledgeBase")}
      centered
      destroyOnHidden
      width={CREATE_MODAL_WIDTH}
      className="knowledge-create-modal"
      footer={
        <div className="knowledge-create-modal-footer">
          {activeTab === "direct" ? (
            <>
              <Button onClick={onCancel}>{t("common.cancel")}</Button>
              <Button type="primary" loading={loading} onClick={onOk}>
                {t("common.confirm")}
              </Button>
            </>
          ) : (
            <span className="knowledge-create-modal-footer-spacer" aria-hidden="true" />
          )}
        </div>
      }
      onCancel={onCancel}
    >
      <Tabs
        activeKey={activeTab}
        className="knowledge-create-modal-tabs"
        items={[
          {
            key: "direct",
            label: t("knowledge.createDirect"),
            children: (
              <Form form={form} layout="vertical">
                <Form.Item
                  name="display_name"
                  label={t("knowledge.knowledgeBaseName")}
                  required
                  rules={[
                    {
                      required: true,
                      message: t("knowledge.inputKnowledgeBaseName"),
                    },
                    {
                      pattern: KNOWLEDGE_BASE_NAME_PATTERN,
                      message: t("knowledge.knowledgeNameRule"),
                    },
                  ]}
                >
                  <Input
                    placeholder={t("knowledge.knowledgeNameRule")}
                    maxLength={KNOWLEDGE_BASE_NAME_MAX_LENGTH}
                  />
                </Form.Item>
                <Form.Item name="desc" label={t("knowledge.knowledgeDesc")}>
                  <TextArea
                    placeholder={t("knowledge.maxLength300Chars")}
                    showCount
                    maxLength={300}
                    autoSize={{ minRows: 2, maxRows: 6 }}
                  />
                </Form.Item>
                {algorithm.length !== 1 && (
                  <Form.Item
                    name="algo_id"
                    label={t("knowledge.parseAlgorithm")}
                    initialValue={null}
                    rules={[
                      {
                        required: true,
                        message: t("knowledge.selectParseAlgorithm"),
                      },
                    ]}
                  >
                    <Select
                      options={algorithm.map((item) => ({
                        label: item.display_name,
                        value: item.algo_id,
                      }))}
                      placeholder={t("knowledge.selectParseAlgorithm")}
                    />
                  </Form.Item>
                )}
                <Form.Item
                  name="tags"
                  label={t("knowledge.knowledgeTags")}
                  rules={[
                    {
                      required: true,
                      message: t("knowledge.selectKnowledgeTags"),
                    },
                    {
                      validator: (_, value?: string[]) => {
                        const hasOverLengthTag = (value || []).some(
                          (tag) =>
                            Array.from(tag).length > KNOWLEDGE_TAG_MAX_LENGTH,
                        );
                        return hasOverLengthTag
                          ? Promise.reject(
                              new Error(t("knowledge.knowledgeTagMaxLength")),
                            )
                          : Promise.resolve();
                      },
                    },
                  ]}
                  validateStatus={hasTagLengthError ? "error" : undefined}
                  help={
                    hasTagLengthError
                      ? t("knowledge.knowledgeTagMaxLength")
                      : undefined
                  }
                >
                  <TagSelect
                    tags={tags}
                    maxTagLength={KNOWLEDGE_TAG_MAX_LENGTH}
                    maxTagLengthMessage={t("knowledge.knowledgeTagMaxLength")}
                    showOverLengthInputError
                    onLengthErrorChange={handleTagLengthErrorChange}
                  />
                </Form.Item>
              </Form>
            ),
          },
          {
            key: "cloud",
            label: t("knowledge.createFromCloudDisk"),
            children: (
              <div className="knowledge-create-modal-cloud">
                <Paragraph className="data-source-create-provider-intro">
                  {t("knowledge.createFromCloudDocumentsIntro")}
                </Paragraph>
                <DataSourceProviderPicker vm={syncCreateVm} />
              </div>
            ),
          },
        ]}
        onChange={(key) => setActiveTab(key as CreateTab)}
      />
    </Modal>
  );
});

CreateKnowledgeBaseModal.displayName = "CreateKnowledgeBaseModal";

export default CreateKnowledgeBaseModal;
