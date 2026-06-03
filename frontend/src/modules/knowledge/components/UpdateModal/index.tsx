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

import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";
import TagSelect from "../TagSelect";

const { TextArea } = Input;
const KNOWLEDGE_TAG_MAX_LENGTH = 20;

export interface ForwardProps {
  onUpdate: (dataset: Dataset) => Promise<void>;
}

export interface UpdateImperativeProps {
  onOpen: (data?: Dataset) => void;
}

const UpdateAppModel = forwardRef<UpdateImperativeProps, ForwardProps>(
  ({ onUpdate }, ref) => {
    const { t } = useTranslation();
    const [visible, setVisible] = useState(false);
    const [loading, setLoading] = useState(false);
    const [data, setData] = useState<Dataset>();
    const [tags, setTags] = useState<string[]>([]);
    const [algorithm, setAlgorithm] = useState<Algo[]>([]);
    const [hasTagLengthError, setHasTagLengthError] = useState(false);

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
          console.error('Failed to load algorithm list:', err);
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
      if (sourceData) {
        form.setFieldsValue({
          ...sourceData,
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
            errors: hasError
              ? [t("knowledge.knowledgeTagMaxLength")]
              : [],
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
        const selectedAlgoId =
          params.algo_id || (algorithm.length === 1 ? algorithm[0]?.algo_id : undefined);
        params.algo =
          algorithm.find((item) => item.algo_id === selectedAlgoId) || data?.algo;
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
              { required: true, message: t("knowledge.inputKnowledgeBaseName") },

              {
                pattern: /^[\u4e00-\u9fa5a-zA-Z0-9-_\.]{1,100}$/, // eslint-disable-line
                message: t("knowledge.knowledgeNameRule"),
              },
            ]}
          >
            <Input
              placeholder={
                t("knowledge.knowledgeNameRule")
              }
              maxLength={100}
            />
          </Form.Item>
          <Form.Item
            name="desc"
            label={t("knowledge.knowledgeDesc")}
          >
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
              rules={[{ required: true, message: t("knowledge.selectParseAlgorithm") }]}
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
