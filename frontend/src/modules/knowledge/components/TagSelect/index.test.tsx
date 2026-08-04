import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { message } from "antd";
import TagSelect from "./index";

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: { warning: vi.fn(), success: vi.fn(), error: vi.fn() },
  };
});

describe("TagSelect", () => {
  it("renders existing tags as selectable options", () => {
    renderWithProviders(<TagSelect tags={["foo", "bar"]} value={["foo"]} />);
    expect(screen.getByText("foo")).toBeInTheDocument();
  });

  it("excludes the ALL_TAGS sentinel from the option list", () => {
    renderWithProviders(<TagSelect tags={["__ALL__", "real-tag"]} />);
    const input = screen.getByRole("combobox");
    fireEvent.mouseDown(input);
    expect(screen.queryByTitle("__ALL__")).not.toBeInTheDocument();
  });

  it("calls onChange with trimmed, de-duplicated tags", () => {
    const onChange = vi.fn();
    renderWithProviders(
      <TagSelect tags={[]} value={["a"]} onChange={onChange} />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "b" } });
    fireEvent.keyDown(input, {
      key: "Enter",
      code: "Enter",
      keyCode: 13,
      which: 13,
    });
    expect(onChange).toHaveBeenCalled();
    const calledWith = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    expect(calledWith).toEqual(expect.arrayContaining(["a", "b"]));
  });

  it("warns and truncates when more than 10 tags are selected", () => {
    const onChange = vi.fn();
    const elevenTags = Array.from({ length: 11 }, (_, i) => `tag-${i}`);
    renderWithProviders(
      <TagSelect tags={[]} value={elevenTags.slice(0, 10)} onChange={onChange} />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "tag-10" } });
    fireEvent.keyDown(input, {
      key: "Enter",
      code: "Enter",
      keyCode: 13,
      which: 13,
    });
    expect(message.warning).toHaveBeenCalledWith("knowledge.maxTenTags");
    const calledWith = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    expect(calledWith).toHaveLength(10);
  });

  it("notifies length error when the search input exceeds maxTagLength and showOverLengthInputError is set", () => {
    const onLengthErrorChange = vi.fn();
    renderWithProviders(
      <TagSelect
        tags={[]}
        value={[]}
        maxTagLength={3}
        showOverLengthInputError
        onLengthErrorChange={onLengthErrorChange}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "toolong" } });
    expect(onLengthErrorChange).toHaveBeenCalledWith(true);
  });

  it("blocks further typing via onInputKeyDown once over length when showOverLengthInputError is false", () => {
    renderWithProviders(<TagSelect tags={[]} value={[]} maxTagLength={2} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "ab" } });
    const event = new KeyboardEvent("keydown", { key: "a", bubbles: true, cancelable: true });
    const prevented = !input.dispatchEvent(event);
    expect(prevented).toBe(true);
  });
});
