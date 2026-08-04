import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import WidgetPlaceholder from "./WidgetPlaceholder";
import type { WidgetConfig } from "../core/pluginModel";

describe("WidgetPlaceholder", () => {
  it("renders the label when provided", () => {
    renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "text-single" }} label="Outline" />,
    );
    expect(screen.getByText("Outline")).toBeInTheDocument();
  });

  it("renders a lorem placeholder for text-single", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "text-single" }} />,
    );
    expect(container.querySelector(".wp-text-single")).toBeInTheDocument();
  });

  it("renders 3 sample items and an add button for text-list by default", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "text-list" }} />,
    );
    expect(container.querySelectorAll(".wp-text-list-item")).toHaveLength(3);
    expect(container.querySelector(".wp-add-btn")).toBeInTheDocument();
  });

  it("hides the add button for text-list when showAddButton is false", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "text-list", showAddButton: false }} />,
    );
    expect(container.querySelector(".wp-add-btn")).not.toBeInTheDocument();
  });

  it("applies grid column styling for text-list grid layout", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "text-list", itemLayout: "grid", gridMaxCols: 4 }} />,
    );
    const list = container.querySelector(".wp-text-list--grid") as HTMLElement;
    expect(list).toBeInTheDocument();
    expect(list.style.getPropertyValue("--wp-grid-cols")).toBe("repeat(4, 1fr)");
  });

  it("renders markdown sample content for text-markdown", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "text-markdown" }} />,
    );
    expect(container.querySelector(".wp-text-markdown")).toBeInTheDocument();
  });

  it("renders an image icon for image-single", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "image-single" }} />,
    );
    expect(container.querySelector(".wp-image-single .anticon-picture")).toBeInTheDocument();
  });

  it("renders 3 gallery cards plus an add card for image-gallery", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "image-gallery" }} />,
    );
    expect(container.querySelectorAll(".wp-image-gallery-card")).toHaveLength(4);
  });

  it("omits the gallery add card when showAddButton is false", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "image-gallery", showAddButton: false }} />,
    );
    expect(container.querySelectorAll(".wp-image-gallery-card")).toHaveLength(3);
  });

  it("marks the file card as readonly when configured", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "file-card", readOnly: true }} />,
    );
    expect(container.querySelector(".wp-file-card--readonly")).toBeInTheDocument();
  });

  it("renders sample JSON content for json-block and applies collapsed class", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "json-block", collapsed: true }} />,
    );
    expect(container.querySelector(".wp-json-collapsed")).toBeInTheDocument();
    expect(container.querySelector("pre")?.textContent).toContain("key");
  });

  it("returns null for an unknown widget type", () => {
    const { container } = renderWithProviders(
      <WidgetPlaceholder widgetConfig={{ widgetType: "does-not-exist" } as unknown as WidgetConfig} />,
    );
    expect(container.querySelector(".wp-root")).not.toBeInTheDocument();
  });
});
