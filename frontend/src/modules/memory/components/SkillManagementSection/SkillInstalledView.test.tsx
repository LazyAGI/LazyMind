import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import SkillInstalledView from "./SkillInstalledView";
import type { StructuredAsset } from "../../shared";
import type { SkillTreeNode } from "../../shared";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const makeAsset = (overrides: Partial<StructuredAsset> = {}): StructuredAsset => ({
  id: "skill-1",
  content: "",
  name: "My Skill",
  description: "desc",
  category: "general",
  tags: [],
  ...overrides,
});

const columns = [
  { title: "Name", dataIndex: "name", key: "name" },
];

const baseProps = {
  t,
  loading: false,
  skillAssets: [] as StructuredAsset[],
  dataSource: [] as SkillTreeNode[],
  searchInput: "",
  onSearchInputChange: vi.fn(),
  onSearch: vi.fn(),
  category: undefined,
  onCategoryChange: vi.fn(),
  categories: ["general", "coding"],
  categoriesLoading: false,
  source: "all" as const,
  onSourceChange: vi.fn(),
  onReset: vi.fn(),
  organizeMode: false,
  organizeLoading: false,
  selectedOrganizeSkillIds: [] as string[],
  onOrganizeSelectionChange: vi.fn(),
  onOrganizeCancel: vi.fn(),
  onOrganizeSubmit: vi.fn(),
  columns,
  page: 1,
  pageSize: 12,
  total: 0,
  onPageChange: vi.fn(),
  listContentRef: createRef<HTMLDivElement>(),
};

describe("SkillInstalledView", () => {
  it("renders the search input, category select and reset button", () => {
    render(<SkillInstalledView {...baseProps} />);
    expect(
      screen.getByPlaceholderText("admin.memorySkillSearchPlaceholder"),
    ).toBeInTheDocument();
    expect(screen.getByText("admin.memoryReset")).toBeInTheDocument();
  });

  it("triggers onSearchInputChange when typing in the search box", () => {
    const onSearchInputChange = vi.fn();
    render(
      <SkillInstalledView {...baseProps} onSearchInputChange={onSearchInputChange} />,
    );
    fireEvent.change(
      screen.getByPlaceholderText("admin.memorySkillSearchPlaceholder"),
      { target: { value: "hello" } },
    );
    expect(onSearchInputChange).toHaveBeenCalledWith("hello");
  });

  it("triggers onReset when clicking the reset button", () => {
    const onReset = vi.fn();
    render(<SkillInstalledView {...baseProps} onReset={onReset} />);
    fireEvent.click(screen.getByText("admin.memoryReset"));
    expect(onReset).toHaveBeenCalledTimes(1);
  });

  it("shows the organize bar with selection count when organizeMode is on", () => {
    render(
      <SkillInstalledView
        {...baseProps}
        organizeMode
        selectedOrganizeSkillIds={["skill-1", "skill-2"]}
      />,
    );
    expect(screen.getByRole("status")).toBeInTheDocument();
    expect(screen.getByText("admin.memorySkillOrganizeSubmit")).toBeInTheDocument();
  });

  it("does not show the organize bar when organizeMode is off", () => {
    render(<SkillInstalledView {...baseProps} />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("calls onOrganizeCancel when cancel button is clicked in organize mode", () => {
    const onOrganizeCancel = vi.fn();
    render(
      <SkillInstalledView
        {...baseProps}
        organizeMode
        onOrganizeCancel={onOrganizeCancel}
      />,
    );
    fireEvent.click(screen.getByText("common.cancel"));
    expect(onOrganizeCancel).toHaveBeenCalledTimes(1);
  });

  it("renders table rows from dataSource", () => {
    render(
      <SkillInstalledView
        {...baseProps}
        dataSource={[
          { ...makeAsset({ id: "a", name: "Alpha" }), children: [] } as unknown as SkillTreeNode,
        ]}
      />,
    );
    expect(screen.getByText("Alpha")).toBeInTheDocument();
  });
});
