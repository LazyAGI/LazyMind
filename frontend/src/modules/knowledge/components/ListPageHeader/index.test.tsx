import { describe, expect, it, vi } from "vitest";
import { Form } from "antd";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import ListPageHeader from "./index";

function renderHeader(props: Partial<Parameters<typeof ListPageHeader>[0]> = {}) {
  const onSearch = vi.fn();
  const defaultProps = {
    searchKey: "keyword",
    onSearch,
    ...props,
  } as Parameters<typeof ListPageHeader>[0];

  const utils = renderWithProviders(
    <Form>
      <ListPageHeader {...defaultProps} />
    </Form>,
  );
  return { ...utils, onSearch };
}

describe("ListPageHeader", () => {
  it("renders the search input with the default placeholder translation key", () => {
    renderHeader();
    expect(
      screen.getByPlaceholderText("common.pleaseInput"),
    ).toBeInTheDocument();
  });

  it("renders a custom placeholder when provided", () => {
    renderHeader({ placeholder: "custom placeholder" });
    expect(screen.getByPlaceholderText("custom placeholder")).toBeInTheDocument();
  });

  it("calls onSearch with the typed keyword when the user presses enter", () => {
    const { onSearch } = renderHeader();
    const input = screen.getByPlaceholderText("common.pleaseInput");
    fireEvent.change(input, { target: { value: "hello" } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    expect(onSearch).toHaveBeenCalledWith("hello");
  });

  it("does not render the primary button when btnText/onClick are missing", () => {
    renderHeader();
    expect(screen.queryByRole("button", { name: "common.create" })).not.toBeInTheDocument();
  });

  it("renders the primary action button and calls onClick", () => {
    const onClick = vi.fn();
    renderHeader({ btnText: "Create", onClick });
    const button = screen.getByRole("button", { name: "Create" });
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("renders a disabled secondary button with a tooltip title prop", () => {
    const onSecondaryClick = vi.fn();
    renderHeader({
      secondaryBtnText: "Export",
      onSecondaryClick,
      secondaryBtnDisabled: true,
    });
    const button = screen.getByRole("button", { name: "Export" });
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(onSecondaryClick).not.toHaveBeenCalled();
  });

  it("renders sort options and triggers onSearch when changed", () => {
    renderHeader({
      sortOption: [
        { value: "asc", label: "Ascending" },
        { value: "desc", label: "Descending" },
      ],
    });
    expect(screen.getByText("common.sortBy")).toBeInTheDocument();
    expect(screen.getByText("common.sort")).toBeInTheDocument();
  });
});
