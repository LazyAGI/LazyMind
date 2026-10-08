import "./src/test/setup";
import "@testing-library/jest-dom";
import { transferableAbortController } from "node:util";

// Node's fetch/Request reject jsdom's AbortSignal despite its matching shape.
// Keep the controller and signal in the same realm as the Fetch API used by routers.
const fetchController = transferableAbortController();
Object.defineProperty(globalThis, "AbortController", {
  configurable: true, writable: true, value: fetchController.constructor,
});
Object.defineProperty(globalThis, "AbortSignal", {
  configurable: true, writable: true, value: fetchController.signal.constructor,
});

// jsdom does not perform layout. Selection/highlight tests still need the browser
// geometry APIs to exist; tests asserting coordinates can override these stubs.
Object.defineProperty(Range.prototype, "getBoundingClientRect", {
  configurable: true, writable: true,
  value: () => new DOMRect(),
});
Object.defineProperty(Range.prototype, "getClientRects", {
  configurable: true, writable: true,
  value: () => Object.assign([], { item: () => null }),
});

function createMemoryStorage(): Storage {
  const store = new Map<string, string>();
  return {
    get length() {
      return store.size;
    },
    clear: () => {
      store.clear();
    },
    getItem: (key: string) => store.get(key) ?? null,
    key: (index: number) => Array.from(store.keys())[index] ?? null,
    removeItem: (key: string) => {
      store.delete(key);
    },
    setItem: (key: string, value: string) => {
      store.set(key, String(value));
    },
  };
}

const memoryLocalStorage = createMemoryStorage();
const memorySessionStorage = createMemoryStorage();
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: memoryLocalStorage,
});
Object.defineProperty(globalThis, "sessionStorage", {
  configurable: true,
  value: memorySessionStorage,
});
Object.defineProperty(window, "localStorage", {
  configurable: true,
  value: memoryLocalStorage,
});
Object.defineProperty(window, "sessionStorage", {
  configurable: true,
  value: memorySessionStorage,
});

Object.defineProperty(window, "matchMedia", {
  configurable: true,
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  }),
});
