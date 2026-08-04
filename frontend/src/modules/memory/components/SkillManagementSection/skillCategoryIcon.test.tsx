import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import {
  getSkillCategoryIconComponent,
  renderSkillCategoryIcon,
} from "./skillCategoryIcon";
import {
  AppstoreOutlined,
  BookOutlined,
  DatabaseOutlined,
  FileTextOutlined,
  StarOutlined,
  TeamOutlined,
  ToolOutlined,
} from "@ant-design/icons";

describe("getSkillCategoryIconComponent", () => {
  it("resolves a known lowercase english category", () => {
    expect(getSkillCategoryIconComponent("research")).toBe(BookOutlined);
  });

  it("is case-insensitive for english categories", () => {
    expect(getSkillCategoryIconComponent("Research")).toBe(BookOutlined);
    expect(getSkillCategoryIconComponent("TEAM")).toBe(TeamOutlined);
  });

  it("resolves known Chinese categories by exact match", () => {
    expect(getSkillCategoryIconComponent("推荐技能")).toBe(StarOutlined);
    expect(getSkillCategoryIconComponent("文档处理")).toBe(FileTextOutlined);
    expect(getSkillCategoryIconComponent("知识库增强")).toBe(DatabaseOutlined);
    expect(getSkillCategoryIconComponent("业务流程")).toBe(ToolOutlined);
    expect(getSkillCategoryIconComponent("研发与运维")).toBe(TeamOutlined);
    expect(getSkillCategoryIconComponent("团队共享")).toBe(TeamOutlined);
  });

  it("falls back to AppstoreOutlined for unknown categories", () => {
    expect(getSkillCategoryIconComponent("unknown-category")).toBe(AppstoreOutlined);
  });

  it("falls back to AppstoreOutlined for undefined/empty category", () => {
    expect(getSkillCategoryIconComponent(undefined)).toBe(AppstoreOutlined);
    expect(getSkillCategoryIconComponent("")).toBe(AppstoreOutlined);
  });

  it("trims whitespace before matching", () => {
    expect(getSkillCategoryIconComponent("  search  ")).toBe(DatabaseOutlined);
  });
});

describe("renderSkillCategoryIcon", () => {
  it("renders the icon element matching the resolved component", () => {
    const { container } = render(<>{renderSkillCategoryIcon("personal")}</>);
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("renders the fallback icon for unknown categories", () => {
    const { container } = render(<>{renderSkillCategoryIcon("nope")}</>);
    expect(container.querySelector("svg")).not.toBeNull();
  });
});
