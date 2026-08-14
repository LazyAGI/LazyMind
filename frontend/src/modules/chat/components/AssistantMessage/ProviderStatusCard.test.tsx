import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import ProviderStatusCard from "./ProviderStatusCard";

describe("ProviderStatusCard", () => {
  it.each(["stop", "tool_calls"])(
    "hides normal HTTP 200/%s status",
    (finishReason) => {
      const { container } = render(
        <ProviderStatusCard
          status={{
            model_call_id: "call-1",
            http_status: 200,
            finish_reason: finishReason,
          }}
        />,
      );

      expect(container.childElementCount).toBe(0);
    },
  );

  it("shows retry progress", () => {
    render(
      <ProviderStatusCard
        retry={{
          model_call_id: "call-1",
          retry_index: 2,
          max_retries: 5,
          delay_ms: 2100,
        }}
      />,
    );

    expect(screen.getByRole("status").textContent).toContain("2/5");
    expect(screen.getByRole("status").textContent).toContain("2100 ms");
  });

  it("shows an unknown reason verbatim and renders raw body as text", () => {
    const rawBody = '<img src=x onerror="alert(1)">';
    const { container } = render(
      <ProviderStatusCard
        status={{
          model_call_id: "call-1",
          http_status: 200,
          finish_reason: "VendorLimit",
          error_body: rawBody,
        }}
      />,
    );

    expect(screen.getByLabelText("Provider status").textContent).toContain(
      "VendorLimit",
    );
    expect(container.querySelector("pre")?.textContent).toContain(rawBody);
    expect(container.querySelector("img")).toBeNull();
  });

  it("explains that the raw body is unavailable after history restore", () => {
    render(
      <ProviderStatusCard
        status={{
          model_call_id: "call-1",
          http_status: 429,
          finish_reason: null,
        }}
      />,
    );

    expect(screen.queryByText("原始响应仅在错误发生时可用。")).not.toBeNull();
  });

  it("shows null HTTP and finish reason for a transport error", () => {
    render(
      <ProviderStatusCard
        transportError={{
          model_call_id: "call-1",
          http_status: null,
          finish_reason: null,
          error_type: "ReadTimeout",
          error_message: "timed out",
        }}
      />,
    );

    const card = screen.getByLabelText("Provider status");
    expect(card.textContent).toContain("HTTP null");
    expect(card.textContent).toContain("finish_reason: null");
    expect(card.textContent).toContain("ReadTimeout");
  });
});
