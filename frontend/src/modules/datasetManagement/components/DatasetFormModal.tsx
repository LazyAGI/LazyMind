import { useEffect, useMemo } from "react";
import { Form, Input, Modal, Select } from "antd";
import type {
  DatasetFormValues,
  DatasetListItem,
  KnowledgeBaseOption,
} from "../shared";

const { TextArea } = Input;

interface DatasetFormModalProps {
  open: boolean;
  mode: "create" | "edit";
  dataset?: DatasetListItem | null;
  knowledgeBases: KnowledgeBaseOption[];
  submitting?: boolean;
  onCancel: () => void;
  onSubmit: (values: DatasetFormValues) => void;
}

export default function DatasetFormModal({
  open,
  mode,
  dataset,
  knowledgeBases,
  submitting,
  onCancel,
  onSubmit,
}: DatasetFormModalProps) {
  const [form] = Form.useForm<DatasetFormValues>();

  const title = mode === "create" ? "新建数据集" : "编辑数据集";

  const initialValues = useMemo<Partial<DatasetFormValues>>(() => {
    if (!dataset) {
      return {};
    }
    return {
      name: dataset.name,
      description: dataset.description,
      knowledge_base_ids: dataset.knowledge_bases?.map((item) => item.id) || [],
    };
  }, [dataset]);

  useEffect(() => {
    if (open) {
      form.setFieldsValue(initialValues);
    } else {
      form.resetFields();
    }
  }, [form, initialValues, open]);

  const handleSubmit = async () => {
    const values = await form.validateFields();
    onSubmit(values);
  };

  return (
    <Modal
      destroyOnClose
      open={open}
      title={title}
      okText="保存"
      cancelText="取消"
      confirmLoading={submitting}
      width={720}
      onCancel={onCancel}
      onOk={handleSubmit}
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={initialValues}
        className="dataset-form"
      >
        <Form.Item
          name="name"
          label="数据集名称"
          rules={[
            { required: true, whitespace: true, message: "请输入数据集名称" },
            { max: 80, message: "数据集名称不能超过 80 个字符" },
          ]}
        >
          <Input placeholder="请输入数据集名称" />
        </Form.Item>

        <Form.Item
          name="description"
          label="数据集描述"
          rules={[{ max: 500, message: "数据集描述不能超过 500 个字符" }]}
        >
          <TextArea rows={3} placeholder="请输入数据集描述" />
        </Form.Item>

        <Form.Item
          name="knowledge_base_ids"
          label="关联知识库"
          rules={[{ required: true, message: "请选择关联知识库" }]}
        >
          <Select
            mode="multiple"
            allowClear
            placeholder="请选择知识库"
            options={knowledgeBases.map((item) => ({
              label: item.name,
              value: item.id,
            }))}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
