import { Button, Space, message } from "antd";
import { DownloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import * as XLSX from "xlsx";
import { createTemplateRows } from "../utils/datasetImport";

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}

function downloadCsvTemplate(rows: ReturnType<typeof createTemplateRows>) {
  const sheet = XLSX.utils.json_to_sheet(rows);
  const csv = XLSX.utils.sheet_to_csv(sheet);
  downloadBlob(new Blob([csv], { type: "text/csv;charset=utf-8" }), "dataset-template.csv");
}

function downloadJsonTemplate(rows: ReturnType<typeof createTemplateRows>) {
  const json = JSON.stringify(rows, null, 2);
  downloadBlob(new Blob([json], { type: "application/json" }), "dataset-template.json");
}

function downloadXlsxTemplate(rows: ReturnType<typeof createTemplateRows>) {
  const worksheet = XLSX.utils.json_to_sheet(rows);
  const workbook = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(workbook, worksheet, "dataset");
  const output = XLSX.write(workbook, { bookType: "xlsx", type: "array" });
  downloadBlob(
    new Blob([output], {
      type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    }),
    "dataset-template.xlsx",
  );
}

export default function DatasetTemplateDownload() {
  const { t } = useTranslation();
  const templateRows = () =>
    createTemplateRows({
      question: t("datasetManagement.template.sampleQuestion"),
      question_type: t("datasetManagement.template.sampleQuestionType"),
      ground_truth: t("datasetManagement.template.sampleGroundTruth"),
      key_points: t("datasetManagement.template.sampleKeyPoints"),
      reference_context: t("datasetManagement.template.sampleReferenceContext"),
      reference_doc: t("datasetManagement.template.sampleReferenceDoc"),
      generate_reason: t("datasetManagement.template.sampleGenerateReason"),
    });

  const handleDownload = (type: "xlsx" | "csv" | "json") => {
    try {
      const rows = templateRows();
      if (type === "xlsx") {
        downloadXlsxTemplate(rows);
      } else if (type === "csv") {
        downloadCsvTemplate(rows);
      } else {
        downloadJsonTemplate(rows);
      }
      message.success(t("datasetManagement.template.downloaded"));
    } catch (error) {
      console.error("Failed to download dataset template:", error);
      message.error(t("datasetManagement.template.downloadFailed"));
    }
  };

  return (
    <Space wrap>
      <Button icon={<DownloadOutlined />} onClick={() => handleDownload("xlsx")}>
        {t("datasetManagement.template.downloadExcel")}
      </Button>
      <Button icon={<DownloadOutlined />} onClick={() => handleDownload("csv")}>
        {t("datasetManagement.template.downloadCsv")}
      </Button>
      <Button icon={<DownloadOutlined />} onClick={() => handleDownload("json")}>
        {t("datasetManagement.template.downloadJson")}
      </Button>
    </Space>
  );
}
