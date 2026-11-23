import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import MemoryPagination from "./MemoryPagination";

afterEach(cleanup);

it("hides single-page navigation and makes each page directly selectable for small lists", () => {
  const props = { page: 1, pageSize: 5, disabled: false, label: "Episodes", onChange: vi.fn() };
  const { rerender } = render(<MemoryPagination {...props} total={5} />);
  expect(screen.queryByRole("navigation")).toBeNull();
  rerender(<MemoryPagination {...props} total={15} />);
  expect(screen.queryByRole("textbox")).toBeNull();
  fireEvent.click(screen.getByText("3"));
  expect(props.onChange.mock.calls[0][0]).toBe(3);
});

it("jumps straight to a typed page for long lists", () => {
  const onChange = vi.fn();
  render(<MemoryPagination page={1} pageSize={5} total={50} label="Episodes" disabled={false} onChange={onChange} />);
  const input = screen.getByDisplayValue("1");
  fireEvent.change(input, { target: { value: "8" } });
  fireEvent.keyUp(input, { key: "Enter", keyCode: 13 });
  expect(onChange).toHaveBeenCalledTimes(1);
  expect(onChange.mock.calls[0][0]).toBe(8);
});
