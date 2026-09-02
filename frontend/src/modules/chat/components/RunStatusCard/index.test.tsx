import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CHAT_OPEN_MODEL_SELECTOR_EVENT } from "@/modules/chat/constants/chat";

import RunStatusCard from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      values ? `${key}:${JSON.stringify(values)}` : key,
  }),
}));

describe("RunStatusCard", () => {
  it("renders cancellation as a compact status alert", () => {
    render(<RunStatusCard terminal={{
      status: "cancelled",
      reason: "user_cancelled",
      partial_output: true,
    }} />);

    const alert = screen.getByRole("alert");
    expect(alert).toHaveClass("chat-run-status-card--cancelled");
    expect(screen.getByText("chat.runStatus.cancelled")).toBeInTheDocument();
    expect(screen.getByText("chat.runStatus.partialOutput")).toBeInTheDocument();
  });

  it("does not let an invalid cancellation reason override failed status", () => {
    render(<RunStatusCard terminal={{
      status: "failed",
      reason: "user_cancelled",
      partial_output: false,
    }} />);

    const alert = screen.getByRole("alert");
    expect(alert).not.toHaveClass("chat-run-status-card--cancelled");
    expect(screen.getByText("chat.runStatus.failed")).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.providerError/)).toBeInTheDocument();
  });

  it.each([
    "usage_limit_exceeded",
    "concurrency_limited",
    "rate_limited",
  ])("renders normalized throttling code %s", (code) => {
    render(<RunStatusCard terminal={{
      status: "failed",
      reason: "model_failure",
      code,
      partial_output: false,
    }} />);

    expect(screen.getByText("chat.runStatus.failed")).toBeInTheDocument();
    expect(screen.getByText(new RegExp(`chat\\.runStatus\\.codes\\.${code}`))).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.noOutput/)).toBeInTheDocument();
  });

  it("renders runtime failures with a runtime title and red alert", () => {
    render(<RunStatusCard terminal={{
      status: "failed",
      reason: "runtime_failure",
      code: "upstream_stream_failed",
      partial_output: true,
    }} />);

    const alert = screen.getByRole("alert");
    expect(alert).not.toHaveClass("chat-run-status-card--cancelled");
    expect(screen.getByText("chat.runStatus.runtimeFailed")).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.runtimeError/)).toBeInTheDocument();
  });

  it("renders incomplete model output with the interrupted title", () => {
    render(<RunStatusCard terminal={{
      status: "interrupted",
      reason: "model_incomplete",
      code: "length",
      partial_output: true,
    }} />);

    expect(screen.getByText("chat.runStatus.interrupted")).toBeInTheDocument();
  });

  it("renders a safe provider reason and partial-output state", () => {
    render(<RunStatusCard terminal={{
      status: "interrupted",
      reason: "model_failure",
      code: "organization_spend_limit_exceeded",
      partial_output: true,
    }} />);

    expect(screen.getByText("chat.runStatus.failed")).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.codes\.organization_spend_limit_exceeded/)).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.partialOutput/)).toBeInTheDocument();
    expect(screen.queryByText(/HTTP/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Retry-After/)).not.toBeInTheDocument();
  });

  it("does not render an unknown provider code or a raw provider message", () => {
    const terminal = {
      status: "failed",
      reason: "model_failure",
      code: "secret_provider_code",
      partial_output: false,
      provider_message: "raw secret body",
    } as const;
    render(<RunStatusCard terminal={terminal} />);

    expect(screen.getByText(/chat\.runStatus\.providerError/)).toBeInTheDocument();
    expect(screen.queryByText(/secret_provider_code/)).not.toBeInTheDocument();
    expect(screen.queryByText(/raw secret body/)).not.toBeInTheDocument();
  });

  it("offers one retry without duplicating the submitted message", () => {
    const onRetry = vi.fn();
    render(<RunStatusCard terminal={{
      status: "failed",
      reason: "model_failure",
      code: "provider_overloaded",
      partial_output: false,
    }} onRetry={onRetry} />);

    fireEvent.click(screen.getByRole("button", { name: "chat.tryAgain" }));

    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("opens the model selector for the failed conversation", () => {
    const listener = vi.fn();
    window.addEventListener(CHAT_OPEN_MODEL_SELECTOR_EVENT, listener);
    render(<RunStatusCard terminal={{
      status: "failed",
      reason: "model_failure",
      code: "rate_limited",
      partial_output: false,
    }} conversationId="conversation-1" />);

    fireEvent.click(screen.getByRole("button", { name: "chat.changeModel" }));

    expect(listener).toHaveBeenCalledTimes(1);
    expect((listener.mock.calls[0][0] as CustomEvent).detail).toEqual({
      conversationId: "conversation-1",
    });
    window.removeEventListener(CHAT_OPEN_MODEL_SELECTOR_EVENT, listener);
  });

  it("links credential failures to model settings", () => {
    render(<RunStatusCard terminal={{
      status: "failed",
      reason: "model_failure",
      code: "authentication_failed",
      partial_output: false,
    }} />);

    expect(screen.getByRole("link", { name: "chat.checkModelSettings" }))
      .toHaveAttribute("href", "/settings?section=models");
  });
});
