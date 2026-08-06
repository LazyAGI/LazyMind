import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const renderMock = vi.fn();
const createRootMock = vi.fn(() => ({ render: renderMock }));

vi.mock("react-dom/client", () => ({
  createRoot: createRootMock,
}));

vi.mock("./App", () => ({
  default: () => "app",
}));

vi.mock("./components/GlobalErrorBoundary", () => ({
  default: ({ children }: { children: React.ReactNode }) => children,
}));

vi.mock("./index.scss", () => ({}));
vi.mock("./i18n", () => ({}));

describe("main entry", () => {
  beforeEach(() => {
    vi.resetModules();
    renderMock.mockClear();
    createRootMock.mockClear();
    document.body.innerHTML = '<div id="app"></div>';
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("mounts the app into the #app root element", async () => {
    await import("./main");

    const container = document.getElementById("app");
    expect(createRootMock).toHaveBeenCalledWith(container);
    expect(renderMock).toHaveBeenCalledTimes(1);
  });

  it("throws when the #app root element is missing", async () => {
    document.body.innerHTML = "";

    await expect(import("./main")).rejects.toThrow("Root element #app not found");
  });
});
