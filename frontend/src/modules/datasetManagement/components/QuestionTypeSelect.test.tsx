import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import QuestionTypeSelect from "./QuestionTypeSelect";

describe("QuestionTypeSelect", () => {
  it("renders with the current value in the input", () => {
    renderWithProviders(<QuestionTypeSelect value="事实问答" />);
    expect(screen.getByRole("combobox")).toHaveValue("事实问答");
  });

  it("uses the placeholder translation key when none is provided", () => {
    renderWithProviders(<QuestionTypeSelect />);
    expect(
      screen.getByText("datasetManagement.detail.placeholders.questionType"),
    ).toBeInTheDocument();
  });

  it("uses a custom placeholder when provided", () => {
    renderWithProviders(<QuestionTypeSelect placeholder="custom placeholder" />);
    expect(screen.getByText("custom placeholder")).toBeInTheDocument();
  });

  it("calls onChange with the typed value", () => {
    const onChange = vi.fn();
    renderWithProviders(<QuestionTypeSelect onChange={onChange} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "自定义类型" } });
    expect(onChange).toHaveBeenCalledWith("自定义类型");
  });

  it("calls onBlur when the input loses focus", () => {
    const onBlur = vi.fn();
    renderWithProviders(<QuestionTypeSelect onBlur={onBlur} />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.blur(input);
    expect(onBlur).toHaveBeenCalled();
  });

  it("accepts a restricted set of options via the options prop", () => {
    const onChange = vi.fn();
    renderWithProviders(
      <QuestionTypeSelect options={["自定义A", "自定义B"]} onChange={onChange} />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "自定义A" } });
    expect(onChange).toHaveBeenCalledWith("自定义A");
  });
});
