import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Modal,
  Steps,
  Table,
  Upload,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { InboxOutlined } from "@ant-design/icons";
import type {
  DatasetImportResultState,
  DatasetImportRow,
  DatasetItem,
  DatasetItemField,
  ImportStep,
} from "../shared";
import DatasetTemplateDownload from "./DatasetTemplateDownload";
import {
  buildImportPreview,
  createAutoFieldMapping,
  getFileKind,
  getMissingRequiredMappings,
  parseDatasetFile,
} from "../utils/datasetImport";

const importSteps: Array<{ key: ImportStep; title: string }> = [
  { key: "selectFile", title: "选择文件" },
  { key: "preview", title: "数据预览" },
  { key: "result", title: "导入结果" },
];

const fieldLabels: Record<DatasetItemField, string> = {
  case_id: "Case ID",
  question: "问题",
  question_type: "问题类型",
  ground_truth: "标准答案",
  key_points: "答案要点",
  reference_context: "参考上下文",
  reference_doc: "参考文档",
  reference_doc_ids: "参考文档 ID",
  reference_chunk_ids: "参考片段 ID",
  generate_reason: "生成依据",
  is_deleted: "是否删除",
};

interface DatasetImportModalProps {
  open: boolean;
  initialFile?: File | null;
  onCancel: () => void;
  onImported: (
    items: Array<Partial<DatasetItem>>,
    result: DatasetImportResultState,
    file: File | null,
  ) => void;
}

export default function DatasetImportModal({
  open,
  initialFile,
  onCancel,
  onImported,
}: DatasetImportModalProps) {
  const [step, setStep] = useState<ImportStep>("selectFile");
  const [file, setFile] = useState<File | null>(null);
  const [previewRows, setPreviewRows] = useState<DatasetImportRow[]>([]);
  const [onlyErrors, setOnlyErrors] = useState(false);
  const [parsing, setParsing] = useState(false);
  const [importing, setImporting] = useState(false);
  const [result, setResult] = useState<DatasetImportResultState | null>(null);

  useEffect(() => {
    if (!open) {
      setStep("selectFile");
      setFile(null);
      setPreviewRows([]);
      setOnlyErrors(false);
      setImporting(false);
      setResult(null);
      return;
    }
    if (initialFile) {
      setFile(initialFile);
    }
  }, [initialFile, open]);

  const currentStepIndex = importSteps.findIndex((item) => item.key === step);
  const visiblePreviewRows = onlyErrors
    ? previewRows.filter((row) => row.errors.length > 0)
    : previewRows;

  const parseCurrentFile = async () => {
    if (!file) {
      message.error("请先选择文件");
      return;
    }
    setParsing(true);
    try {
      const rows = await parseDatasetFile(file);
      if (rows.length === 0) {
        message.error("文件中没有可导入的数据");
        return;
      }
      const fieldSet = new Set<string>();
      rows.forEach((row) => {
        Object.keys(row).forEach((field) => fieldSet.add(field));
      });
      const fields = Array.from(fieldSet);
      const mapping = createAutoFieldMapping(fields);
      const missing = getMissingRequiredMappings(mapping);
      if (missing.length > 0) {
        message.error(`必填字段未识别：${missing.map((field) => fieldLabels[field]).join("、")}`);
        return;
      }
      setPreviewRows(buildImportPreview(rows, mapping));
      setOnlyErrors(false);
      setStep("preview");
    } catch (error: any) {
      message.error(error?.message || "文件解析失败");
    } finally {
      setParsing(false);
    }
  };

  const handleConfirmImport = async () => {
    const successRows = previewRows.filter((row) => row.errors.length === 0);
    if (successRows.length === 0) {
      message.error("没有可导入的有效数据");
      return;
    }
    const nextResult = {
      successCount: successRows.length,
      failedCount: previewRows.length - successRows.length,
      failedRows: previewRows.filter((row) => row.errors.length > 0),
    };
    setImporting(true);
    try {
      await onImported(successRows.map((row) => row.normalized), nextResult, file);
      setResult(nextResult);
      setStep("result");
    } catch (error: any) {
      message.error(error?.message || "导入失败");
    } finally {
      setImporting(false);
    }
  };

  const previewColumns: ColumnsType<DatasetImportRow> = [
    { title: "行号", dataIndex: "rowIndex", width: 80 },
    {
      title: "question",
      render: (_, row) => row.normalized.question || "-",
      ellipsis: true,
    },
    {
      title: "question_type",
      render: (_, row) => row.normalized.question_type || "-",
      width: 150,
    },
    {
      title: "ground_truth",
      render: (_, row) => row.normalized.ground_truth || "-",
      ellipsis: true,
    },
    {
      title: "校验结果",
      render: (_, row) =>
        row.errors.length > 0 ? (
          <span className="dataset-import-error-text">{row.errors.join("；")}</span>
        ) : (
          <span className="dataset-import-success-text">通过</span>
        ),
      width: 220,
    },
  ];

  const footer = (() => {
    if (step === "selectFile") {
      return [
        <Button key="cancel" onClick={onCancel}>
          取消
        </Button>,
        <Button key="next" type="primary" loading={parsing} onClick={parseCurrentFile}>
          下一步
        </Button>,
      ];
    }
    if (step === "preview") {
      return [
        <Button key="prev" onClick={() => setStep("selectFile")}>
          上一步
        </Button>,
        <Button key="confirm" type="primary" loading={importing} onClick={handleConfirmImport}>
          确认导入
        </Button>,
      ];
    }
    return [
      <Button key="done" type="primary" onClick={onCancel}>
        完成
      </Button>,
    ];
  })();

  return (
    <Modal
      destroyOnClose
      open={open}
      title="导入数据"
      width={920}
      footer={footer}
      onCancel={onCancel}
    >
      <Steps
        className="dataset-import-steps"
        current={currentStepIndex}
        items={importSteps.map((item) => ({ title: item.title }))}
      />

      {step === "selectFile" ? (
        <div className="dataset-import-step">
          <DatasetTemplateDownload />
          <Upload.Dragger
            accept=".xlsx,.xls,.csv,.json,.numbers"
            maxCount={1}
            fileList={file ? [{ uid: file.name, name: file.name, status: "done" }] : []}
            beforeUpload={(nextFile) => {
              const kind = getFileKind(nextFile);
              if (kind === "numbers") {
                message.error("暂不支持 Numbers 文件，请先导出为 Excel 或 CSV 后再上传。");
                return Upload.LIST_IGNORE;
              }
              if (kind === "unknown") {
                message.error("仅支持 Excel、CSV、JSON 文件。");
                return Upload.LIST_IGNORE;
              }
              setFile(nextFile);
              return false;
            }}
            onRemove={() => setFile(null)}
          >
            <p className="ant-upload-drag-icon">
              <InboxOutlined />
            </p>
            <p className="ant-upload-text">拖拽文件到这里，或点击选择文件</p>
            <p className="ant-upload-hint">
              支持 .xlsx / .xls / .csv / .json，一次只能上传一个文件
            </p>
          </Upload.Dragger>
        </div>
      ) : null}

      {step === "preview" ? (
        <div className="dataset-import-step">
          <div className="dataset-import-preview-toolbar">
            <span>
              共解析 {previewRows.length} 条，错误{" "}
              {previewRows.filter((row) => row.errors.length > 0).length} 条
            </span>
            <Button onClick={() => setOnlyErrors((value) => !value)}>
              {onlyErrors ? "查看全部" : "只看错误"}
            </Button>
          </div>
          <Table
            size="small"
            rowKey="rowIndex"
            columns={previewColumns}
            dataSource={visiblePreviewRows}
            pagination={{ pageSize: 6 }}
          />
        </div>
      ) : null}

      {step === "result" ? (
        <div className="dataset-import-result">
          <h3>导入完成</h3>
          <p>成功：{result?.successCount || 0} 条</p>
          <p>失败：{result?.failedCount || 0} 条</p>
          {result?.failedRows.length ? (
            <Alert
              type="warning"
              showIcon
              message="存在失败行"
              description="失败行已在预览页标记，可返回修正文件后重新导入。"
            />
          ) : null}
        </div>
      ) : null}
    </Modal>
  );
}
