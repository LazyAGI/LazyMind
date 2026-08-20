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

    expect(screen.getByText(new RegExp(`chat\\.runStatus\\.codes\\.${code}`))).toBeInTheDocument();
    expect(screen.getByText(/chat\.runStatus\.noOutput/)).toBeInTheDocument();
  });

  it("renders a safe provider reason and partial-output state", () => {
    render(<RunStatusCard terminal={{
      status: "interrupted",
      reason: "model_failure",
      code: "organization_spend_limit_exceeded",
      partial_output: true,
    }} />);

    expect(screen.getByText("chat.runStatus.interrupted")).toBeInTheDocument();
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
});
