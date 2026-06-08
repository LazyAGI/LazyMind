import { JobJobStateEnum } from "@/api/generated/knowledge-client";

// Knowledge base user type.
export enum MemberType {
  USER = 1,
  GROUP = 2,
}

// Knowledge base user type.
export enum RoleType {
  MAINTAINER = "dataset_maintainer",
  USER = "dataset_user",
  UPLOADER = "dataset_uploader",
}

// Knowledge base share type.
export enum ShareType {
  NOT_SHARED = 0,
  // All tenant users - Maintainer permissions.
  TENANT_ADMIN = 1,
  // All tenant users - User permissions.
  TENANT_USER = 2,
}

// Knowledge document type.
export enum DocumentType {
  UNSPECIFIED = 0,
  // Website.
  URL = 1,
  FOLDER = 2,
  TXT = 3,
  PDF = 4,
  HTML = 5,
  XLSX = 6,
  XLS = 7,
  DOCX = 8,
  CSV = 9,
  PPTX = 10,
  PPT = 11,
  XML = 12,
  MARKDOWN = 13,
  MD = 14,
}

// The default source of the task is the knowledge base.
export enum TaskOrigin {
  KNOWLEDGEBASE = 1,
  DATASET = 2,
}

// File status negative numbers are front-end custom status.
export enum DatasetFileState {
  // Local queue.
  UPLOAD_PENDING = -1,
  // Local uploading.
  UPLOADING = -2,
  SUCCESS = 1,
  FAIL = 2,
  CANCEL = -3,
}

// Task status.
export enum DatasetTaskState {
  // Local file uploading.
  UPLOADING = 0,
  JOB_STARTED = 1,
  SUCCESS = 2,
  FAIL = 3,
  CANCEL = 4,
}

// File status Negative numbers are front-end custom status.
export enum FileState {
  UNSPECIFIED = "DOCUMENT_STAGE_UNSPECIFIED",
  UPLOAD_PENDING = -1,
  UPLOADING = -2,
  PARSE_PENDING = "DOCUMENT_QUEUED",
  PARSING = "DOCUMENT_PARSING",
  SUCCESS = "DOCUMENT_PARSE_SUCCESSFULLY",
  FAIL = "DOCUMENT_PARSING_FAILED",
  CANCEL = "DOCUMENT_PARSING_CANCELLED",
  CRAWLING = "DOCUMENT_CRAWLING",
  CRAWLING_FAILED = "DOCUMENT_CRAWLING_FAILED",
  FILE_FAILED = "DOCUMENT_FAILED",
  CRAWLING_PENDING = "DOCUMENT_CRAWLING_QUEUED",
}

// Import knowledge source type.
export enum DataSourceType {
  LOCAL = 1,
  URL = 2,
  NOTION = 3,
  ONES = 4,
  FEISHU = 5,
}

// File status tabs.
export enum FileTabs {
  RUNNING = "1",
  SUCCESS = "2",
  FAILED = "3",
}

export enum SegmentType {
  TEXT = 1,
  AOSS_IMAGE = 2,
  // Markdown table.
  TABLE = 3,
  // Download error image.
  ERROR_IMAGE = 4,
  STRUCTURED_DATA = 5,
}

// The SegmentType.Text returned by the backend contains two formats: Text and Markdown. They need to be distinguished by SegmentDisplayType.
export enum SegmentDisplayType {
  TEXT = 1,
  MARKDOWN = 2,
}

export const TIME_FORMAT = "YYYY-MM-DD HH:mm:ss";
export const DATE_FORMAT = "YYYY-MM-DD";

export const TIME_COLUMN_WIDTH = 200;
export const DATE_COLUMN_WIDTH = 120;
export const ACTION_COLUMN_BASE_WIDTH = 60;

export const TABLE_PAGE_SIZE = 10;
export const CARD_PAGE_SIZE = 12;
export const IMPORT_TASK_POLL_INTERVAL = 5 * 1000;

// export const SUPPORT_SUFFIX = ['txt', 'xml', 'json', 'pdf', 'docx', 'doc', 'html', 'md', 'pptx', 'csv', 'xlsx', 'xls'];
export const SUPPORT_SUFFIX = ["pdf", "docx", "doc", "pptx", "zip"];
// Unstructured data file suffix.
export const UNSTRUCTURED_SUFFIX = [
  "txt",
  "xml",
  "json",
  "pdf",
  "docx",
  "html",
  "md",
  "pptx",
];
// Structured data file suffix.
export const STRUCTURED_SUFFIX = ["xls", "xlsx", "csv", "jsonl", "parquet"];

export const FOLDER_NAME_REG = /^[a-zA-Z\d\u4e00-\u9fa5_]+$/;

// Task status color.
export const STATUS_COLORS = {
  offline: "rgba(143, 154, 175, 1)",
  progress: "rgba(0, 106, 230, 1)",
  success: "rgba(31, 202, 125, 1)",
  error: "rgba(205, 20, 11, 1)",
  warning: "rgba(249, 150, 2, 1)",
};

export const ROLE_TITLE_MAP: Record<string, string> = {
  [RoleType.MAINTAINER]: "管理者",
  [RoleType.USER]: "只读者",
  [RoleType.UPLOADER]: "上传者",
};

export const ROLE_TYPE_INFO = [
  { id: RoleType.MAINTAINER, title: ROLE_TITLE_MAP[RoleType.MAINTAINER] },
  { id: RoleType.USER, title: ROLE_TITLE_MAP[RoleType.USER] },
  { id: RoleType.UPLOADER, title: ROLE_TITLE_MAP[RoleType.UPLOADER] },
];

export const IMPORT_TASK_RUNNING_STATES = ["WAITING", "WORKING", "CREATING", "RUNNING"];
export const IMPORT_TASK_SUCCESS_STATES = ["SUCCESS", "SUCCEEDED"];
export const IMPORT_TASK_FAILED_STATES = ["FAILED", "CANCELED"];

export const ALL_TAGS = "__ALL__";
