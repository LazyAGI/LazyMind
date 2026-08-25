import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ManagedToolSummary } from "./ToolManagementSection";

const mockSummarySize = (overflowing: boolean) => {
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 36 : 0;
  });
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
    if (!this.classList.contains("model-provider-service-summary")) return 0;
    return overflowing ? 72 : 36;
  });
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 240 : 0;
  });
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 240 : 0;
  });
};

describe("ManagedToolSummary", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not add a tooltip or focus stop when the summary is fully visible", async () => {
    mockSummarySize(false);
    const { container } = render(
      <ManagedToolSummary
        fallback="暂无数据"
        primary="简短简介"
        secondary="仅应在溢出气泡中出现的补充介绍"
      />,
    );

    const trigger = container.querySelector(".model-provider-service-summary-wrap");
    expect(trigger).not.toHaveAttribute("tabindex");
    fireEvent.mouseEnter(trigger as Element);

    await waitFor(() => {
      expect(screen.queryByText("仅应在溢出气泡中出现的补充介绍")).not.toBeInTheDocument();
    });
  });

  it("shows the full description below an actually truncated summary", async () => {
    mockSummarySize(true);
    const { container } = render(
      <ManagedToolSummary
        fallback="暂无数据"
        primary="被截断的简介"
        secondary="完整功能介绍"
      />,
    );

    const trigger = container.querySelector(".model-provider-service-summary-wrap");
    await waitFor(() => expect(trigger).toHaveAttribute("tabindex", "0"));
    fireEvent.mouseEnter(trigger as Element);

    expect(await screen.findByText(/完整功能介绍/)).toBeInTheDocument();
    expect(document.querySelector(".ant-tooltip-placement-bottomLeft")).toBeInTheDocument();
  });
});
