import type { ComponentType } from "react";
import {
  AppstoreOutlined,
  BookOutlined,
  DatabaseOutlined,
  FileTextOutlined,
  StarOutlined,
  TeamOutlined,
  ToolOutlined,
} from "@ant-design/icons";

const categoryIconMap: Record<string, ComponentType> = {
  research: BookOutlined,
  review: FileTextOutlined,
  search: DatabaseOutlined,
  team: TeamOutlined,
  personal: ToolOutlined,
  推荐技能: StarOutlined,
  文档处理: FileTextOutlined,
  知识库增强: DatabaseOutlined,
  业务流程: ToolOutlined,
  研发与运维: TeamOutlined,
  团队共享: TeamOutlined,
};

export const getSkillCategoryIconComponent = (category?: string) => {
  const normalized = category?.trim().toLowerCase() ?? "";
  return categoryIconMap[normalized] || categoryIconMap[category?.trim() ?? ""] || AppstoreOutlined;
};

export const renderSkillCategoryIcon = (category?: string) => {
  const Icon = getSkillCategoryIconComponent(category);
  return <Icon />;
};
