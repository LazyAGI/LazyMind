import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { act, fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import TypedConfirmModal, { type TypedConfirmModalRef } from "./TypedConfirmModal";

describe("TypedConfirmModal", () => {
  it("is hidden until onOpen is called via the imperative ref", () => {
    const ref = createRef<TypedConfirmModalRef>();
    renderWithProviders(<TypedConfirmModal ref={ref} onClick={vi.fn()} />);
    expect(screen.queryByText("common.confirm")).toBeNull();

    act(() => {
      ref.current?.onOpen({
        id: "item-1",
        title: "Delete item",
        content: "This cannot be undone",
        confirmText: "delete-me",
      });
    });

    expect(screen.getByText("Delete item")).toBeTruthy();
    expect(screen.getByText("This cannot be undone")).toBeTruthy();
  });

  it("shows a validation error and blocks confirm when input is empty", () => {
    const ref = createRef<TypedConfirmModalRef>();
    const onClick = vi.fn();
    renderWithProviders(<TypedConfirmModal ref={ref} onClick={onClick} />);

    act(() => {
      ref.current?.onOpen({
        id: "item-1",
        title: "Delete item",
        content: "This cannot be undone",
        confirmText: "delete-me",
      });
    });

    // The confirm button stays disabled until the typed text matches, so
    // trigger validation via the input's onBlur handler instead of a click.
    fireEvent.blur(screen.getByRole("textbox"));
    expect(screen.getByText("common.pleaseInput")).toBeTruthy();
    expect(onClick).not.toHaveBeenCalled();
  });

  it("shows a mismatch error when the typed text does not match", () => {
    const ref = createRef<TypedConfirmModalRef>();
    const onClick = vi.fn();
    renderWithProviders(<TypedConfirmModal ref={ref} onClick={onClick} />);

    act(() => {
      ref.current?.onOpen({
        id: "item-1",
        title: "Delete item",
        content: "This cannot be undone",
        confirmText: "delete-me",
      });
    });

    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "wrong-text" } });
    // The confirm button is disabled while the text doesn't match, so
    // validation is triggered via blur instead of a click.
    fireEvent.blur(input);
    expect(screen.getByText("common.inputMismatch")).toBeTruthy();
    expect(onClick).not.toHaveBeenCalled();
  });

  it("calls onClick with the item id when the typed text matches", () => {
    const ref = createRef<TypedConfirmModalRef>();
    const onClick = vi.fn();
    renderWithProviders(<TypedConfirmModal ref={ref} onClick={onClick} />);

    act(() => {
      ref.current?.onOpen({
        id: "item-1",
        title: "Delete item",
        content: "This cannot be undone",
        confirmText: "delete-me",
      });
    });

    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "delete-me" } });
    fireEvent.click(screen.getByText("common.confirm"));
    expect(onClick).toHaveBeenCalledWith("item-1");
  });
});
