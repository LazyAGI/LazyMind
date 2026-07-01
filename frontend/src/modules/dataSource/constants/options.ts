import type { DataSourceFileType } from "./types";

export const DEFAULT_SCAN_TENANT_ID = "tenant-demo";
export const FEISHU_APP_SETUP_STORAGE_KEY = "lazymind:datasource:feishu:app-setup";
export const NOTION_APP_SETUP_STORAGE_KEY = "lazymind:datasource:notion:app-setup";
export const FEISHU_DEFAULT_SCOPES = [
  "offline_access",
  "drive:drive",
  "drive:drive:readonly",
  "drive:drive.metadata:readonly",
  "wiki:wiki",
  "wiki:wiki:readonly",
  "wiki:node:retrieve",
  "docx:document",
];
export const FEISHU_EXCLUDE_PATTERNS = ["**/~$*"];
export const DATA_SOURCE_FILE_TYPE_OPTIONS: Array<{
  value: DataSourceFileType;
  extensions: string[];
  i18nKey: string;
}> = [
  {
    value: "pdf",
    extensions: ["pdf"],
    i18nKey: "admin.dataSourceFileTypePdf",
  },
  {
    value: "doc",
    extensions: ["doc"],
    i18nKey: "admin.dataSourceFileTypeDoc",
  },
  {
    value: "docx",
    extensions: ["docx"],
    i18nKey: "admin.dataSourceFileTypeDocx",
  },
  {
    value: "hwp",
    extensions: ["hwp"],
    i18nKey: "admin.dataSourceFileTypeHwp",
  },
  {
    value: "ppt",
    extensions: ["ppt"],
    i18nKey: "admin.dataSourceFileTypePpt",
  },
  {
    value: "pptx",
    extensions: ["pptx"],
    i18nKey: "admin.dataSourceFileTypePptx",
  },
  {
    value: "pptm",
    extensions: ["pptm"],
    i18nKey: "admin.dataSourceFileTypePptm",
  },
  {
    value: "jpg",
    extensions: ["jpg"],
    i18nKey: "admin.dataSourceFileTypeJpg",
  },
  {
    value: "jpeg",
    extensions: ["jpeg"],
    i18nKey: "admin.dataSourceFileTypeJpeg",
  },
  {
    value: "png",
    extensions: ["png"],
    i18nKey: "admin.dataSourceFileTypePng",
  },
  {
    value: "gif",
    extensions: ["gif"],
    i18nKey: "admin.dataSourceFileTypeGif",
  },
  {
    value: "bmp",
    extensions: ["bmp"],
    i18nKey: "admin.dataSourceFileTypeBmp",
  },
  {
    value: "webp",
    extensions: ["webp"],
    i18nKey: "admin.dataSourceFileTypeWebp",
  },
  {
    value: "tiff",
    extensions: ["tiff"],
    i18nKey: "admin.dataSourceFileTypeTiff",
  },
  {
    value: "tif",
    extensions: ["tif"],
    i18nKey: "admin.dataSourceFileTypeTif",
  },
  {
    value: "ipynb",
    extensions: ["ipynb"],
    i18nKey: "admin.dataSourceFileTypeIpynb",
  },
  {
    value: "epub",
    extensions: ["epub"],
    i18nKey: "admin.dataSourceFileTypeEpub",
  },
  {
    value: "md",
    extensions: ["md"],
    i18nKey: "admin.dataSourceFileTypeMd",
  },
  {
    value: "mbox",
    extensions: ["mbox"],
    i18nKey: "admin.dataSourceFileTypeMbox",
  },
  {
    value: "csv",
    extensions: ["csv"],
    i18nKey: "admin.dataSourceFileTypeCsv",
  },
  {
    value: "xls",
    extensions: ["xls"],
    i18nKey: "admin.dataSourceFileTypeXls",
  },
  {
    value: "xlsx",
    extensions: ["xlsx"],
    i18nKey: "admin.dataSourceFileTypeXlsx",
  },
  {
    value: "mp3",
    extensions: ["mp3"],
    i18nKey: "admin.dataSourceFileTypeMp3",
  },
  {
    value: "mp4",
    extensions: ["mp4"],
    i18nKey: "admin.dataSourceFileTypeMp4",
  },
  {
    value: "txt",
    extensions: ["txt"],
    i18nKey: "admin.dataSourceFileTypeTxt",
  },
  {
    value: "xml",
    extensions: ["xml"],
    i18nKey: "admin.dataSourceFileTypeXml",
  },
  {
    value: "json",
    extensions: ["json"],
    i18nKey: "admin.dataSourceFileTypeJson",
  },
  {
    value: "jsonl",
    extensions: ["jsonl"],
    i18nKey: "admin.dataSourceFileTypeJsonl",
  },
  {
    value: "yaml",
    extensions: ["yaml"],
    i18nKey: "admin.dataSourceFileTypeYaml",
  },
  {
    value: "yml",
    extensions: ["yml"],
    i18nKey: "admin.dataSourceFileTypeYml",
  },
  {
    value: "html",
    extensions: ["html"],
    i18nKey: "admin.dataSourceFileTypeHtml",
  },
  {
    value: "htm",
    extensions: ["htm"],
    i18nKey: "admin.dataSourceFileTypeHtm",
  },
  {
    value: "py",
    extensions: ["py"],
    i18nKey: "admin.dataSourceFileTypePy",
  },
];
export const DEFAULT_DATA_SOURCE_FILE_TYPES: DataSourceFileType[] = [
  "pdf",
  "doc",
  "docx",
  "xls",
  "xlsx",
  "csv",
];
export const FEISHU_MAX_OBJECT_SIZE_BYTES = 209715200;
export const CLOUD_SYNC_POLL_INTERVAL_MS = 2000;
export const CLOUD_SYNC_TIMEOUT_MS = 120000;
