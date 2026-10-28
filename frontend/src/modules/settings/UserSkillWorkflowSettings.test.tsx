import { createRef, type ReactElement } from "react";
import { act, fireEvent, render as renderComponent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createMemoryRouter, MemoryRouter, RouterProvider } from "react-router-dom";

import UserSkillWorkflowSettings from "./UserSkillWorkflowSettings";

const render = (component: ReactElement) => renderComponent(component, { wrapper: MemoryRouter });

const mocks = vi.hoisted(() => ({
  listSkillAssetsPage: vi.fn(),
  listUserWorkflowSettings: vi.fn(),
  onGroupChange: vi.fn(),
  onChanged: vi.fn(),
  patchSkillAsset: vi.fn(),
  setUserWorkflowEnabled: vi.fn(),
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
  patchSkillAsset: mocks.patchSkillAsset,
}));

vi.mock("@/modules/workflow/workflowDraftApi", () => ({
  listUserWorkflowSettings: mocks.listUserWorkflowSettings,
  setUserWorkflowEnabled: mocks.setUserWorkflowEnabled,
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
    mocks.onChanged.mockReset();
    mocks.patchSkillAsset.mockReset();
    mocks.setUserWorkflowEnabled.mockReset();
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

  it.each([
    ["skills", "", "/memory-management/skills"],
    ["workflows", "", "/memory-management/skills?skillView=workflows"],
    ["skills", "/lazymind", "/lazymind/memory-management/skills"],
    ["workflows", "/lazymind", "/lazymind/memory-management/skills?skillView=workflows"],
  ] as const)("opens %s management with PUSH navigation under basename '%s'", async (activeView, basename, destination) => {
    const initialUrl = `${basename}/settings?section=skills&skillView=${activeView}`;
    const router = createMemoryRouter([
      { path: "/settings", element: <UserSkillWorkflowSettings activeView={activeView}
        skillsEnabled workflowsEnabled groupSaving={null} controlsDisabled={false}
        onGroupChange={mocks.onGroupChange} onChanged={mocks.onChanged}
        headingRef={createRef<HTMLHeadingElement>()} /> },
      { path: "/memory-management/skills", element: <h1>Capability Management</h1> },
    ], { basename, initialEntries: [initialUrl] });
    renderComponent(<RouterProvider router={router} />);
    await screen.findByText(activeView === "skills" ? "settingsPage.skills.emptySkills" : "AI 图片生成");
    const link = screen.getByRole("link", { name: "settingsPage.skills.openCapabilityManagement" });
    expect(link).toHaveAttribute("href", destination);
    fireEvent.click(link);
    await screen.findByRole("heading", { name: "Capability Management" });
    expect(router.state.location.pathname + router.state.location.search).toBe(destination);
    expect(router.state.historyAction).toBe("PUSH");

    await act(async () => { await router.navigate(-1); });
    expect(router.state.location.pathname + router.state.location.search).toBe(initialUrl);
    expect(router.state.historyAction).toBe("POP");
    expect(screen.getByRole("link", { name: "settingsPage.skills.openCapabilityManagement" })).toHaveAttribute("href", destination);
    expect(mocks.onGroupChange).not.toHaveBeenCalled();
    expect(mocks.onChanged).not.toHaveBeenCalled();
    expect(mocks.patchSkillAsset).not.toHaveBeenCalled();
    expect(mocks.setUserWorkflowEnabled).not.toHaveBeenCalled();
    router.dispose();
  });

  it("links the header to the active resource view without nested interactive controls", async () => {
    render(<UserSkillWorkflowSettings skillsEnabled workflowsEnabled groupSaving={null}
      controlsDisabled={false} onGroupChange={mocks.onGroupChange} headingRef={createRef<HTMLHeadingElement>()} />);
    await screen.findByText("settingsPage.skills.emptySkills");

    const link = screen.getByRole("link", { name: "settingsPage.skills.openCapabilityManagement" });
    expect(link.closest("header")).not.toBeNull();
    expect(link).toHaveAttribute("href", "/memory-management/skills");
    expect(link.tabIndex).toBe(0);
    expect(link.closest("button")).toBeNull();
    expect(link.querySelector("button, a, input, [tabindex]")).toBeNull();
    expect(screen.getAllByRole("link", { name: "settingsPage.skills.openCapabilityManagement" })).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: /我的工作流/ }));
    expect(link).toHaveAttribute("href", "/memory-management/skills?skillView=workflows");
    fireEvent.click(screen.getByRole("button", { name: /我的技能/ }));
    expect(link).toHaveAttribute("href", "/memory-management/skills");
  });

  it.each([
    ["skills", "loading", "/memory-management/skills"],
    ["skills", "empty", "/memory-management/skills"],
    ["skills", "failed", "/memory-management/skills"],
    ["skills", "master off", "/memory-management/skills"],
    ["workflows", "loading", "/memory-management/skills?skillView=workflows"],
    ["workflows", "empty", "/memory-management/skills?skillView=workflows"],
    ["workflows", "failed", "/memory-management/skills?skillView=workflows"],
    ["workflows", "master off", "/memory-management/skills?skillView=workflows"],
  ] as const)("keeps the %s management link available when %s", async (activeView, state, href) => {
    if (state === "loading") mocks.listSkillAssetsPage.mockReturnValue(new Promise(() => {}));
    if (state === "empty") mocks.listUserWorkflowSettings.mockResolvedValue([]);
    if (state === "failed") mocks.listSkillAssetsPage.mockRejectedValue(new Error("unavailable"));
    render(<UserSkillWorkflowSettings activeView={activeView}
      skillsEnabled={state !== "master off"} workflowsEnabled={state !== "master off"}
      groupSaving={null} controlsDisabled={state === "loading"}
      onGroupChange={mocks.onGroupChange} headingRef={createRef<HTMLHeadingElement>()} />);

    if (state === "loading") expect(screen.getByLabelText("settingsPage.skills.loadingAria")).toBeInTheDocument();
    if (state === "empty") await screen.findByText(activeView === "skills"
      ? "settingsPage.skills.emptySkills" : "settingsPage.skills.emptyWorkflows");
    if (state === "failed") await screen.findByText("settingsPage.skills.loadFailed");
    if (state === "master off") await screen.findByText(activeView === "skills"
      ? "settingsPage.skills.masterOffSkillNotice" : "settingsPage.skills.masterOffWorkflowNotice");
    const link = screen.getByRole("link", { name: "settingsPage.skills.openCapabilityManagement" });
    expect(link).toHaveAttribute("href", href);
    expect(link).toBeVisible();
    expect(link).not.toHaveAttribute("aria-disabled", "true");
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

  it("temporarily locks controls without reporting the workflow master as off", async () => {
    render(<UserSkillWorkflowSettings activeView="workflows" skillsEnabled workflowsEnabled
      groupSaving={null} controlsDisabled onGroupChange={mocks.onGroupChange}
      headingRef={createRef<HTMLHeadingElement>()} />);
    await screen.findByText("AI 图片生成");
    expect(screen.getByRole("switch", { name: "settingsPage.skills.toggleAria" })).toBeDisabled();
    expect(screen.getByText("settingsPage.enabled")).toBeInTheDocument();
    expect(screen.queryByText("settingsPage.skills.resourceSuspended")).not.toBeInTheDocument();
  });

  it("loads skills beyond the server's 100-item page and searches the complete list", async () => {
    const records = Array.from({ length: 205 }, (_, index) => ({
      id: `skill-${index}`, name: `Skill ${index}`, description: "", isEnabled: true,
    }));
    mocks.listSkillAssetsPage.mockImplementation(async ({ page, pageSize }) => ({
      records: records.slice((page - 1) * pageSize, page * pageSize), total: records.length, page, pageSize,
    }));
    render(<UserSkillWorkflowSettings skillsEnabled workflowsEnabled groupSaving={null}
      controlsDisabled={false} onGroupChange={mocks.onGroupChange} headingRef={createRef<HTMLHeadingElement>()} />);
    await screen.findByText("Skill 0");
    expect(mocks.listSkillAssetsPage).toHaveBeenCalledTimes(3);
    expect(mocks.listSkillAssetsPage).toHaveBeenLastCalledWith({ page: 3, pageSize: 100 });
    fireEvent.change(screen.getByPlaceholderText("settingsPage.skills.searchResources"), { target: { value: "Skill 204" } });
    expect(await screen.findByText("Skill 204")).toBeVisible();
    expect(screen.queryByText("Skill 0")).not.toBeInTheDocument();
  });

  it("keeps workflows beyond 100 searchable", async () => {
    mocks.listUserWorkflowSettings.mockResolvedValue(Array.from({ length: 105 }, (_, index) => ({
      workflow_ref: `workflow-${index}`, name: `Workflow ${index}`, description: "", enabled: true,
    })));
    render(<UserSkillWorkflowSettings activeView="workflows" skillsEnabled workflowsEnabled groupSaving={null}
      controlsDisabled={false} onGroupChange={mocks.onGroupChange} headingRef={createRef<HTMLHeadingElement>()} />);
    await screen.findByText("Workflow 0");
    fireEvent.change(screen.getByPlaceholderText("settingsPage.skills.searchResources"), { target: { value: "Workflow 104" } });
    expect(await screen.findByText("Workflow 104")).toBeVisible();
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
