import {
  CheckCircleFilled,
  ClockCircleFilled,
  DeleteOutlined,
  ExclamationCircleFilled,
  SyncOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import type { DocumentStatusRow } from "../constants/types";

export function getParseStatusMeta(
  status: DocumentStatusRow["parseStatus"],
  t: TFunction,
) {
  if (status === "parsed") {
    return {
      color: "#12b76a",
      text: t("admin.dataSourceParseParsed"),
      icon: <CheckCircleFilled />,
    };
  }
  if (status === "reindexing") {
    return {
      color: "#1677ff",
      text: t("admin.dataSourceParseReindexing"),
      icon: <SyncOutlined spin />,
    };
  }
  if (status === "pending") {
    return {
      color: "default",
      text: t("admin.dataSourceParsePending"),
      icon: <ClockCircleFilled />,
    };
  }
  if (status === "downloading") {
    return {
      color: "#1677ff",
      text: t("admin.dataSourceParseDownloading"),
      icon: <SyncOutlined spin />,
    };
  }
  if (status === "duplicate") {
    return {
      color: "#f79009",
      text: t("admin.dataSourceParseDuplicate"),
      icon: <ClockCircleFilled />,
    };
  }
  if (status === "deleted") {
    return {
      color: "#f04438",
      text: t("admin.dataSourceParseDeleted"),
      icon: <DeleteOutlined />,
    };
  }
  if (status === "download_failed") {
    return {
      color: "#f04438",
      text: t("admin.dataSourceParseDownloadFailed"),
      icon: <ExclamationCircleFilled />,
    };
  }
  if (status === "parse_failed") {
    return {
      color: "#f04438",
      text: t("admin.dataSourceParseParseFailed"),
      icon: <ExclamationCircleFilled />,
    };
  }
  if (status === "canceled") {
    return {
      color: "#f79009",
      text: t("admin.dataSourceParseCanceled"),
      icon: <ClockCircleFilled />,
    };
  }
  return {
    color: "#f04438",
    text: t("admin.dataSourceParseFailed"),
    icon: <ExclamationCircleFilled />,
  };
}
