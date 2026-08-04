import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import GlobalErrorBoundary from "./index";

vi.mock("./index.scss", () => ({}));
vi.mock("../../i18n", () => ({ default: { language: "zh-CN" } }));
vi.mock("../../globalState", () => ({ BASENAME: "" }));

function Boom(): never {
  throw new Error("boom");
}

describe("GlobalErrorBoundary", () => {
  const originalError = console.error;

  beforeEach(() => {
    console.error = vi.fn();
  });

  afterEach(() => {
    console.error = originalError;
    vi.restoreAllMocks();
  });

  it("renders children when there is no error", () => {
    render(
      <GlobalErrorBoundary>
        <div>All good</div>
      </GlobalErrorBoundary>,
    );
    expect(screen.getByText("All good")).toBeTruthy();
  });

  it("renders a fallback UI when a child throws during render", () => {
    render(
      <GlobalErrorBoundary>
        <Boom />
      </GlobalErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("抱歉，页面暂时无法正常显示")).toBeTruthy();
  });

  it("reloads the page when the reload button is clicked", () => {
    const reloadSpy = vi.fn();
    vi.stubGlobal("location", { ...window.location, reload: reloadSpy, assign: vi.fn() });

    render(
      <GlobalErrorBoundary>
        <Boom />
      </GlobalErrorBoundary>,
    );
    fireEvent.click(screen.getByText("刷新页面"));
    expect(reloadSpy).toHaveBeenCalledTimes(1);
  });

  it("navigates home when the home button is clicked", () => {
    const assignSpy = vi.fn();
    vi.stubGlobal("location", { ...window.location, assign: assignSpy, reload: vi.fn() });

    render(
      <GlobalErrorBoundary>
        <Boom />
      </GlobalErrorBoundary>,
    );
    fireEvent.click(screen.getByText("返回首页"));
    expect(assignSpy).toHaveBeenCalledWith("/");
  });
});
