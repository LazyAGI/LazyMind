import { useEffect, useMemo } from "react";
import { Form, Input, Modal, Select } from "antd";
import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();

  const title =
    mode === "create"
      ? t("datasetManagement.form.createTitle")
      : t("datasetManagement.form.editTitle");

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
      form.resetFields();
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
      okText={t("common.save")}
      cancelText={t("common.cancel")}
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
          label={t("datasetManagement.fields.datasetName")}
          rules={[
            {
              required: true,
              whitespace: true,
              message: t("datasetManagement.form.validation.nameRequired"),
            },
            { max: 80, message: t("datasetManagement.form.validation.nameMax") },
          ]}
        >
          <Input placeholder={t("datasetManagement.form.namePlaceholder")} />
        </Form.Item>

        <Form.Item
          name="description"
          label={t("datasetManagement.fields.datasetDescription")}
          rules={[{ max: 500, message: t("datasetManagement.form.validation.descriptionMax") }]}
        >
          <TextArea rows={3} placeholder={t("datasetManagement.form.descriptionPlaceholder")} />
        </Form.Item>

        <Form.Item
          name="knowledge_base_ids"
          label={t("datasetManagement.fields.knowledgeBase")}
          rules={[{ required: true, message: t("datasetManagement.form.validation.knowledgeBaseRequired") }]}
        >
          <Select
            mode="multiple"
            allowClear
            placeholder={t("datasetManagement.form.knowledgeBasePlaceholder")}
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
