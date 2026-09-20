import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import SkillCallModeControl from "./SkillCallModeControl";

describe("skill calling mode control", () => {
  it.each([["Home", "manual"], ["End", "priority"]] as const)("submits %s as %s", (key, mode) => {
    const onChange = vi.fn();
    render(<SkillCallModeControl value="on_demand" t={(key) => key} onChange={onChange} />);
    const slider = screen.getByRole("slider");
    fireEvent.keyDown(slider, { key, keyCode: key === "Home" ? 36 : 35 });
    fireEvent.keyUp(slider, { key, keyCode: key === "Home" ? 36 : 35 });
    expect(onChange).toHaveBeenCalledWith(mode);
  });
});
