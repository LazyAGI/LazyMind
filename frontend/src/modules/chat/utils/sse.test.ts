import { describe, expect, it, vi } from "vitest";
import { SSE } from "./sse";

describe("SSE transport errors", () => {
  it("exposes the XHR status on the error event", () => {
    const sse = new SSE("/stream", { start: false });
    const listener = vi.fn();
    const xhr = {
      response: "service unavailable",
      status: 503,
      abort: vi.fn(),
    };
    sse.xhr = xhr as unknown as XMLHttpRequest;
    sse.addEventListener("error", listener);

    (sse as any).onStreamFailure({ currentTarget: xhr } as unknown as Event);

    expect(listener).toHaveBeenCalledTimes(1);
    expect(listener.mock.calls[0][0]).toMatchObject({
      data: "service unavailable",
      status: 503,
    });
  });
});
