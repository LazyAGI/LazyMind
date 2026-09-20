import { createRef } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { SkillTreeNode } from "../../shared";
import SkillInstalledView from "./SkillInstalledView";

const createSkill = (id: string, category: string): SkillTreeNode => ({
  id,
  name: id,
  description: "",
  category,
  tags: [],
  content: "",
});

const skills = [
  createSkill("internal-one", "internal"),
  createSkill("internal-two", "internal"),
  createSkill("review-skill", "review"),
];

const translations: Record<string, string> = {
  "admin.memorySkillOrganizeRequirement": "only internal; select 2-20",
  "admin.memorySkillOrganizeSubmit": "start organize",
  "admin.memorySkillOrganizeSelectRow": "select skill",
  "admin.memorySkillOrganizeInternalOnlyRow": "not internal",
};

const renderView = (selectedOrganizeSkillIds: string[], onSubmit = vi.fn()) => render(
  <SkillInstalledView
    t={(key) => translations[key] || key}
    loading={false}
    skillAssets={skills}
    dataSource={skills}
    searchInput=""
    onSearchInputChange={vi.fn()}
    onSearch={vi.fn()}
    onCategoryChange={vi.fn()}
    categories={[]}
    categoriesLoading={false}
    onReset={vi.fn()}
    organizeMode
    organizeLoading={false}
    selectedOrganizeSkillIds={selectedOrganizeSkillIds}
    onOrganizeSelectionChange={vi.fn()}
    onOrganizeCancel={vi.fn()}
    onOrganizeSubmit={onSubmit}
    columns={[]}
    page={1}
    pageSize={10}
    total={skills.length}
    onPageChange={vi.fn()}
    listContentRef={createRef<HTMLDivElement>()}
  />,
);

describe("SkillInstalledView organize rules", () => {
  it("only enables internal skill checkboxes", () => {
    renderView([]);

    expect(screen.getAllByRole("checkbox", { name: "select skill" })).toHaveLength(2);
    expect(screen.getByRole("checkbox", { name: "not internal" })).toBeDisabled();
  });

  it("requires at least two selected internal skills before submit", () => {
    const { rerender } = renderView(["internal-one"]);
    expect(screen.getByRole("button", { name: /start organize$/ })).toBeDisabled();

    rerender(
      <SkillInstalledView
        t={(key) => translations[key] || key}
        loading={false}
        skillAssets={skills}
        dataSource={skills}
        searchInput=""
        onSearchInputChange={vi.fn()}
        onSearch={vi.fn()}
        onCategoryChange={vi.fn()}
        categories={[]}
        categoriesLoading={false}
        onReset={vi.fn()}
        organizeMode
        organizeLoading={false}
        selectedOrganizeSkillIds={["internal-one", "internal-two"]}
        onOrganizeSelectionChange={vi.fn()}
        onOrganizeCancel={vi.fn()}
        onOrganizeSubmit={vi.fn()}
        columns={[]}
        page={1}
        pageSize={10}
        total={skills.length}
        onPageChange={vi.fn()}
        listContentRef={createRef<HTMLDivElement>()}
      />,
    );
    expect(screen.getByRole("button", { name: /start organize$/ })).toBeEnabled();
  });
});


describe("organize level submission", () => {
  it.each(["light", "deep"])("confirms and submits %s organization", async (mode) => {
    const onSubmit = vi.fn();
    renderView(["internal-one", "internal-two"], onSubmit);
    if (mode === "deep") {
      fireEvent.mouseDown(screen.getByRole("combobox", { name: "admin.memorySkillOrganizeDepth" }));
      fireEvent.click(await screen.findByText("admin.memorySkillOrganizeDeep"));
    }
    expect(screen.getByText(mode === "light" ? "admin.memorySkillOrganizeLightHint" : "admin.memorySkillOrganizeDeepHint")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /start organize$/ }));
    fireEvent.click(await screen.findByRole("button", { name: "admin.memorySkillOrganizeConfirmSubmit" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(mode));
  });
});
