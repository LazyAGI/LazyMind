import { useEffect } from "react";
import { Button, Form, Input, Select, Space, Typography } from "antd";
import type { DatasetItem, DatasetItemFormValues } from "../shared";
import { questionTypeOptions, sourceLabelMap } from "../shared";
import { joinListField } from "../utils/datasetValidation";

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
            label="问题"
            rules={[{ required: true, whitespace: true, message: "问题不能为空" }]}
          >
            <Input placeholder="请输入问题" />
          </Form.Item>
          <Form.Item
            name="question_type"
            label="问题类型"
            rules={[{ required: true, message: "问题类型不能为空" }]}
          >
            <Select
              showSearch
              placeholder="请选择问题类型"
              options={questionTypeOptions.map((value) => ({ label: value, value }))}
              optionFilterProp="label"
            />
          </Form.Item>
        </div>

        <Form.Item
          name="ground_truth"
          label="标准答案"
          rules={[{ required: true, whitespace: true, message: "标准答案不能为空" }]}
        >
          <TextArea rows={4} placeholder="请输入标准答案" />
        </Form.Item>

        <Form.Item name="key_points" label="答案要点">
          <TextArea rows={3} placeholder="请输入答案要点" />
        </Form.Item>

        <Form.Item name="reference_context" label="参考上下文">
          <TextArea rows={4} placeholder="请输入参考上下文" />
        </Form.Item>

        <div className="dataset-editor-grid dataset-editor-grid-single">
          <Form.Item name="reference_doc" label="参考文档">
            <Input placeholder="请输入参考文档" />
          </Form.Item>
        </div>

        <Form.Item name="generate_reason" label="生成依据">
          <TextArea rows={3} placeholder="请输入生成依据" />
        </Form.Item>

        <div className="dataset-editor-footer">
          <Space size="middle" wrap>
            <Text type="secondary">
              数据来源：{item?.source ? sourceLabelMap[item.source] : isNew ? "手动" : "-"}
            </Text>
            {item?.source_session_id ? (
              <Text type="secondary">来源会话：{item.source_session_id}</Text>
            ) : null}
          </Space>
          <Space>
            <Button onClick={onCancel}>取消</Button>
            <Button type="primary" loading={saving} onClick={handleSave}>
              保存
            </Button>
          </Space>
        </div>
      </Form>
    </div>
  );
}
