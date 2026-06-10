import { Button, Space, message } from "antd";
import { DownloadOutlined } from "@ant-design/icons";
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

function downloadCsvTemplate() {
  const rows = createTemplateRows();
  const sheet = XLSX.utils.json_to_sheet(rows);
  const csv = XLSX.utils.sheet_to_csv(sheet);
  downloadBlob(new Blob([csv], { type: "text/csv;charset=utf-8" }), "dataset-template.csv");
}

function downloadJsonTemplate() {
  const json = JSON.stringify(createTemplateRows(), null, 2);
  downloadBlob(new Blob([json], { type: "application/json" }), "dataset-template.json");
}

function downloadXlsxTemplate() {
  const worksheet = XLSX.utils.json_to_sheet(createTemplateRows());
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
  const handleDownload = (type: "xlsx" | "csv" | "json") => {
    try {
      if (type === "xlsx") {
        downloadXlsxTemplate();
      } else if (type === "csv") {
        downloadCsvTemplate();
      } else {
        downloadJsonTemplate();
      }
      message.success("模版已下载");
    } catch (error) {
      console.error("Failed to download dataset template:", error);
      message.error("模版下载失败");
    }
  };

  return (
    <Space wrap>
      <Button icon={<DownloadOutlined />} onClick={() => handleDownload("xlsx")}>
        下载 Excel 模版
      </Button>
      <Button icon={<DownloadOutlined />} onClick={() => handleDownload("csv")}>
        下载 CSV 模版
      </Button>
      <Button icon={<DownloadOutlined />} onClick={() => handleDownload("json")}>
        下载 JSON 模版
      </Button>
    </Space>
  );
}
