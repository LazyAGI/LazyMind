import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import SkillManagementToolbar from "./SkillManagementToolbar";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const baseProps = {
  t,
  skillView: "installed" as const,
  onSkillViewChange: vi.fn(),
  installedCount: 5,
  trashCount: 2,
  onCreateSkill: vi.fn(),
  organizeMode: false,
  organizeDisabled: false,
  organizeStatus: "idle" as const,
  onOrganizeSkills: vi.fn(),
  manualSkillReviewCount: 3,
  manualSkillReviewDisabled: false,
  onSkillReviewClick: vi.fn(),
  messageCenterCount: 1,
  onMessageCenterClick: vi.fn(),
  showMessageCenter: true,
  isAdmin: false,
};

describe("SkillManagementToolbar", () => {
  it("renders the view tabs with counts", () => {
    render(<SkillManagementToolbar {...baseProps} />);
    expect(
      screen.getByText(
        'admin.memorySkillViewInstalledWithCount:{"count":5}',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText('admin.memorySkillViewTrashWithCount:{"count":2}'),
    ).toBeInTheDocument();
  });

  it("switches views when a tab is clicked", () => {
    const onSkillViewChange = vi.fn();
    render(
      <SkillManagementToolbar
        {...baseProps}
        onSkillViewChange={onSkillViewChange}
      />,
    );
    fireEvent.click(screen.getByText("admin.memorySkillViewMarket"));
    expect(onSkillViewChange).toHaveBeenCalledWith("market");
  });

  it("triggers the organize action for the installed view", () => {
    const onOrganizeSkills = vi.fn();
    render(
      <SkillManagementToolbar
        {...baseProps}
        onOrganizeSkills={onOrganizeSkills}
      />,
    );
    fireEvent.click(screen.getByTitle("admin.memorySkillOrganizeHint"));
    expect(onOrganizeSkills).toHaveBeenCalledTimes(1);
  });

  it("triggers the skill review click and shows the message center button", () => {
    const onSkillReviewClick = vi.fn();
    const onMessageCenterClick = vi.fn();
    render(
      <SkillManagementToolbar
        {...baseProps}
        onSkillReviewClick={onSkillReviewClick}
        onMessageCenterClick={onMessageCenterClick}
      />,
    );
    fireEvent.click(screen.getByText("admin.memorySkillReviewCardTitle"));
    expect(onSkillReviewClick).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByText("admin.memorySkillMessageCenterTitle"));
    expect(onMessageCenterClick).toHaveBeenCalledTimes(1);
  });

  it("shows the admin publish button and market filters for admins in market view", () => {
    const onAdminPublish = vi.fn();
    render(
      <SkillManagementToolbar
        {...baseProps}
        skillView="market"
        isAdmin
        marketFilters={<div>market-filters</div>}
        onAdminPublish={onAdminPublish}
      />,
    );
    expect(screen.getByText("market-filters")).toBeInTheDocument();
    fireEvent.click(screen.getByText("admin.memorySkillAdminPublishButton"));
    expect(onAdminPublish).toHaveBeenCalledTimes(1);
  });

  it("shows the new plugin button in the plugins view", () => {
    const onNewPlugin = vi.fn();
    render(
      <SkillManagementToolbar
        {...baseProps}
        skillView="plugins"
        onNewPlugin={onNewPlugin}
      />,
    );
    fireEvent.click(screen.getByText("admin.memoryPluginNewButton"));
    expect(onNewPlugin).toHaveBeenCalledTimes(1);
  });

  it("disables the organize and review buttons with a tooltip reason", () => {
    render(
      <SkillManagementToolbar
        {...baseProps}
        organizeDisabled
        organizeDisabledReason="organize disabled reason"
        manualSkillReviewDisabled
        manualSkillReviewDisabledReason="review disabled reason"
      />,
    );
    expect(screen.getByTitle("organize disabled reason")).toBeDisabled();
    expect(
      screen.getByText("admin.memorySkillReviewCardTitle").closest("button"),
    ).toBeDisabled();
  });
});
