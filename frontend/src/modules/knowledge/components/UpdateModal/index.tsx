import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useState,
} from "react";
import { Modal, Form, Input, Select } from "antd";
import { useTranslation } from "react-i18next";
import { Dataset, Algo } from "@/api/generated/knowledge-client";
import type { Dataset as CoreDataset } from "@/api/generated/core-client";

import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";
import {
  KNOWLEDGE_BASE_NAME_MAX_LENGTH,
  KNOWLEDGE_BASE_NAME_PATTERN,
} from "@/modules/knowledge/constants/validation";
import TagSelect from "../TagSelect";
import {
  effectiveProcessingLevel,
  PROCESSING_LEVEL_ORDER,
  type ProcessingLevel,
} from "@/modules/knowledge/utils/processingLevel";
import { getKnowledgeBaseCapabilities, getLearningCatalog, saveKnowledgeBaseCapabilities, type LearningCapability } from "@/modules/learning/api";
import CapabilitySettings, { capabilityRefsFromForm, parseCapabilitySettings } from "@/modules/learning/CapabilitySettings";

const { TextArea } = Input;
const KNOWLEDGE_TAG_MAX_LENGTH = 20;

export interface ForwardProps {
  onUpdate: (dataset: Dataset & { processing_level?: ProcessingLevel }) => Promise<CoreDataset | void>;
  embeddingReady?: boolean | null;
}

export interface UpdateImperativeProps {
  onOpen: (data?: Dataset) => void;
}

const UpdateAppModel = forwardRef<UpdateImperativeProps, ForwardProps>(
  ({ onUpdate, embeddingReady }, ref) => {
    const { t } = useTranslation();
    const [visible, setVisible] = useState(false);
    const [loading, setLoading] = useState(false);
    const [data, setData] = useState<Dataset>();
    const [tags, setTags] = useState<string[]>([]);
    const [algorithm, setAlgorithm] = useState<Algo[]>([]);
    const [hasTagLengthError, setHasTagLengthError] = useState(false);
    const [learningCapabilities, setLearningCapabilities] = useState<LearningCapability[]>([]);

    const [form] = Form.useForm();
    useImperativeHandle(ref, () => ({
      onOpen,
    }));

    useEffect(() => {
      // If there is only one parse algorithm, auto-select it and hide the selector.
      if (!visible || algorithm.length !== 1) {
        return;
      }
      const currentAlgoId = form.getFieldValue("algo_id");
      if (!currentAlgoId) {
        form.setFieldsValue({ algo_id: algorithm[0].algo_id });
      }
    }, [algorithm, visible, form]);

    function getAlgorithm(sourceData?: Dataset) {
      return KnowledgeBaseServiceApi()
        .datasetServiceListAlgos()
        .then((res) => {
          const list = res.data.algos;
          setAlgorithm(list || []);
          const sourceAlgoId = sourceData?.algo?.algo_id;
          if (list?.length === 1) {
            form.setFieldsValue({
              algo_id: sourceAlgoId || list[0].algo_id,
            });
          } else if (sourceAlgoId) {
            form.setFieldsValue({ algo_id: sourceAlgoId });
          }
        })
        .catch((err) => {
          console.error("Failed to load algorithm list:", err);
        });
    }

    function getTags() {
      KnowledgeBaseServiceApi()
        .datasetServiceAllDatasetTags()
        .then((res) => {
          setTags(res.data.tags || []);
        });
    }

    function onOpen(sourceData: Dataset | undefined) {
      getTags();
      setData(sourceData);
      setHasTagLengthError(false);
      getLearningCatalog().then((catalog) => setLearningCapabilities(catalog.capabilities)).catch(() => setLearningCapabilities([]));
      if (sourceData?.dataset_id) {
        getKnowledgeBaseCapabilities(sourceData.dataset_id).then((items) => form.setFieldsValue({learning_capability_keys:items.filter((item)=>item.enabled).sort((a,b)=>a.display_order-b.display_order).map((item)=>item.capability_key),learning_capability_settings:parseCapabilitySettings(items)})).catch(() => form.setFieldValue("learning_capability_keys", []));
      } else {
        form.setFieldValue("learning_capability_keys", ["general_translation"]);
      }
      if (sourceData) {
        form.setFieldsValue({
          ...sourceData,
          processing_level: effectiveProcessingLevel(
            (sourceData as Dataset & { processing_level?: ProcessingLevel })
              .processing_level,
          ),
          algo_id: sourceData?.algo?.algo_id,
          industry: sourceData?.industry,
        });
      }
      // Show the modal only after the algo list is loaded so the selector
      // visibility (algorithm.length !== 1) is evaluated with real data,
      // not with the initial empty array.
      getAlgorithm(sourceData).finally(() => {
        setVisible(true);
      });
    }

    function onCancel() {
      form.resetFields();
      setHasTagLengthError(false);
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
      if (hasTagLengthError) {
        form.setFields([
          { name: "tags", errors: [t("knowledge.knowledgeTagMaxLength")] },
        ]);
        return;
      }
      form.validateFields().then(async (values) => {
        const params = { ...values };
        const learningCapabilityKeys = params.learning_capability_keys || [];
        const learningCapabilitySettings = params.learning_capability_settings || {};
        delete params.learning_capability_keys;
        delete params.learning_capability_settings;
        const selectedAlgoId =
          params.algo_id ||
          (algorithm.length === 1 ? algorithm[0]?.algo_id : undefined);
        params.algo =
          algorithm.find((item) => item.algo_id === selectedAlgoId) ||
          data?.algo;
        if (selectedAlgoId) {
          params.algo_id = selectedAlgoId;
        }
        delete params.algo_id;
        if (loading) {
          return;
        }
        setLoading(true);
        try {
          await onUpdate({ ...params, dataset_id: data?.dataset_id });
          if (data?.dataset_id) {
            await saveKnowledgeBaseCapabilities(data.dataset_id, capabilityRefsFromForm(learningCapabilityKeys,learningCapabilitySettings,learningCapabilities));
          }
          setLoading(false);
          onCancel();
        } catch (error) {
          setLoading(false);
          console.error("Update knowledge base error: ", error);
        }
      });
    }

    return (
      <Modal
        open={visible}
        title={
          data
            ? t("knowledge.editKnowledgeBase")
            : t("knowledge.createKnowledgeBase")
        }
        centered
        onCancel={onCancel}
        onOk={onOk}
        width={576}
        okButtonProps={{ disabled: loading }}
      >
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
          <Form.Item name="learning_capability_keys" label={t("learning.knowledgeBaseCapabilities")} extra={data?.dataset_id ? t("learning.knowledgeBaseCapabilitiesHint") : t("learning.knowledgeBaseCapabilitiesCreateHint")}>
            <Select mode="multiple" options={learningCapabilities.map((item) => ({ value:item.key, label:t(item.name_i18n_key) }))} placeholder={t("learning.selectCapabilities")} />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(a,b)=>a.learning_capability_keys!==b.learning_capability_keys}>{({getFieldValue})=><CapabilitySettings capabilities={learningCapabilities} selectedKeys={getFieldValue("learning_capability_keys") || []}/>}</Form.Item>
          <Form.Item
            name="processing_level"
            label={t("knowledge.processingLevel")}
            extra={t("knowledge.processingLevelSaveHint")}
          >
            <Select
              options={PROCESSING_LEVEL_ORDER.map((level) => ({
                value: level,
                label: t(`knowledge.processing${level[0].toUpperCase()}${level.slice(1)}`),
                disabled: level === "indexed" && embeddingReady === false,
              }))}
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
                disabled={!!data?.dataset_id}
                placeholder={t("knowledge.selectParseAlgorithm")}
              />
            </Form.Item>
          )}
          <Form.Item
            name="tags"
            label={t("knowledge.knowledgeTags")}
            rules={[
              { required: true, message: t("knowledge.selectKnowledgeTags") },
              {
                validator: (_, value?: string[]) => {
                  const hasOverLengthTag = (value || []).some(
                    (tag) => Array.from(tag).length > KNOWLEDGE_TAG_MAX_LENGTH,
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
      </Modal>
    );
  },
);

UpdateAppModel.displayName = "UpdateAppModel";

export default UpdateAppModel;
