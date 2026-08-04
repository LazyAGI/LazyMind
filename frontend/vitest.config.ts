import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import svgr from "vite-plugin-svgr";
import path from "node:path";

export default defineConfig({
  plugins: [react(), svgr()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      // monaco-editor's package.json only exposes a "module" field with no "main"/"exports",
      // which Vite's dependency resolver cannot resolve outside a real bundler context. Alias
      // it to a tiny stub so `vi.mock("monaco-editor", ...)` factories can intercept it in tests.
      "monaco-editor": path.resolve(__dirname, "src/test/mocks/monaco-editor.ts"),
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./vitest.setup.ts"],
    css: true,
    // `react-markdown-editor-lite` is referenced by MarkdownEditor but is not an
    // installed dependency (missing from package.json/node_modules). Without this
    // alias Vite fails to resolve the bare import before `vi.mock` factories run.
    alias: {
      "react-markdown-editor-lite/lib/index.css": path.resolve(
        __dirname,
        "src/test/mocks/empty.css",
      ),
      "react-markdown-editor-lite": path.resolve(
        __dirname,
        "src/test/mocks/react-markdown-editor-lite.tsx",
      ),
    },
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    exclude: ["src/api/generated/**", "node_modules/**", "dist/**"],
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      reportsDirectory: "./coverage",
      include: ["src/**/*.{ts,tsx}"],
      exclude: [
        "src/**/*.d.ts",
        "src/api/generated/**",
        "src/**/*.test.{ts,tsx}",
        "src/test/**",
      ],
    },
  },
});
