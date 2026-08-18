import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import RunStatusCard from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      values ? `${key}:${JSON.stringify(values)}` : key,
  }),
}));

describe("RunStatusCard", () => {
  it("renders a safe provider reason, HTTP status, retry hint, and partial-output state", () => {
    render(<RunStatusCard terminal={{
      status: "interrupted",
      reason: "model_failure",
      code: "rate_limited",
      partial_output: true,
      provider_http_status: 429,
      retry_after_ms: 2000,
    }} />);

    expect(screen.getByText("chat.runStatus.interrupted")).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.codes\.rate_limited/)).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.partialOutput/)).toBeInTheDocument();
    expect(screen.queryByText(/provider_http_status/)).not.toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.httpStatus.*429/)).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.retryAfterSeconds.*2/)).toBeInTheDocument();
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
});
