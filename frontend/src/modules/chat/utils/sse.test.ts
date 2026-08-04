import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Method, SSE, TriggerEvent, XHRStates } from "./sse";

class MockXHR {
  static instances: MockXHR[] = [];
  method = "";
  url = "";
  status = 200;
  readyState = 0;
  responseText = "";
  requestHeaders: Record<string, string> = {};
  withCredentials = false;
  sentPayload: unknown = null;
  aborted = false;
  listeners: Record<string, Array<(e: unknown) => void>> = {};

  constructor() {
    MockXHR.instances.push(this);
  }

  addEventListener(type: string, cb: (e: unknown) => void) {
    this.listeners[type] = this.listeners[type] || [];
    this.listeners[type]!.push(cb);
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  setRequestHeader(key: string, value: string) {
    this.requestHeaders[key] = value;
  }

  send(payload: unknown) {
    this.sentPayload = payload;
  }

  abort() {
    // A real XMLHttpRequest only fires the abort event once per in-flight
    // request; guard against re-entrancy the same way to avoid infinite
    // recursion through SSE's close() -> abort() -> onStreamAbort -> close().
    if (this.aborted) {
      return;
    }
    this.aborted = true;
    this.emit("abort", new Event("abort"));
  }

  emit(type: string, event: unknown) {
    (this.listeners[type] || []).forEach((cb) => cb(event));
  }
}

describe("SSE enums", () => {
  it("exposes the expected method and readyState values", () => {
    expect(Method.GET).toBe("GET");
    expect(Method.POST).toBe("POST");
    expect(XHRStates.OPEN).toBe(1);
    expect(XHRStates.CLOSED).toBe(2);
    expect(TriggerEvent.ERROR).toBe("error");
  });
});

describe("SSE", () => {
  let originalXHR: typeof XMLHttpRequest;

  beforeEach(() => {
    originalXHR = window.XMLHttpRequest;
    MockXHR.instances = [];
    (window as unknown as { XMLHttpRequest: unknown }).XMLHttpRequest =
      MockXHR as unknown as typeof XMLHttpRequest;
  });

  afterEach(() => {
    (window as unknown as { XMLHttpRequest: unknown }).XMLHttpRequest = originalXHR;
    vi.restoreAllMocks();
  });

  it("defaults to GET when there is no payload, and opens the connection on construction", () => {
    const sse = new SSE("http://example.com/stream");
    const xhr = MockXHR.instances[0]!;

    expect(xhr.method).toBe("GET");
    expect(xhr.url).toBe("http://example.com/stream");
    sse.close();
  });

  it("uses POST automatically when a payload is provided", () => {
    const sse = new SSE("http://example.com/stream", { payload: '{"q":1}' });
    const xhr = MockXHR.instances[0]!;

    expect(xhr.method).toBe("POST");
    expect(xhr.sentPayload).toBe('{"q":1}');
    sse.close();
  });

  it("does not auto-start the connection when start is false", () => {
    new SSE("http://example.com/stream", { start: false });
    expect(MockXHR.instances).toHaveLength(0);
  });

  it("parses a single SSE message chunk and dispatches a message event", () => {
    const onMessage = vi.fn();
    const sse = new SSE("http://example.com/stream", {
      callbacks: { message: onMessage },
    });
    const xhr = MockXHR.instances[0]!;
    xhr.status = 200;
    xhr.responseText = "data: hello\n\n";
    xhr.emit("progress", new ProgressEvent("progress"));

    expect(onMessage).toHaveBeenCalledTimes(1);
    const event = onMessage.mock.calls[0]![0] as CustomEvent;
    expect(event.data).toBe("hello");
    sse.close();
  });

  it("dispatches an error event and closes when the xhr status is not 200", () => {
    const onError = vi.fn();
    const sse = new SSE("http://example.com/stream", {
      callbacks: { error: onError },
    });
    const xhr = MockXHR.instances[0]!;
    xhr.status = 500;
    xhr.responseText = "boom";
    const progressEvent = new ProgressEvent("progress");
    Object.defineProperty(progressEvent, "currentTarget", { value: xhr });
    xhr.emit("progress", progressEvent);

    expect(onError).toHaveBeenCalledTimes(1);
    expect(sse.readyState).toBe(2);
  });

  it("close() aborts the underlying xhr and sets readyState to CLOSED", () => {
    const sse = new SSE("http://example.com/stream");
    expect(sse.readyState).not.toBe(2);

    sse.close();

    expect(sse.readyState).toBe(2);
    expect(sse.xhr).toBeNull();
  });

  it("removeEventListener stops a previously added listener from firing", () => {
    const sse = new SSE("http://example.com/stream", { start: false, manual: true });
    const listener = vi.fn();
    sse.addEventListener("message", listener);
    sse.removeEventListener("message", listener);

    const dispatched = sse.dispatchEvent(new CustomEvent("message"));

    expect(listener).not.toHaveBeenCalled();
    expect(dispatched).toBe(true);
  });
});
