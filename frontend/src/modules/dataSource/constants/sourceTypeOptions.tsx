import type { ReactNode } from "react";
import {
  ApiOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
} from "@ant-design/icons";
import type { SourceType } from "./types";

export const sourceTypeOptions: Array<{
  type: SourceType;
  icon: ReactNode;
  logoUrl?: string;
  adminOnly?: boolean;
}> = [
  {
    type: "local",
    icon: <FolderOpenOutlined />,
    adminOnly: true,
  },
  {
    type: "feishu",
    icon: <ApiOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=feishu.cn&sz=96",
  },
  {
    type: "notion",
    icon: <DatabaseOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=notion.so&sz=96",
  },
];

export const providerAuthOptions = sourceTypeOptions.filter(
  (item) => item.type === "feishu" || item.type === "notion",
);
