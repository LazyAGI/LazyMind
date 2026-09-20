import type { ReactNode } from "react";
import {
  ApiOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
  GithubOutlined,
  GoogleOutlined,
  WechatOutlined,
} from "@ant-design/icons";

export type CloudProviderType =
  | "local"
  | "feishu"
  | "notion"
  | "github"
  | "googledrive"
  | "wechat";

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
  {
    type: "notion",
    icon: <DatabaseOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=notion.so&sz=96",
  },
  {
    type: "github",
    icon: <GithubOutlined />,
  },
  {
    type: "googledrive",
    icon: <GoogleOutlined />,
  },
  {
    type: "wechat",
    icon: <WechatOutlined />,
  },
];

export const cloudAuthProviderOptions = cloudProviderOptions.filter(
  (item) => item.type !== "local",
);
