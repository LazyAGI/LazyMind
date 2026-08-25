import { createRef } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import UserSkillWorkflowSettings from "./UserSkillWorkflowSettings";

const mocks = vi.hoisted(() => ({
  listSkillAssetsPage: vi.fn(),
  listUserWorkflowSettings: vi.fn(),
  onGroupChange: vi.fn(),
}));

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: { error: vi.fn(), success: vi.fn() },
  };
});

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      const translations: Record<string, string> = {
        "settingsPage.skills.mySkills": "我的技能",
        "settingsPage.skills.myWorkflows": "我的工作流",
        "settingsPage.skills.bulkEnableAria": `批量启用${String(values?.label ?? "")}`,
        "settingsPage.skills.allDisabled": `0 / ${String(values?.total ?? 0)} 全部停用`,
        "settingsPage.controls.taskCenter.title": "任务中心",
      };
      return translations[key] ?? key;
    },
  }),
}));

vi.mock("@/modules/memory/skillApi", () => ({
  listSkillAssetsPage: mocks.listSkillAssetsPage,
  patchSkillAsset: vi.fn(),
}));

vi.mock("@/modules/workflow/workflowDraftApi", () => ({
  listUserWorkflowSettings: mocks.listUserWorkflowSettings,
  setUserWorkflowEnabled: vi.fn(),
}));

describe("UserSkillWorkflowSettings workflow controls", () => {
  beforeEach(() => {
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
    mocks.onGroupChange.mockReset();
    mocks.listSkillAssetsPage.mockReset();
    mocks.listSkillAssetsPage.mockResolvedValue({ records: [] });
    mocks.listUserWorkflowSettings.mockReset();
    mocks.listUserWorkflowSettings.mockResolvedValue([{
      workflow_ref: "builtin:image-workflow",
      workflow_id: "image-workflow",
      name: "AI 图片生成",
      description: "生成图片",
      when_to_use: "",
      source_type: "builtin",
      revision_id: "revision-1",
      revision_no: 1,
      remote_root: "remote://workflows/image-workflow",
      enabled: true,
      call_mode: "auto",
      status: "active",
    }]);
  });

  it("keeps workflow controls available independently", async () => {
    render(<UserSkillWorkflowSettings
      skillsEnabled
      workflowsEnabled
      groupSaving={null}
      controlsDisabled={false}
      onGroupChange={mocks.onGroupChange}
      headingRef={createRef<HTMLHeadingElement>()}
    />);

    fireEvent.click(await screen.findByRole("button", { name: /我的工作流/ }));

    const groupSwitch = screen.getByRole("switch", { name: "批量启用我的工作流" });
    expect(groupSwitch).toBeEnabled();
    expect(groupSwitch).toBeChecked();
    expect(screen.getByRole("switch", { name: "settingsPage.skills.toggleAria" })).toBeEnabled();

    fireEvent.click(groupSwitch);
    expect(mocks.onGroupChange).toHaveBeenCalledWith("workflows", false, 1);
  });

  it("allows workflows to be enabled from their own master switch", async () => {
    render(<UserSkillWorkflowSettings
      skillsEnabled
      workflowsEnabled={false}
      groupSaving={null}
      controlsDisabled={false}
      onGroupChange={mocks.onGroupChange}
      headingRef={createRef<HTMLHeadingElement>()}
    />);

    fireEvent.click(await screen.findByRole("button", { name: /我的工作流/ }));
    const groupSwitch = screen.getByRole("switch", { name: "批量启用我的工作流" });

    expect(groupSwitch).toBeEnabled();
    expect(groupSwitch).not.toBeChecked();
    fireEvent.click(groupSwitch);
    expect(mocks.onGroupChange).toHaveBeenCalledWith("workflows", true, 1);
  });
});
