import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import ToolLimitCard, { type ToolLimitPending } from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key) }),
}));

function pending(overrides: Partial<ToolLimitPending> = {}): ToolLimitPending {
  return {
    decision_id: "d1",
    used_rounds: 5,
    round_limit: 5,
    expanded_max_rounds: 10,
    timeout_seconds: 30,
    ...overrides,
  };
}

describe("ToolLimitCard", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders description, countdown and action buttons", () => {
    render(<ToolLimitCard pending={pending()} onDecision={vi.fn()} />);
    expect(screen.getByText("chat.toolLimitTitle")).toBeInTheDocument();
    expect(screen.getByText(/chat.toolLimitCountdown/)).toBeInTheDocument();
    expect(screen.getByText("chat.toolLimitContinue")).toBeInTheDocument();
    expect(screen.getByText("chat.toolLimitSummarize")).toBeInTheDocument();
  });

  it("counts down and auto-resolves once the timeout elapses", async () => {
    render(<ToolLimitCard pending={pending({ timeout_seconds: 2 })} onDecision={vi.fn()} />);
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.getByText("chat.toolLimitAutoContinued")).toBeInTheDocument();
  });

  it("calls onDecision with 'continue' and hides the card afterwards", async () => {
    vi.useRealTimers();
    const onDecision = vi.fn().mockResolvedValue(undefined);
    render(<ToolLimitCard pending={pending()} onDecision={onDecision} />);
    fireEvent.click(screen.getByText("chat.toolLimitContinue"));
    await waitFor(() => expect(onDecision).toHaveBeenCalledWith("continue"));
    await waitFor(() =>
      expect(screen.queryByText("chat.toolLimitTitle")).not.toBeInTheDocument(),
    );
  });

  it("shows an error message when onDecision rejects", async () => {
    vi.useRealTimers();
    const onDecision = vi.fn().mockRejectedValue(new Error("fail"));
    render(<ToolLimitCard pending={pending()} onDecision={onDecision} />);
    fireEvent.click(screen.getByText("chat.toolLimitSummarize"));
    await waitFor(() => expect(onDecision).toHaveBeenCalledWith("summarize"));
    // Card should remain, still allowing the user to retry.
    expect(screen.getByText("chat.toolLimitTitle")).toBeInTheDocument();
  });
});
