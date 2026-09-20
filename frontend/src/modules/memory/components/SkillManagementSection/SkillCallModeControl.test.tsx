import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import SkillCallModeControl from "./SkillCallModeControl";

const labels: Record<string, string> = {
  "admin.memorySkillCallMode": "Call mode",
  "admin.memorySkillCallModePriority": "Priority",
  "admin.memorySkillCallModePriorityDesc": "Always available",
  "admin.memorySkillCallModeOnDemand": "On demand",
  "admin.memorySkillCallModeOnDemandDesc": "Found when relevant",
  "admin.memorySkillCallModeManual": "Manual only",
  "admin.memorySkillCallModeManualDesc": "Only when explicitly requested",
};

const t = (key: string) => labels[key] || key;

describe("skill calling mode control", () => {
  it("shows the current mode and emits the exact selected menu mode", async () => {
    const onChange = vi.fn();
    render(<SkillCallModeControl value="on_demand" t={t} onChange={onChange} />);

    fireEvent.click(screen.getByRole("button", { name: "Call mode: On demand" }));
    fireEvent.click((await screen.findByText("Priority")).closest("li")!);

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith("priority");
  });

  it("cannot open or emit changes while disabled", () => {
    const onChange = vi.fn();
    render(<SkillCallModeControl value="manual" disabled t={t} onChange={onChange} />);

    const trigger = screen.getByRole("button", { name: "Call mode: Manual only" });
    expect(trigger).toBeDisabled();
    fireEvent.click(trigger);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });
});
