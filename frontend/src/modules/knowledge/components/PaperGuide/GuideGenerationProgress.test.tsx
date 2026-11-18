import { act, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import GuideGenerationProgress from "./GuideGenerationProgress";
afterEach(() => vi.useRealTimers());
it("uses real chapter completion counts without inventing percentage progress", () => {
  const view = render(<GuideGenerationProgress stage="planning" zh />);
  expect(screen.queryByRole("progressbar")).toBeNull();
  expect(screen.getByText("目录就绪后会立即显示")).toBeVisible();
  view.rerender(<GuideGenerationProgress stage="writing" total={9} done={3} zh />);
  expect(screen.getByRole("progressbar")).toHaveAttribute("value", "3");
  expect(screen.getByRole("progressbar")).toHaveAttribute("max", "9");
  expect(screen.getByText("3 / 9 章可阅读")).toBeVisible();
});
it("replaces the estimate with an honest slow-run message after two minutes", () => {
  vi.useFakeTimers();
  const view = render(<GuideGenerationProgress stage="writing" total={9} done={3} zh />);
  act(() => vi.advanceTimersByTime(121000));
  expect(screen.getByText(/比预计时间稍久/)).toBeVisible();
  expect(screen.getByText("已等待 2:01")).toBeVisible();
  view.unmount();
  expect(vi.getTimerCount()).toBe(0);
});
it("keeps queue wait separate from the generation estimate", () => {
  vi.useFakeTimers();
  const view = render(<GuideGenerationProgress stage="queued" compact zh />);
  act(() => vi.advanceTimersByTime(180000));
  expect(screen.getByRole("status")).toHaveTextContent("排队中");
  expect(screen.getByText("开始后约 2 分钟")).toBeVisible();
  expect(screen.queryByText("仍在生成")).toBeNull();
  view.rerender(<GuideGenerationProgress stage="planning" compact zh />);
  expect(screen.getByText("预计约 2 分钟")).toBeVisible();
});
