import { useEffect, useMemo, useState } from "react";
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
import { useTranslation } from "react-i18next";
import type {
  DatasetImportResultState,
  DatasetImportRow,
  DatasetItem,
  DatasetItemField,
  ImportStep,
} from "../shared";
import {
  datasetItemFieldI18nKeys,
  datasetItemFields,
} from "../shared";
import DatasetTemplateDownload from "./DatasetTemplateDownload";
import {
  type DatasetImportMessages,
  buildImportPreview,
  createAutoFieldMapping,
  getFileKind,
  getMissingRequiredMappings,
  parseDatasetFile,
} from "../utils/datasetImport";

const hiddenImportPreviewFields = new Set<DatasetItemField>([
  "case_id",
  "reference_doc_ids",
  "reference_chunk_ids",
]);
const visibleImportPreviewFields = datasetItemFields.filter(
  (field) => !hiddenImportPreviewFields.has(field),
);

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
  const { t } = useTranslation();
  const [step, setStep] = useState<ImportStep>("selectFile");
  const [file, setFile] = useState<File | null>(null);
  const [previewRows, setPreviewRows] = useState<DatasetImportRow[]>([]);
  const [onlyErrors, setOnlyErrors] = useState(false);
  const [parsing, setParsing] = useState(false);
  const [importing, setImporting] = useState(false);
  const [result, setResult] = useState<DatasetImportResultState | null>(null);
  const importMessages = useMemo<DatasetImportMessages>(
    () => ({
      numbersUnsupported: t("datasetManagement.import.numbersUnsupported"),
      fileUnsupported: t("datasetManagement.import.fileUnsupported"),
      jsonFormatInvalid: t("datasetManagement.import.jsonFormatInvalid"),
      deletedFieldInvalid: t("datasetManagement.import.deletedFieldInvalid"),
      required: {
        question: t("datasetManagement.validation.questionRequired"),
        question_type: t("datasetManagement.validation.questionTypeRequired"),
        ground_truth: t("datasetManagement.validation.groundTruthRequired"),
      },
    }),
    [t],
  );
  const importSteps: Array<{ key: ImportStep; title: string }> = [
    { key: "selectFile", title: t("datasetManagement.import.selectFileStep") },
    { key: "preview", title: t("datasetManagement.import.previewStep") },
    { key: "result", title: t("datasetManagement.import.resultStep") },
  ];

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
      message.error(t("datasetManagement.import.noFile"));
      return;
    }
    setParsing(true);
    try {
      const rows = await parseDatasetFile(file, importMessages);
      if (rows.length === 0) {
        message.error(t("datasetManagement.import.noRows"));
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
        message.error(t("datasetManagement.import.missingRequiredFields", {
          fields: missing.map((field) => t(datasetItemFieldI18nKeys[field])).join(", "),
        }));
        return;
      }
      setPreviewRows(buildImportPreview(rows, mapping, importMessages));
      setOnlyErrors(false);
      setStep("preview");
    } catch (error: any) {
      message.error(error?.message || t("datasetManagement.import.parseFailed"));
    } finally {
      setParsing(false);
    }
  };

  const handleConfirmImport = async () => {
    const successRows = previewRows.filter((row) => row.errors.length === 0);
    if (successRows.length === 0) {
      message.error(t("datasetManagement.import.noValidRows"));
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
      message.error(error?.message || t("datasetManagement.import.importFailed"));
    } finally {
      setImporting(false);
    }
  };

  const previewColumns: ColumnsType<DatasetImportRow> = [
    { title: t("datasetManagement.import.rowNumber"), dataIndex: "rowIndex", width: 80 },
    ...visibleImportPreviewFields.map((field) => ({
      title: t(datasetItemFieldI18nKeys[field]),
      render: (_: unknown, row: DatasetImportRow) => {
        const value = row.normalized[field];
        if (Array.isArray(value)) {
          return value.length > 0 ? value.join(", ") : "-";
        }
        if (typeof value === "boolean") {
          return value ? t("common.enabled") : t("common.disabled");
        }
        return `${value || ""}`.trim() || "-";
      },
      ellipsis: true,
      width: field === "question_type" || field === "is_deleted" ? 140 : 200,
    })),
    {
      title: t("datasetManagement.import.validationResult"),
      render: (_, row) =>
        row.errors.length > 0 ? (
          <span className="dataset-import-error-text">{row.errors.join("；")}</span>
        ) : (
          <span className="dataset-import-success-text">{t("datasetManagement.import.passed")}</span>
        ),
      width: 220,
    },
  ];

  const footer = (() => {
    if (step === "selectFile") {
      return [
        <Button key="cancel" onClick={onCancel}>
          {t("datasetManagement.import.cancel")}
        </Button>,
        <Button key="next" type="primary" loading={parsing} onClick={parseCurrentFile}>
          {t("datasetManagement.import.next")}
        </Button>,
      ];
    }
    if (step === "preview") {
      return [
        <Button key="prev" onClick={() => setStep("selectFile")}>
          {t("datasetManagement.import.previous")}
        </Button>,
        <Button key="confirm" type="primary" loading={importing} onClick={handleConfirmImport}>
          {t("datasetManagement.import.confirmImport")}
        </Button>,
      ];
    }
    return [
      <Button key="done" type="primary" onClick={onCancel}>
        {t("datasetManagement.import.done")}
      </Button>,
    ];
  })();

  return (
    <Modal
      destroyOnClose
      open={open}
      title={t("datasetManagement.import.title")}
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
                message.error(t("datasetManagement.import.numbersUnsupported"));
                return Upload.LIST_IGNORE;
              }
              if (kind === "unknown") {
                message.error(t("datasetManagement.import.fileUnsupported"));
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
            <p className="ant-upload-text">{t("datasetManagement.import.uploadText")}</p>
            <p className="ant-upload-hint">
              {t("datasetManagement.import.uploadHint")}
            </p>
          </Upload.Dragger>
        </div>
      ) : null}

      {step === "preview" ? (
        <div className="dataset-import-step">
          <div className="dataset-import-preview-toolbar">
            <span>
              {t("datasetManagement.import.previewSummary", {
                total: previewRows.length,
                errors: previewRows.filter((row) => row.errors.length > 0).length,
              })}
            </span>
            <Button onClick={() => setOnlyErrors((value) => !value)}>
              {onlyErrors
                ? t("datasetManagement.import.showAll")
                : t("datasetManagement.import.onlyErrors")}
            </Button>
          </div>
          <Table
            size="small"
            rowKey="rowIndex"
            columns={previewColumns}
            dataSource={visiblePreviewRows}
            pagination={{ pageSize: 6 }}
            scroll={{ x: 2600 }}
          />
        </div>
      ) : null}

      {step === "result" ? (
        <div className="dataset-import-result">
          <h3>{t("datasetManagement.import.resultTitle")}</h3>
          <p>{t("datasetManagement.import.successCount", { count: result?.successCount || 0 })}</p>
          <p>{t("datasetManagement.import.failedCount", { count: result?.failedCount || 0 })}</p>
          {result?.failedRows.length ? (
            <Alert
              type="warning"
              showIcon
              message={t("datasetManagement.import.failedRowsTitle")}
              description={t("datasetManagement.import.failedRowsDescription")}
            />
          ) : null}
        </div>
      ) : null}
    </Modal>
  );
}
