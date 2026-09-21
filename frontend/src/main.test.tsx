import { expect, it, vi } from "vitest";

const { render } = vi.hoisted(() => ({ render: vi.fn() }));

vi.mock("react-dom/client", () => ({
  createRoot: () => ({ render }),
}));

// Vite can evaluate the editor's shared C++ chunk before its C chunk.
// Use the real grammar to reproduce that startup order without loading the app.
vi.mock("./App", async () => {
  await import("prismjs/components/prism-cpp");
  return { default: () => null };
});

vi.mock("./components/GlobalErrorBoundary", () => ({
  default: ({ children }: { children: React.ReactNode }) => children,
}));
vi.mock("./index.scss", () => ({}));
vi.mock("./i18n", () => ({}));

it("renders the app when an editor evaluates a dependent Prism grammar during startup", async () => {
  document.body.innerHTML = '<div id="app"></div>';

  await expect(import("./main")).resolves.toBeDefined();

  expect(render).toHaveBeenCalledOnce();
});
