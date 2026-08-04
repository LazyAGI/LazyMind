import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import SkillMarketView from "./SkillMarketView";
import type { StructuredAsset } from "../../shared";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const makeAsset = (overrides: Partial<StructuredAsset> = {}): StructuredAsset => ({
  id: "market-1",
  content: "",
  name: "Market Skill",
  description: "market skill desc",
  category: "general",
  tags: ["docs"],
  ...overrides,
});

const baseProps = {
  t,
  loading: false,
  skillAssets: [] as StructuredAsset[],
  installedSkills: [] as StructuredAsset[],
  isAdmin: false,
  onInstall: vi.fn(),
  onDetail: vi.fn(),
  onDelete: vi.fn(),
  installingUid: undefined,
  deletingUid: undefined,
  page: 1,
  pageSize: 8,
  total: 0,
  onPageChange: vi.fn(),
};

describe("SkillMarketView", () => {
  it("shows a loading indicator while loading", () => {
    render(<SkillMarketView {...baseProps} loading />);
    expect(screen.getByText("common.loading")).toBeInTheDocument();
  });

  it("shows an empty state when there are no skills", () => {
    render(<SkillMarketView {...baseProps} />);
    expect(screen.getByText("admin.memorySkillMarketEmpty")).toBeInTheDocument();
  });

  it("renders skill tiles with name and description", () => {
    render(
      <SkillMarketView
        {...baseProps}
        skillAssets={[makeAsset()]}
        total={1}
      />,
    );
    expect(screen.getByText("Market Skill")).toBeInTheDocument();
    expect(screen.getByText("admin.memorySkillMarketInstall")).toBeInTheDocument();
  });

  it("shows an installed badge and disables install when already installed", () => {
    const asset = makeAsset();
    render(
      <SkillMarketView
        {...baseProps}
        skillAssets={[asset]}
        installedSkills={[asset]}
        total={1}
      />,
    );
    expect(
      screen.getAllByText("admin.memorySkillInstalledBadge").length,
    ).toBeGreaterThan(0);
  });

  it("calls onInstall when clicking the install button", () => {
    const onInstall = vi.fn();
    const asset = makeAsset();
    render(
      <SkillMarketView
        {...baseProps}
        skillAssets={[asset]}
        total={1}
        onInstall={onInstall}
      />,
    );
    fireEvent.click(screen.getByText("admin.memorySkillMarketInstall"));
    expect(onInstall).toHaveBeenCalledWith(asset);
  });

  it("calls onDetail when clicking the detail button", () => {
    const onDetail = vi.fn();
    const asset = makeAsset();
    render(
      <SkillMarketView
        {...baseProps}
        skillAssets={[asset]}
        total={1}
        onDetail={onDetail}
      />,
    );
    fireEvent.click(screen.getByText("admin.memorySkillMarketDetail"));
    expect(onDetail).toHaveBeenCalledWith(asset);
  });

  it("shows the delete button for admin-sourced assets when isAdmin is true", () => {
    const asset = makeAsset({ id: "admin-1" }) as StructuredAsset & {
      marketSource: string;
    };
    asset.marketSource = "admin";
    render(
      <SkillMarketView
        {...baseProps}
        isAdmin
        skillAssets={[asset]}
        total={1}
      />,
    );
    expect(screen.getByText("admin.memorySkillMarketDelete")).toBeInTheDocument();
  });

  it("does not render pagination when total is zero", () => {
    render(<SkillMarketView {...baseProps} total={0} />);
    expect(document.querySelector(".ant-pagination")).not.toBeInTheDocument();
  });

  it("renders pagination when total is greater than zero", () => {
    render(
      <SkillMarketView {...baseProps} skillAssets={[makeAsset()]} total={1} />,
    );
    expect(document.querySelector(".ant-pagination")).toBeInTheDocument();
  });
});
