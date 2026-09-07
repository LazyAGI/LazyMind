import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import StreamRecoveryBanner from "./StreamRecoveryBanner";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      key === "chat.streamResuming"
        ? `LazyMind ${values?.attempt}/${values?.max}`
        : key,
  }),
}));

describe("StreamRecoveryBanner", () => {
  it("renders an orange conversation status while reconnecting", () => {
    render(
      <StreamRecoveryBanner
        recovery={{
          conversationId: "conversation-1",
          status: "resuming",
          attempt: 2,
          maxAttempts: 8,
        }}
        onReconnect={vi.fn()}
      />,
    );

    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("LazyMind 2/8");
    expect(status.parentElement).toHaveClass("is-resuming");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("keeps a failed alert visible and supports a manual new cycle", () => {
    const onReconnect = vi.fn();
    render(
      <StreamRecoveryBanner
        recovery={{
          conversationId: "conversation-1",
          status: "failed",
          attempt: 8,
          maxAttempts: 8,
        }}
        onReconnect={onReconnect}
      />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "chat.streamResumeFailed",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "chat.streamReconnect" }),
    );
    expect(onReconnect).toHaveBeenCalledTimes(1);
  });

  it("announces a successful reconnection without a stale retry action", () => {
    render(
      <StreamRecoveryBanner
        recovery={{
          conversationId: "conversation-1",
          status: "recovered",
          attempt: 2,
          maxAttempts: 8,
        }}
        onReconnect={vi.fn()}
      />,
    );

    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("chat.streamRecovered");
    expect(status.parentElement).toHaveClass("is-recovered");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
