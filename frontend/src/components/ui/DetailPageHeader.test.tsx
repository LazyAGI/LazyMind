import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import DetailPageHeader from "./DetailPageHeader";

describe("DetailPageHeader", () => {
  it("renders the title and a back button by default", () => {
    renderWithProviders(<DetailPageHeader title="Detail Page" />);
    expect(screen.getByText("Detail Page")).toBeTruthy();
    expect(document.querySelector(".anticon-left")).not.toBeNull();
  });

  it("calls onBack instead of history.back when provided", () => {
    const onBack = vi.fn();
    const historyBackSpy = vi.spyOn(window.history, "back").mockImplementation(() => {});
    renderWithProviders(<DetailPageHeader title="Detail Page" onBack={onBack} />);

    fireEvent.click(document.querySelector(".anticon-left")!.closest("button")!);
    expect(onBack).toHaveBeenCalledTimes(1);
    expect(historyBackSpy).not.toHaveBeenCalled();
    historyBackSpy.mockRestore();
  });

  it("hides the back button when showBackButton is false", () => {
    renderWithProviders(<DetailPageHeader title="Detail Page" showBackButton={false} />);
    expect(document.querySelector(".anticon-left")).toBeNull();
  });

  it("renders breadcrumbs, description, and extraContent when provided", () => {
    renderWithProviders(
      <DetailPageHeader
        title="Detail Page"
        breadcrumbs={[{ title: "Home" }, { title: "Detail" }]}
        description="Some description"
        extraContent={[
          { label: "Owner", value: "Alice" },
          { label: "Hidden", value: "Bob", hidden: true },
        ]}
      />,
    );
    expect(screen.getByText("Home")).toBeTruthy();
    expect(screen.getByText("Some description")).toBeTruthy();
    expect(screen.getByText("Owner")).toBeTruthy();
    expect(screen.getByText("Alice")).toBeTruthy();
    expect(screen.queryByText("Bob")).toBeNull();
  });

  it("renders settingsMenu and titleExtra together", () => {
    renderWithProviders(
      <DetailPageHeader
        title="Detail Page"
        settingsMenu={<button type="button">Settings</button>}
        titleExtra="Extra info"
      />,
    );
    expect(screen.getByText("Settings")).toBeTruthy();
    expect(screen.getByText("Extra info")).toBeTruthy();
  });
});
