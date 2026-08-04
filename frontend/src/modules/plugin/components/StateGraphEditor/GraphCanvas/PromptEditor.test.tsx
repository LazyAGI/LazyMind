import { fireEvent, render, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import PromptEditor from "./PromptEditor";
import type { SlotDef } from "../core/model";

const slots: SlotDef[] = [
  { id: "outline", type: "text", label: "Outline" },
  { id: "body", type: "text" },
];

describe("PromptEditor", () => {
  it("renders plain text content when the value has no slot references", () => {
    const { container } = renderWithProviders(
      <PromptEditor value="hello world" onChange={vi.fn()} slots={slots} />,
    );
    const editor = container.querySelector(".pe-editor")!;
    expect(editor.textContent).toBe("hello world");
    expect(editor.querySelector(".pe-chip")).toBeNull();
  });

  it("renders a chip for each {{slot_id}} reference using the slot label", () => {
    const { container } = renderWithProviders(
      <PromptEditor value="Use {{outline}} then {{body}}" onChange={vi.fn()} slots={slots} />,
    );
    const editor = container.querySelector(".pe-editor")!;
    const chips = editor.querySelectorAll(".pe-chip");
    expect(chips).toHaveLength(2);
    expect(chips[0].querySelector(".pe-chip-text")?.textContent).toBe("Outline");
    // No label configured for "body" -> falls back to the id.
    expect(chips[1].querySelector(".pe-chip-text")?.textContent).toBe("body");
  });

  it("emits the serialized value on input", () => {
    const onChange = vi.fn();
    const { container } = render(
      <PromptEditor value="hello" onChange={onChange} slots={slots} />,
    );
    const editor = container.querySelector(".pe-editor")! as HTMLElement;
    editor.textContent = "hello world";
    fireEvent.input(editor);
    expect(onChange).toHaveBeenCalledWith("hello world");
  });

  it("removes the chip and emits the updated value when its delete button is clicked", () => {
    const onChange = vi.fn();
    const { container } = render(
      <PromptEditor value="{{outline}} text" onChange={onChange} slots={slots} />,
    );
    const editor = container.querySelector(".pe-editor")!;
    const deleteBtn = editor.querySelector(".pe-chip-del")! as HTMLButtonElement;
    fireEvent.mouseDown(deleteBtn);
    expect(onChange).toHaveBeenCalledWith(" text");
  });

  it("rebuilds the DOM when an external value change is not an echo of the last emission", async () => {
    const onChange = vi.fn();
    const { container, rerender } = render(
      <PromptEditor value="hello" onChange={onChange} slots={slots} />,
    );
    rerender(<PromptEditor value="{{body}}" onChange={onChange} slots={slots} />);
    await waitFor(() => {
      expect(container.querySelector(".pe-chip")).not.toBeNull();
    });
  });
});
