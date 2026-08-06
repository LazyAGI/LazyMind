import { describe, expect, it } from "vitest";
import {
  MemberType,
  RoleType,
  ShareType,
  DocumentType,
  TaskOrigin,
  DatasetFileState,
  DatasetTaskState,
  FileState,
  DataSourceType,
  FileTabs,
  SegmentType,
  SegmentDisplayType,
  TIME_FORMAT,
  DATE_FORMAT,
  TIME_COLUMN_WIDTH,
  DATE_COLUMN_WIDTH,
  ACTION_COLUMN_BASE_WIDTH,
  TABLE_PAGE_SIZE,
  CARD_PAGE_SIZE,
  IMPORT_TASK_POLL_INTERVAL,
  SUPPORT_SUFFIX,
  UNSTRUCTURED_SUFFIX,
  STRUCTURED_SUFFIX,
  FOLDER_NAME_REG,
  STATUS_COLORS,
  ROLE_TITLE_MAP,
  ROLE_TYPE_INFO,
  IMPORT_TASK_RUNNING_STATES,
  IMPORT_TASK_SUCCESS_STATES,
  IMPORT_TASK_FAILED_STATES,
  ALL_TAGS,
} from "./common";

describe("knowledge constants/common", () => {
  it("exposes stable enum values used across the module", () => {
    expect(MemberType.USER).toBe(1);
    expect(MemberType.GROUP).toBe(2);
    expect(RoleType.MAINTAINER).toBe("dataset_maintainer");
    expect(RoleType.USER).toBe("dataset_user");
    expect(RoleType.UPLOADER).toBe("dataset_uploader");
    expect(ShareType.NOT_SHARED).toBe(0);
    expect(ShareType.TENANT_ADMIN).toBe(1);
    expect(ShareType.TENANT_USER).toBe(2);
    expect(DocumentType.UNSPECIFIED).toBe(0);
    expect(DocumentType.PDF).toBe(4);
    expect(TaskOrigin.KNOWLEDGEBASE).toBe(1);
    expect(TaskOrigin.DATASET).toBe(2);
  });

  it("exposes negative front-end custom statuses for file/task state", () => {
    expect(DatasetFileState.UPLOAD_PENDING).toBe(-1);
    expect(DatasetFileState.UPLOADING).toBe(-2);
    expect(DatasetFileState.CANCEL).toBe(-3);
    expect(DatasetFileState.SUCCESS).toBe(1);
    expect(DatasetTaskState.UPLOADING).toBe(0);
    expect(DatasetTaskState.SUCCESS).toBe(2);
    expect(FileState.UPLOAD_PENDING).toBe(-1);
    expect(FileState.SUCCESS).toBe("DOCUMENT_PARSE_SUCCESSFULLY");
  });

  it("exposes data source, tab, and segment enums", () => {
    expect(DataSourceType.LOCAL).toBe(1);
    expect(DataSourceType.FEISHU).toBe(5);
    expect(FileTabs.RUNNING).toBe("1");
    expect(FileTabs.SUCCESS).toBe("2");
    expect(FileTabs.FAILED).toBe("3");
    expect(SegmentType.TEXT).toBe(1);
    expect(SegmentType.STRUCTURED_DATA).toBe(5);
    expect(SegmentDisplayType.TEXT).toBe(1);
    expect(SegmentDisplayType.MARKDOWN).toBe(2);
  });

  it("exposes format strings and layout constants", () => {
    expect(TIME_FORMAT).toBe("YYYY-MM-DD HH:mm:ss");
    expect(DATE_FORMAT).toBe("YYYY-MM-DD");
    expect(TIME_COLUMN_WIDTH).toBe(200);
    expect(DATE_COLUMN_WIDTH).toBe(120);
    expect(ACTION_COLUMN_BASE_WIDTH).toBe(60);
    expect(TABLE_PAGE_SIZE).toBe(10);
    expect(CARD_PAGE_SIZE).toBe(12);
    expect(IMPORT_TASK_POLL_INTERVAL).toBe(5000);
  });

  it("exposes file suffix lists partitioned by structure", () => {
    expect(SUPPORT_SUFFIX).toContain("pdf");
    expect(SUPPORT_SUFFIX).toContain("zip");
    expect(UNSTRUCTURED_SUFFIX).toContain("txt");
    expect(UNSTRUCTURED_SUFFIX).not.toContain("xlsx");
    expect(STRUCTURED_SUFFIX).toContain("xlsx");
    expect(STRUCTURED_SUFFIX).not.toContain("txt");
  });

  it("validates folder names using FOLDER_NAME_REG", () => {
    expect(FOLDER_NAME_REG.test("valid_folder")).toBe(true);
    expect(FOLDER_NAME_REG.test("有效文件夹")).toBe(true);
    expect(FOLDER_NAME_REG.test("invalid/folder")).toBe(false);
    expect(FOLDER_NAME_REG.test("invalid folder")).toBe(false);
    expect(FOLDER_NAME_REG.test("")).toBe(false);
  });

  it("exposes status colors and role metadata maps", () => {
    expect(STATUS_COLORS.success).toBe("rgba(31, 202, 125, 1)");
    expect(STATUS_COLORS.error).toBe("rgba(205, 20, 11, 1)");
    expect(ROLE_TITLE_MAP[RoleType.MAINTAINER]).toBe("管理者");
    expect(ROLE_TITLE_MAP[RoleType.USER]).toBe("只读者");
    expect(ROLE_TITLE_MAP[RoleType.UPLOADER]).toBe("上传者");
    expect(ROLE_TYPE_INFO).toHaveLength(3);
    expect(ROLE_TYPE_INFO[0]).toEqual({
      id: RoleType.MAINTAINER,
      title: "管理者",
    });
  });

  it("exposes import task state groupings and ALL_TAGS sentinel", () => {
    expect(IMPORT_TASK_RUNNING_STATES).toEqual(
      expect.arrayContaining(["WAITING", "WORKING", "CREATING", "RUNNING"]),
    );
    expect(IMPORT_TASK_SUCCESS_STATES).toEqual(
      expect.arrayContaining(["SUCCESS", "SUCCEEDED"]),
    );
    expect(IMPORT_TASK_FAILED_STATES).toEqual(
      expect.arrayContaining(["FAILED", "CANCELED"]),
    );
    expect(ALL_TAGS).toBe("__ALL__");
  });
});
