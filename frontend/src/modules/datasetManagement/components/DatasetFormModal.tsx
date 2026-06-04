import { useEffect, useMemo } from "react";
import { Form, Input, Modal, Radio, Select, Upload, message } from "antd";
import { InboxOutlined } from "@ant-design/icons";
import type { UploadFile } from "antd";
import type {
  DatasetFormValues,
  DatasetListItem,
  KnowledgeBaseOption,
} from "../shared";
import DatasetTemplateDownload from "./DatasetTemplateDownload";
import { getFileKind } from "../utils/datasetImport";

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

function toUploadFile(file: File): UploadFile {
  return {
    uid: `${file.name}-${file.lastModified}`,
    name: file.name,
    status: "done",
    originFileObj: file as any,
  };
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
  const createMethod = Form.useWatch("create_method", form);
  const isUploadCreate = mode === "create" && createMethod === "upload";

  const title = mode === "create" ? "新建数据集" : "编辑数据集";
  const okText = mode === "create" && createMethod === "upload" ? "下一步" : "保存";

  const initialValues = useMemo<Partial<DatasetFormValues>>(() => {
    if (!dataset) {
      return {
        create_method: "manual",
      };
    }
    return {
      name: dataset.name,
      description: dataset.description,
      knowledge_base_ids: dataset.knowledge_bases?.map((item) => item.id) || [],
      create_method: "manual",
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
      okText={okText}
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

        {mode === "create" ? (
          <Form.Item
            name="create_method"
            label="创建方式"
            rules={[{ required: true, message: "请选择创建方式" }]}
          >
            <Radio.Group>
              <Radio.Button value="manual" aria-label="手动创建">
                手动创建
              </Radio.Button>
              <Radio.Button value="upload" aria-label="上传文件创建">
                上传文件创建
              </Radio.Button>
            </Radio.Group>
          </Form.Item>
        ) : null}

        {isUploadCreate ? (
          <div className="dataset-upload-create-panel">
            <div className="dataset-upload-template-row">
              <span className="dataset-upload-template-label">下载模版</span>
              <DatasetTemplateDownload />
            </div>
            <Form.Item
              name="uploadFile"
              label="上传文件"
              valuePropName="fileList"
              getValueFromEvent={(event) => event?.fileList || []}
              rules={[{ required: true, message: "请上传一个数据集文件" }]}
            >
              <Upload.Dragger
                accept=".xlsx,.xls,.csv,.json,.numbers"
                maxCount={1}
                beforeUpload={(file) => {
                  const kind = getFileKind(file);
                  if (kind === "numbers") {
                    message.error("暂不支持 Numbers 文件，请先导出为 Excel 或 CSV 后再上传。");
                    return Upload.LIST_IGNORE;
                  }
                  if (kind === "unknown") {
                    message.error("仅支持 Excel、CSV、JSON 文件。");
                    return Upload.LIST_IGNORE;
                  }
                  form.setFieldValue("uploadFile", [toUploadFile(file)]);
                  return false;
                }}
              >
                <p className="ant-upload-drag-icon">
                  <InboxOutlined />
                </p>
                <p className="ant-upload-text">拖拽文件到这里，或点击选择文件</p>
                <p className="ant-upload-hint">
                  支持 .xlsx / .xls / .csv / .json，一次只能上传一个文件
                </p>
              </Upload.Dragger>
            </Form.Item>
          </div>
        ) : null}
      </Form>
    </Modal>
  );
}
