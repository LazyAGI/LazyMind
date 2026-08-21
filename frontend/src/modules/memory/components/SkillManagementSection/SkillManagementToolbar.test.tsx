import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import SkillManagementToolbar from "./SkillManagementToolbar";

describe("SkillManagementToolbar", () => {
  it("shows the three supported views without a trash tab", () => {
    const onSkillViewChange = vi.fn();
    const labels: Record<string, string> = {
      "admin.memorySkillViewBarLabel": "技能管理页面切换",
      "admin.memorySkillViewInstalledWithCount": "我的技能 (6)",
      "admin.memorySkillViewMarket": "技能广场",
      "admin.memorySkillViewWorkflows": "我的工作流",
    };

    render(
      <SkillManagementToolbar
        t={(key) => labels[key] || key}
        skillView="market"
        onSkillViewChange={onSkillViewChange}
        installedCount={6}
        onCreateSkill={vi.fn()}
        organizeMode={false}
        organizeDisabled={false}
        organizeStatus="idle"
        onOrganizeSkills={vi.fn()}
        manualSkillReviewCount={0}
        manualSkillReviewDisabled={false}
        onSkillReviewClick={vi.fn()}
        messageCenterCount={0}
        onMessageCenterClick={vi.fn()}
        showMessageCenter={false}
        isAdmin={false}
        onNewWorkflow={vi.fn()}
      />,
    );

    expect(screen.getAllByRole("tab")).toHaveLength(3);
    expect(screen.queryByRole("tab", { name: /回收站/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "我的工作流" }));
    expect(onSkillViewChange).toHaveBeenCalledWith("workflows");
  });
});
