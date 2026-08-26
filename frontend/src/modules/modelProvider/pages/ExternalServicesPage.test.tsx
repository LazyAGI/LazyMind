import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ComponentProps } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ExternalServiceCard } from "./ExternalServicesPage";

type Service = ComponentProps<typeof ExternalServiceCard>["service"];

let resizeCallback: ResizeObserverCallback | undefined;
let summaryOverflowing = false;

const service: Service = {
  category: "parsing",
  description: "",
  fields: ["baseUrl", "apiKey"],
  key: "mineru",
  logo: <span />,
  logoUrl: "",
  name: "MinerU",
  status: "configured",
  summary: "MinerU 是面向科研与企业的高精度文档解析服务",
  tone: "blue",
};

function mockSummaryMeasurements(withResizeObserver = true) {
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 36 : 0;
  });
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
    if (!this.classList.contains("model-provider-service-summary")) return 0;
    return summaryOverflowing ? 72 : 36;
  });
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 240 : 0;
  });
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 240 : 0;
  });
  if (withResizeObserver) {
    vi.stubGlobal("ResizeObserver", class {
      constructor(callback: ResizeObserverCallback) {
        resizeCallback = callback;
      }

      disconnect() {}
      observe() {}
      unobserve() {}
    });
  } else {
    vi.stubGlobal("ResizeObserver", undefined);
  }
}

function renderCard() {
  return render(
    <ExternalServiceCard
      ariaLabel="配置 MinerU"
      onOpen={vi.fn()}
      service={service}
      statusLabel="已配置"
    />,
  );
}

describe("ExternalServiceCard document parsing summary", () => {
  afterEach(() => {
    resizeCallback = undefined;
    summaryOverflowing = false;
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("does not show a tooltip when the rendered summary fits", async () => {
    mockSummaryMeasurements();
    renderCard();

    const card = screen.getByRole("button", { name: "配置 MinerU" });
    act(() => card.focus());
    fireEvent.mouseEnter(card);

    expect(card).toHaveFocus();
    expect(card.querySelector("[tabindex='0']")).toBeNull();
    await waitFor(() => expect(document.querySelector(".ant-tooltip")).toBeNull());
  });

  it("remeasures after resize and exposes an overflowing summary from the card focus", async () => {
    mockSummaryMeasurements();
    renderCard();

    summaryOverflowing = true;
    act(() => resizeCallback?.([], {} as ResizeObserver));

    const card = screen.getByRole("button", { name: "配置 MinerU" });
    fireEvent.focus(card);

    await waitFor(() => {
      expect(document.querySelector(".ant-tooltip-placement-bottomLeft")).toBeInTheDocument();
    });
    expect(screen.getAllByText(service.summary)).toHaveLength(2);
  });

  it("falls back to the window resize event when ResizeObserver is unavailable", async () => {
    mockSummaryMeasurements(false);
    renderCard();

    summaryOverflowing = true;
    fireEvent(window, new Event("resize"));

    const card = screen.getByRole("button", { name: "配置 MinerU" });
    fireEvent.focus(card);

    await waitFor(() => {
      expect(document.querySelector(".ant-tooltip-placement-bottomLeft")).toBeInTheDocument();
    });
  });
});
