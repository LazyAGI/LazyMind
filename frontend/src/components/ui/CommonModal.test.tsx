import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import CommonModal from "./CommonModal";

describe("CommonModal", () => {
  it("renders title and content, and shows default confirm/cancel buttons", () => {
    renderWithProviders(
      <CommonModal title="My Title" contentText="Some content" />,
    );
    expect(screen.getByText("My Title")).toBeTruthy();
    expect(screen.getByText("Some content")).toBeTruthy();
    expect(screen.getByText("common.confirm")).toBeTruthy();
    expect(screen.getByText("common.cancel")).toBeTruthy();
  });

  it("invokes successFn and cancelFn when their buttons are clicked", () => {
    const successFn = vi.fn();
    const cancelFn = vi.fn();
    renderWithProviders(
      <CommonModal
        title="Delete item"
        contentText="Are you sure?"
        successFn={successFn}
        cancelFn={cancelFn}
      />,
    );
    fireEvent.click(screen.getByText("common.confirm"));
    expect(successFn).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByText("common.cancel"));
    expect(cancelFn).toHaveBeenCalledTimes(1);
  });

  it("hides the footer buttons when isBtn is false", () => {
    renderWithProviders(
      <CommonModal title="Info" contentText="No actions here" isBtn={false} />,
    );
    expect(screen.queryByText("common.confirm")).toBeNull();
    expect(screen.queryByText("common.cancel")).toBeNull();
  });

  it("disables both buttons when disable is true", () => {
    renderWithProviders(
      <CommonModal title="Busy" contentText="Working" disable />,
    );
    expect(screen.getByText("common.confirm").closest("button")).toBeDisabled();
    expect(screen.getByText("common.cancel").closest("button")).toBeDisabled();
  });
});
