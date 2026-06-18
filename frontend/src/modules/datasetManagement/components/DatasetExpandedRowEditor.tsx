import { useEffect } from "react";
import { Button, Form, Input, Space, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { DatasetItem, DatasetItemFormValues } from "../shared";
import { sourceLabelI18nKeys } from "../shared";
import { joinListField } from "../utils/datasetValidation";
import QuestionTypeSelect from "./QuestionTypeSelect";

const { TextArea } = Input;
const { Text } = Typography;

interface DatasetExpandedRowEditorProps {
  item?: DatasetItem;
  initialValues?: Partial<DatasetItemFormValues>;
  isNew?: boolean;
  saving?: boolean;
  onDirtyChange?: (dirty: boolean) => void;
  onSave: (values: DatasetItemFormValues) => void;
  onCancel: () => void;
}

function getInitialValues(item?: DatasetItem, initialValues?: Partial<DatasetItemFormValues>) {
  if (initialValues) {
    return initialValues;
  }
  return {
    case_id: item?.case_id || "",
    question: item?.question || "",
    question_type: item?.question_type || "",
    ground_truth: item?.ground_truth || "",
    key_points: item?.key_points || "",
    reference_context: item?.reference_context || "",
    reference_doc: item?.reference_doc || "",
    reference_doc_ids: joinListField(item?.reference_doc_ids),
    reference_chunk_ids: joinListField(item?.reference_chunk_ids),
    generate_reason: item?.generate_reason || "",
    is_deleted: Boolean(item?.is_deleted),
  };
}

export default function DatasetExpandedRowEditor({
  item,
  initialValues,
  isNew,
  saving,
  onDirtyChange,
  onSave,
  onCancel,
}: DatasetExpandedRowEditorProps) {
  const { t } = useTranslation();
  const [form] = Form.useForm<DatasetItemFormValues>();

  useEffect(() => {
    form.setFieldsValue(getInitialValues(item, initialValues));
    onDirtyChange?.(false);
  }, [form, initialValues, item, onDirtyChange]);

  const handleSave = async () => {
    const values = await form.validateFields();
    onSave({
      ...values,
      case_id: item?.case_id || values.case_id,
      reference_doc_ids: joinListField(item?.reference_doc_ids) || values.reference_doc_ids,
      reference_chunk_ids:
        joinListField(item?.reference_chunk_ids) || values.reference_chunk_ids,
    });
  };

  return (
    <div className="dataset-expanded-editor">
      <Form
        form={form}
        layout="vertical"
        onValuesChange={() => onDirtyChange?.(true)}
      >
        <div className="dataset-editor-grid">
          <Form.Item
            name="question"
            label={t("datasetManagement.fields.question")}
            rules={[{
              required: true,
              whitespace: true,
              message: t("datasetManagement.validation.questionRequired"),
            }]}
          >
            <Input placeholder={t("datasetManagement.detail.placeholders.question")} />
          </Form.Item>
          <Form.Item
            name="question_type"
            label={t("datasetManagement.fields.questionType")}
            rules={[{
              required: true,
              message: t("datasetManagement.validation.questionTypeRequired"),
            }]}
          >
            <QuestionTypeSelect placeholder={t("datasetManagement.detail.placeholders.questionType")} />
          </Form.Item>
        </div>

        <Form.Item
          name="ground_truth"
          label={t("datasetManagement.fields.groundTruth")}
          rules={[{
            required: true,
            whitespace: true,
            message: t("datasetManagement.validation.groundTruthRequired"),
          }]}
        >
          <TextArea rows={4} placeholder={t("datasetManagement.detail.placeholders.groundTruth")} />
        </Form.Item>

        <Form.Item name="key_points" label={t("datasetManagement.fields.keyPoints")}>
          <TextArea rows={3} placeholder={t("datasetManagement.detail.placeholders.keyPoints")} />
        </Form.Item>

        <Form.Item name="reference_context" label={t("datasetManagement.fields.referenceContext")}>
          <TextArea rows={4} placeholder={t("datasetManagement.detail.placeholders.referenceContext")} />
        </Form.Item>

        <div className="dataset-editor-grid dataset-editor-grid-single">
          <Form.Item name="reference_doc" label={t("datasetManagement.fields.referenceDoc")}>
            <Input placeholder={t("datasetManagement.detail.placeholders.referenceDoc")} />
          </Form.Item>
        </div>

        <Form.Item name="generate_reason" label={t("datasetManagement.fields.generateReason")}>
          <TextArea rows={3} placeholder={t("datasetManagement.fields.generateReason")} />
        </Form.Item>

        <div className="dataset-editor-footer">
          <Space size="middle" wrap>
            <Text type="secondary">
              {t("datasetManagement.editor.dataSource")}:
              {" "}
              {item?.source
                ? t(sourceLabelI18nKeys[item.source])
                : isNew
                  ? t(sourceLabelI18nKeys.manual)
                  : "-"}
            </Text>
            {item?.source_session_id ? (
              <Text type="secondary">
                {t("datasetManagement.editor.sourceSession")}: {item.source_session_id}
              </Text>
            ) : null}
          </Space>
          <Space>
            <Button onClick={onCancel}>{t("common.cancel")}</Button>
            <Button type="primary" loading={saving} onClick={handleSave}>
              {t("common.save")}
            </Button>
          </Space>
        </div>
      </Form>
    </div>
  );
}
