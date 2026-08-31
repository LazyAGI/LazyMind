import { describe, expect, it } from "vitest";

import enUS from "./en-US";
import zhCN from "./zh-CN";

describe("skill package folder deletion translations", () => {
  it("defines the folder deletion copy in Chinese and English", () => {
    expect(zhCN.admin.memorySkillPackageDelete).toBe("删除文件");
    expect(zhCN.admin.memorySkillPackageDeleteFolder).toBe("删除目录");
    expect(zhCN.admin.memorySkillPackageDeleteFolderConfirmTitle).toBe("确认删除目录？");
    expect(zhCN.admin.memorySkillPackageDeleteFolderConfirmContent).toContain("{{path}}");
    expect(zhCN.admin.memorySkillPackageDeleteFolderSuccess).toBe("目录已删除");

    expect(enUS.admin.memorySkillPackageDelete).toBe("Delete file");
    expect(enUS.admin.memorySkillPackageDeleteFolder).toBe("Delete folder");
    expect(enUS.admin.memorySkillPackageDeleteFolderConfirmTitle).toBe("Delete this folder?");
    expect(enUS.admin.memorySkillPackageDeleteFolderConfirmContent).toContain("{{path}}");
    expect(enUS.admin.memorySkillPackageDeleteFolderSuccess).toBe("Folder deleted");
  });
});

describe("skill management translation coverage", () => {
  it("defines runtime statuses in Chinese and English", () => {
    expect(zhCN.admin.memorySkillOrganizeRunning).toBe("正在整理技能");
    expect(zhCN.admin.memorySkillOrganizeCompleted).toBe("技能整理已完成");
    expect(zhCN.admin.memorySkillReviewDisabledOrganizeRunning).toBe(
      "技能整理正在运行，请稍后再试",
    );
    expect(zhCN.admin.memorySkillOrganizeDisabledReviewRunning).toBe(
      "技能复盘正在运行，请稍后再试",
    );
    expect(zhCN.admin.memoryWorkflowStatusPublished).toBe("已发布");
    expect(zhCN.admin.memoryWorkflowStatusUnpublished).toBe("未发布");

    expect(enUS.admin.memorySkillOrganizeRunning).toBe("Organizing skills");
    expect(enUS.admin.memorySkillOrganizeCompleted).toBe(
      "Skill organization completed",
    );
    expect(enUS.admin.memorySkillReviewDisabledOrganizeRunning).toBe(
      "Skill organization is in progress. Try again later.",
    );
    expect(enUS.admin.memorySkillOrganizeDisabledReviewRunning).toBe(
      "A skill review is in progress. Try again later.",
    );
    expect(enUS.admin.memoryWorkflowStatusPublished).toBe("Published");
    expect(enUS.admin.memoryWorkflowStatusUnpublished).toBe("Unpublished");
  });
});
