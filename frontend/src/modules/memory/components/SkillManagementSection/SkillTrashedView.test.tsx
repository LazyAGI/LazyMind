import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import SkillTrashedView from "./SkillTrashedView";
import type { StructuredAsset } from "../../shared";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const makeAsset = (overrides: Partial<StructuredAsset> = {}): StructuredAsset =>
  ({
    id: "skill-1",
    name: "My Skill",
    description: "desc",
    category: "qa",
    tags: [],
    content: "content",
    ...overrides,
  }) as StructuredAsset;

const baseProps = {
  t,
  loading: false,
  dataSource: [] as StructuredAsset[],
  searchInput: "",
  onSearchInputChange: vi.fn(),
  onSearch: vi.fn(),
  category: undefined,
  onCategoryChange: vi.fn(),
  categories: ["qa", "docs"],
  categoriesLoading: false,
  onReset: vi.fn(),
  page: 1,
  pageSize: 6,
  total: 0,
  onPageChange: vi.fn(),
  actionLoading: new Set<string>(),
  emptyTrashLoading: false,
  onRestore: vi.fn(),
  onPurge: vi.fn(),
  onEmptyTrash: vi.fn(),
  listContentRef: createRef<HTMLDivElement>(),
};

describe("SkillTrashedView", () => {
  it("renders the empty state when there are no trashed skills", () => {
    render(<SkillTrashedView {...baseProps} />);
    expect(
      screen.getByText("admin.memorySkillTrashEmptyState"),
    ).toBeInTheDocument();
  });

  it("renders a row for each trashed skill", () => {
    render(
      <SkillTrashedView {...baseProps} dataSource={[makeAsset()]} total={1} />,
    );
    expect(screen.getByText("My Skill")).toBeInTheDocument();
    expect(screen.getByText("qa")).toBeInTheDocument();
  });

  it("triggers restore and purge actions for a row", () => {
    const onRestore = vi.fn();
    const onPurge = vi.fn();
    const { container } = render(
      <SkillTrashedView
        {...baseProps}
        dataSource={[makeAsset()]}
        total={1}
        onRestore={onRestore}
        onPurge={onPurge}
      />,
    );
    const restoreButton = container.querySelector(
      "button .anticon-redo",
    )?.closest("button");
    const purgeButton = container.querySelector(
      "button .anticon-delete",
    )?.closest("button");
    expect(restoreButton).not.toBeNull();
    expect(purgeButton).not.toBeNull();
    fireEvent.click(restoreButton as HTMLButtonElement);
    expect(onRestore).toHaveBeenCalledWith(expect.objectContaining({ id: "skill-1" }));
    fireEvent.click(purgeButton as HTMLButtonElement);
    expect(onPurge).toHaveBeenCalledWith(expect.objectContaining({ id: "skill-1" }));
  });

  it("disables the empty trash button when total is zero", () => {
    render(<SkillTrashedView {...baseProps} total={0} />);
    expect(
      screen.getByText("admin.memorySkillTrashEmpty").closest("button"),
    ).toBeDisabled();
  });

  it("updates search input and triggers reset", () => {
    const onSearchInputChange = vi.fn();
    const onReset = vi.fn();
    render(
      <SkillTrashedView
        {...baseProps}
        onSearchInputChange={onSearchInputChange}
        onReset={onReset}
      />,
    );
    fireEvent.change(
      screen.getByPlaceholderText("admin.memorySkillSearchPlaceholder"),
      { target: { value: "keyword" } },
    );
    expect(onSearchInputChange).toHaveBeenCalledWith("keyword");
    fireEvent.click(screen.getByText("admin.memoryReset"));
    expect(onReset).toHaveBeenCalledTimes(1);
  });

  it("opens a confirmation modal before emptying the trash", async () => {
    render(
      <SkillTrashedView {...baseProps} dataSource={[makeAsset()]} total={1} />,
    );
    fireEvent.click(screen.getByText("admin.memorySkillTrashEmpty"));
    const matches = await screen.findAllByText(
      "admin.memorySkillTrashEmptyConfirmTitle",
    );
    expect(matches.length).toBeGreaterThan(0);
  });
});
