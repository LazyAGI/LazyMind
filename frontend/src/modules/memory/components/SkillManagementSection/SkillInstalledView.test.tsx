import { createRef, useState } from "react";
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
  "admin.memorySkillOrganizeSubmit": "start organize",
  "admin.memorySkillOrganizeSelectRow": "select skill",
  "admin.memorySkillOrganizeInternalOnlyRow": "not internal",
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

function ControlledView({ selectedOrganizeSkillIds, onSubmit }: { selectedOrganizeSkillIds: string[]; onSubmit: (mode: SkillOrganizeDepth) => void }) {
  const [depth, setDepth] = useState<SkillOrganizeDepth>("light");
  return <SkillInstalledView
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
    onOrganizeDepthChange={setDepth}
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
  />;
}
const renderView = (selectedOrganizeSkillIds: string[], onSubmit = vi.fn()) => render(<ControlledView selectedOrganizeSkillIds={selectedOrganizeSkillIds} onSubmit={onSubmit} />);

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
    onOrganizeDepthChange={vi.fn()}
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

  it("deep disables builtin provenance even when its category is internal", () => {
    renderViewWith({ organizeMode: true, organizeDepth: "deep", dataSource: [{ ...skills[0], originBuiltinSkillUid: "builtin" }, skills[1]] });
    expect(screen.getByRole("checkbox", { name: "not internal" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "select skill" })).toBeEnabled();
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
        organizeDepth="light"
        onOrganizeDepthChange={vi.fn()}
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
