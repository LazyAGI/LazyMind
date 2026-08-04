import { describe, it, expect, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import MdxEditor from "./index";

describe("MdxEditor", () => {
  it("renders the provided value in the textarea", () => {
    renderWithProviders(<MdxEditor value="hello world" onChange={vi.fn()} />);

    expect(screen.getByRole("textbox")).toHaveValue("hello world");
  });

  it("calls onChange with the new text when edited", () => {
    const onChange = vi.fn();
    renderWithProviders(<MdxEditor value="" onChange={onChange} />);

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "new content" },
    });

    expect(onChange).toHaveBeenCalledWith("new content");
    expect(screen.getByRole("textbox")).toHaveValue("new content");
  });

  it("syncs local state when the value prop changes", () => {
    const { rerender } = renderWithProviders(
      <MdxEditor value="first" onChange={vi.fn()} />,
    );
    expect(screen.getByRole("textbox")).toHaveValue("first");

    rerender(<MdxEditor value="second" onChange={vi.fn()} />);

    expect(screen.getByRole("textbox")).toHaveValue("second");
  });

  it("falls back to an empty string when value is undefined", () => {
    renderWithProviders(
      <MdxEditor value={undefined as unknown as string} onChange={vi.fn()} />,
    );

    expect(screen.getByRole("textbox")).toHaveValue("");
  });
});
