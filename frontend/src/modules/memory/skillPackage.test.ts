import { describe, expect, it } from "vitest";
import { buildSkillMarkdownContent, buildSkillZipBlob } from "./skillPackage";

describe("buildSkillMarkdownContent", () => {
  it("builds front matter with name and description followed by the body", () => {
    const content = buildSkillMarkdownContent({
      name: "示例技能",
      description: "描述文本",
      body: "正文内容",
    });

    expect(content).toBe("---\nname: 示例技能\ndescription: 描述文本\n---\n\n正文内容");
  });

  it("omits the description line when description is empty", () => {
    const content = buildSkillMarkdownContent({ name: "技能", description: "  ", body: "body" });
    expect(content).toBe("---\nname: 技能\n---\n\nbody");
  });

  it("trims all input fields", () => {
    const content = buildSkillMarkdownContent({
      name: "  技能  ",
      description: "  desc  ",
      body: "  body  ",
    });
    expect(content).toContain("name: 技能");
    expect(content).toContain("description: desc");
    expect(content.endsWith("body")).toBe(true);
  });
});

describe("buildSkillZipBlob", () => {
  it("builds a zip File using the provided name as the filename", async () => {
    const file = await buildSkillZipBlob({ name: "My Skill", description: "d", body: "b" });
    expect(file.name).toBe("My-Skill.zip");
    expect(file.type).toBe("application/zip");
  });

  it("uses the explicit filename option when provided", async () => {
    const file = await buildSkillZipBlob({
      name: "My Skill",
      description: "d",
      body: "b",
      filename: "custom-name",
    });
    expect(file.name).toBe("custom-name.zip");
  });

  it("falls back to a default name when name/filename produce an empty safe string", async () => {
    const file = await buildSkillZipBlob({ name: "***", description: "d", body: "b" });
    expect(file.name).toBe("skill.zip");
  });
});
