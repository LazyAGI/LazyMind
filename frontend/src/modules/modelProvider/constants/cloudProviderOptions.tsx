import type { ReactNode } from "react";
import { ApiOutlined, FolderOpenOutlined } from "@ant-design/icons";

export type CloudProviderType = "local" | "feishu" | "notion" | "googledrive";

export const cloudProviderOptions: Array<{
  type: CloudProviderType;
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
];

export const cloudAuthProviderOptions = cloudProviderOptions.filter(
  (item) => item.type !== "local",
);
