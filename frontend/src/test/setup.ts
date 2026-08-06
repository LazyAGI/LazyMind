import "@testing-library/jest-dom";
import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});

// jsdom does not implement matchMedia; antd components read it during layout checks.
// NOTE: intentionally a plain function (not vi.fn()) so it survives `vi.restoreAllMocks()`
// calls in individual test files' afterEach hooks - restoring a vi.fn() strips its
// mockImplementation and makes it return undefined, crashing antd's responsive observer.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

// jsdom does not implement ResizeObserver; several antd/virtualized components rely on it.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (!("ResizeObserver" in window)) {
  // @ts-expect-error jsdom polyfill
  window.ResizeObserver = ResizeObserverStub;
}

if (!("IntersectionObserver" in window)) {
  class IntersectionObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return [];
    }
  }
  // @ts-expect-error jsdom polyfill
  window.IntersectionObserver = IntersectionObserverStub;
}

if (!window.scrollTo) {
  window.scrollTo = vi.fn();
}
