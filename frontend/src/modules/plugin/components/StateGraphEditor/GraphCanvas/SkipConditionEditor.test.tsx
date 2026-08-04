import { fireEvent, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import SkipConditionEditor from "./SkipConditionEditor";
import type { SlotDef } from "../core/model";

const slots: Record<string, SlotDef> = {
  outline: { id: "outline", type: "text", label: "Outline" },
  body: { id: "body", type: "text" },
};

describe("SkipConditionEditor", () => {
  it("renders one material row when value is empty", () => {
    const { container } = render(
      <SkipConditionEditor slots={slots} onChange={vi.fn()} />,
    );
    expect(container.querySelectorAll(".npp-skip-material-row")).toHaveLength(1);
  });

  it("renders a row per material for an 'any' expression", () => {
    const { container } = render(
      <SkipConditionEditor
        value={{ any: [{ material: "outline" }, { material: "body" }] }}
        slots={slots}
        onChange={vi.fn()}
      />,
    );
    expect(container.querySelectorAll(".npp-skip-material-row")).toHaveLength(2);
  });

  it("adds a new material row when the add button is clicked", () => {
    const onChange = vi.fn();
    const { container } = render(
      <SkipConditionEditor
        value={{ material: "outline" }}
        slots={slots}
        onChange={onChange}
      />,
    );

    fireEvent.click(container.querySelector('button[aria-label="添加跳过条件素材"]')!);
    expect(onChange).toHaveBeenCalledWith({ all: [{ material: "outline" }, { material: "" }] });
  });

  it("disables controls when readonly", () => {
    const { container } = render(
      <SkipConditionEditor value={{ material: "outline" }} slots={slots} readonly onChange={vi.fn()} />,
    );
    expect(container.querySelector('button[aria-label="添加跳过条件素材"]')).toBeDisabled();
  });
});
