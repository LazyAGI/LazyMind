import { createRef } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { SkillOrganizeDepth } from "../../skillApi";
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
  "admin.memorySkillOrganizeRequirement": "select 2-20 eligible skills",
  "admin.memorySkillOrganizeSubmitLight": "start optimize",
  "admin.memorySkillOrganizeSubmitDeep": "start consolidate",
  "admin.memorySkillOrganizeSelectRow": "select skill",
  "admin.memorySkillOrganizeInternalOnlyRow": "not internal",
  "admin.memorySkillOrganizeConfirmSubmitLight": "confirm optimize",
  "admin.memorySkillOrganizeConfirmSubmitDeep": "confirm consolidate",
  "admin.memorySkillSourceAll": "All",
  "admin.memorySkillOriginBuiltin": "Builtin",
  "admin.memorySkillSourceInternal": "Internal",
  "admin.memorySkillSourceExternal": "External",
  "admin.memorySkillLegacyCategoryFilter": "Legacy category",
  "admin.memorySkillBatchSelected": "Selected",
  "admin.memorySkillBatchCallMode": "Set call mode",
  "admin.memorySkillClearSelection": "Clear selection",
  "admin.memorySkillBatchSelectRow": "select {{name}}",
  "admin.memorySkillCallModePriority": "Priority",
  "admin.memorySkillCallModePriorityDesc": "Always available",
  "admin.memorySkillCallModeOnDemand": "On demand",
  "admin.memorySkillCallModeOnDemandDesc": "Found when relevant",
  "admin.memorySkillCallModeManual": "Manual only",
  "admin.memorySkillCallModeManualDesc": "Only when requested",
};

const translate = (key: string, options?: Record<string, unknown>) => {
  const value = translations[key] || key;
  return Object.entries(options || {}).reduce(
    (result, [name, replacement]) => result.replace(`{{${name}}}`, String(replacement)),
    value,
  );
};

const renderView = (
  selectedOrganizeSkillIds: string[],
  onSubmit = vi.fn(),
  depth: SkillOrganizeDepth = "light",
) => render(
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
    organizeDepth={depth}
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

const renderViewWith = (overrides: Partial<React.ComponentProps<typeof SkillInstalledView>>) => render(
  <SkillInstalledView
    t={translate}
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
    organizeMode={false}
    organizeDepth="light"
    organizeLoading={false}
    selectedOrganizeSkillIds={[]}
    onOrganizeSelectionChange={vi.fn()}
    onOrganizeCancel={vi.fn()}
    onOrganizeSubmit={vi.fn()}
    columns={[]}
    page={1}
    pageSize={20}
    total={skills.length}
    onPageChange={vi.fn()}
    listContentRef={createRef<HTMLDivElement>()}
    {...overrides}
  />
);

describe("SkillInstalledView organize rules", () => {
  it("enables all editable local skill checkboxes in light mode", () => {
    renderView([]);
    expect(screen.getAllByRole("checkbox", { name: "select skill" })).toHaveLength(3);
    screen.getAllByRole("checkbox", { name: "select skill" }).forEach((checkbox) => expect(checkbox).toBeEnabled());
  });

  it("deep lists only eligible internal skills", () => {
    renderViewWith({
      organizeMode: true,
      organizeDepth: "deep",
      dataSource: [{ ...skills[0], originBuiltinSkillUid: "builtin" }, skills[1], skills[2]],
    });
    expect(screen.queryByRole("checkbox", { name: "not internal" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("checkbox", { name: "select skill" })).toHaveLength(1);
  });

  it("locks source tabs to internal while consolidating", () => {
    const onCategoryChange = vi.fn();
    renderViewWith({
      organizeMode: true,
      organizeDepth: "deep",
      category: "internal",
      onCategoryChange,
    });

    expect(screen.getByRole("tab", { name: "Internal" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "All" })).toBeDisabled();
    fireEvent.click(screen.getByRole("tab", { name: "All" }));
    expect(onCategoryChange).not.toHaveBeenCalled();
  });

  it("requires at least two selected internal skills before submit", () => {
    const { rerender } = renderView(["internal-one"]);
    expect(screen.getByRole("button", { name: /start optimize$/ })).toBeDisabled();

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
        organizeDepth="light"
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
    expect(screen.getByRole("button", { name: /start optimize$/ })).toBeEnabled();
  });
});


describe("organize action submission", () => {
  it.each([
    ["light", "start optimize", "confirm optimize"],
    ["deep", "start consolidate", "confirm consolidate"],
  ] as const)("confirms and submits %s organization", async (mode, submitName, confirmName) => {
    const onSubmit = vi.fn();
    renderView(["internal-one", "internal-two"], onSubmit, mode);
    expect(screen.getByText(mode === "light" ? "admin.memorySkillOrganizeLightHint" : "admin.memorySkillOrganizeDeepHint")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: submitName }));
    fireEvent.click(await screen.findByRole("button", { name: confirmName }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(mode));
  });
});

describe("SkillInstalledView source and normal selection", () => {
  it("changes the server-backed source category through tabs", () => {
    const onCategoryChange = vi.fn();
    renderViewWith({ category: "internal", onCategoryChange });

    expect(screen.getByRole("tab", { name: "Internal" })).toHaveAttribute("aria-selected", "true");
    fireEvent.click(screen.getByRole("tab", { name: "Builtin" }));
    expect(onCategoryChange).toHaveBeenCalledWith("__builtin");
    fireEvent.click(screen.getByRole("tab", { name: "External" }));
    expect(onCategoryChange).toHaveBeenCalledWith("external");
    fireEvent.click(screen.getByRole("tab", { name: "All" }));
    expect(onCategoryChange).toHaveBeenCalledWith(undefined);
  });

  it("keeps source tabs and passes a legacy category through unchanged", async () => {
    const onCategoryChange = vi.fn();
    renderViewWith({
      categories: ["internal", "external", "learning", "design"],
      onCategoryChange,
    });

    expect(screen.getByRole("tab", { name: "Internal" })).toBeVisible();
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "Legacy category" }));
    const learningOptions = await screen.findAllByText("learning");
    fireEvent.click(learningOptions[learningOptions.length - 1]);
    expect(onCategoryChange).toHaveBeenCalledWith("learning");
  });

  it("keeps off-page IDs selected while reporting only the changed page row", () => {
    const onSkillSelectionChange = vi.fn();
    const onClearSkillSelection = vi.fn();
    renderViewWith({
      dataSource: [skills[0]],
      selectedSkillIds: ["off-page"],
      onSkillSelectionChange,
      onClearSkillSelection,
      onBatchCallMode: vi.fn(),
    });

    fireEvent.click(screen.getByRole("checkbox", { name: "select internal-one" }));
    expect(onSkillSelectionChange).toHaveBeenCalledWith([skills[0]], true);
    expect(screen.getByRole("checkbox", { name: "select internal-one" })).not.toBeChecked();

    fireEvent.click(screen.getByRole("button", { name: "Clear selection" }));
    expect(onClearSkillSelection).toHaveBeenCalledTimes(1);
  });

  it("does not allow cloud-only rows in batch call-mode selection", () => {
    renderViewWith({
      dataSource: [{ ...skills[0], id: "cloud:one", cloudResourceId: "one", readonly: true }],
      selectedSkillIds: [],
      onSkillSelectionChange: vi.fn(),
      onClearSkillSelection: vi.fn(),
      onBatchCallMode: vi.fn(),
    });

    expect(screen.getByRole("checkbox", { name: "select internal-one" })).toBeDisabled();
  });

  it("does not allow a cloud resource in batch selection even when readonly is absent", () => {
    renderViewWith({
      dataSource: [{ ...skills[0], id: "cloud:one", cloudResourceId: "one", readonly: undefined }],
      selectedSkillIds: [],
      onSkillSelectionChange: vi.fn(),
      onClearSkillSelection: vi.fn(),
      onBatchCallMode: vi.fn(),
    });

    expect(screen.getByRole("checkbox", { name: "select internal-one" })).toBeDisabled();
  });

  it("submits the chosen batch mode for the controlled selection", async () => {
    const onBatchCallMode = vi.fn();
    renderViewWith({
      selectedSkillIds: ["internal-one", "off-page"],
      onSkillSelectionChange: vi.fn(),
      onClearSkillSelection: vi.fn(),
      onBatchCallMode,
    });

    fireEvent.click(screen.getByRole("button", { name: "Set call mode" }));
    fireEvent.click((await screen.findByText("Manual only")).closest("li")!);
    expect(onBatchCallMode).toHaveBeenCalledWith("manual");
  });
});
