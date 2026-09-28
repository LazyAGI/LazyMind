import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { isDesktopRuntime } from "@/runtime/mode";
import SkillManagementToolbar from "./SkillManagementToolbar";

vi.mock("@/runtime/mode", () => ({ isDesktopRuntime: vi.fn() }));

describe("SkillManagementToolbar", () => {
  beforeEach(() => {
    vi.mocked(isDesktopRuntime).mockReturnValue(false);
  });

  const baseProps = {
    t: (key: string) => key,
    skillView: 'installed' as const, installedCount: 6,
    onCreateSkill: vi.fn(), organizeMode: false, organizeDisabled: false,
    organizeStatus: 'idle' as const, onOrganizeSkills: vi.fn(), manualSkillReviewCount: 2,
    manualSkillReviewDisabled: false, onSkillReviewClick: vi.fn(), messageCenterCount: 1,
    onMessageCenterClick: vi.fn(), showMessageCenter: true, isAdmin: false,
  };

  it.each([false, true].flatMap((desktop) =>
    (['installed', 'workflows'] as const).map((skillView) => ({ desktop, skillView })),
  ))('shows $skillView without a location switch (desktop=$desktop)', ({ desktop, skillView }) => {
    vi.mocked(isDesktopRuntime).mockReturnValue(desktop);
    render(<SkillManagementToolbar {...baseProps} skillView={skillView} />);
    expect(screen.getByRole('heading')).toHaveTextContent(skillView === 'installed' ? 'admin.memorySkillViewInstalled' : 'admin.memorySkillViewWorkflows');
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
  });

  it('preserves organize, sediment, messages, and adds a draft review action', async () => {
    const onReviewDrafts = vi.fn();
    render(<SkillManagementToolbar {...baseProps} pendingDraftCount={4} onReviewDrafts={onReviewDrafts} />);
    fireEvent.click(screen.getByRole('button', { name: 'admin.memorySkillOrganizeTitle' }));
    fireEvent.click(await screen.findByText('admin.memorySkillOrganizeLight'));
    fireEvent.click(screen.getByRole('button', { name: /admin.memorySkillReviewCardTitle/ }));
    fireEvent.click(screen.getByRole('button', { name: /admin.memorySkillMessageCenterTitle/ }));
    fireEvent.click(screen.getByRole('button', { name: /admin.memorySkillPendingDrafts/ }));
    expect(baseProps.onOrganizeSkills).toHaveBeenCalledWith('light');
    expect(baseProps.onSkillReviewClick).toHaveBeenCalled();
    expect(baseProps.onMessageCenterClick).toHaveBeenCalled();
    expect(onReviewDrafts).toHaveBeenCalled();
  });

  it('keeps the workflow creation action', () => {
    const onNewWorkflow = vi.fn();
    render(<SkillManagementToolbar {...baseProps} skillView="workflows" onNewWorkflow={onNewWorkflow} />);
    fireEvent.click(screen.getByRole('button', { name: /admin.memoryWorkflowNewButton/ }));
    expect(onNewWorkflow).toHaveBeenCalledOnce();
  });

  it('retains both supported import paths', async () => {
    render(<SkillManagementToolbar {...baseProps} />);
    fireEvent.click(screen.getByRole('button', { name: /admin.memorySkillCreateButton/ }));
    fireEvent.click(await screen.findByText('admin.memorySkillCreateUploadTitle'));
    expect(baseProps.onCreateSkill).toHaveBeenCalledWith('zip');
    fireEvent.click(screen.getByRole('button', { name: /admin.memorySkillCreateButton/ }));
    fireEvent.click(await screen.findByText('admin.memorySkillCreateImportTitle'));
    expect(baseProps.onCreateSkill).toHaveBeenCalledWith('url');
  });

  it('keeps unavailable organize and sediment actions disabled', () => {
    render(<SkillManagementToolbar {...baseProps} organizeDisabled manualSkillReviewDisabled />);
    expect(screen.getByRole('button', { name: 'admin.memorySkillOrganizeTitle' })).toBeDisabled();
    expect(screen.getByRole('button', { name: /admin.memorySkillReviewCardTitle/ })).toBeDisabled();
  });

  it('starts consolidation from the organize menu', async () => {
    render(<SkillManagementToolbar {...baseProps} />);
    fireEvent.click(screen.getByRole('button', { name: 'admin.memorySkillOrganizeTitle' }));
    fireEvent.click(await screen.findByText('admin.memorySkillOrganizeDeep'));
    expect(baseProps.onOrganizeSkills).toHaveBeenCalledWith('deep');
  });

  it('cancels organize selection from the hover dismiss control', () => {
    const onOrganizeSkills = vi.fn();
    const onOrganizeCancel = vi.fn();
    render(
      <SkillManagementToolbar
        {...baseProps}
        organizeMode
        onOrganizeSkills={onOrganizeSkills}
        onOrganizeCancel={onOrganizeCancel}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'admin.memorySkillOrganizeCancel' }));
    expect(onOrganizeCancel).toHaveBeenCalledTimes(1);
    expect(onOrganizeSkills).not.toHaveBeenCalled();
  });

  it('keeps organize progress on the existing card and puts elapsed in the tooltip', () => {
    render(
      <SkillManagementToolbar
        {...baseProps}
        organizeStatus="running"
        organizeRunStatus="organize_plan"
        organizeElapsedMs={125000}
        organizeDisabled
      />,
    );
    const button = screen.getByRole('button', { name: 'admin.memorySkillOrganizeStagePlan' });
    expect(button).toHaveClass('is-running');
    expect(button).toHaveAttribute(
      "title",
      "admin.memorySkillOrganizeStagePlan · admin.memorySkillOrganizeElapsedMinutes",
    );
    expect(screen.queryByText(/admin.memorySkillOrganizeElapsedMinutes/)).not.toBeInTheDocument();
  });

  it.each([
    { mode: "cloud", desktop: false, visible: true },
    { mode: "desktop", desktop: true, visible: false },
  ])("controls the admin publish action in $mode mode", ({ desktop, visible }) => {
    vi.mocked(isDesktopRuntime).mockReturnValue(desktop);

    render(
      <SkillManagementToolbar
        t={(key) => key === "admin.memorySkillAdminPublishButton" ? "管理员上架技能" : key}
        skillView="market"
        installedCount={0}
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
        isAdmin
        onAdminPublish={vi.fn()}
        onNewWorkflow={vi.fn()}
      />,
    );

    expect(Boolean(screen.queryByRole("button", { name: "管理员上架技能" }))).toBe(visible);
  });
});
