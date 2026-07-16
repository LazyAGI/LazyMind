import { defineConfig, Plugin } from "vite";
import react from "@vitejs/plugin-react";
import svgr from "vite-plugin-svgr";
import path from "node:path";

// Allow local development to target a custom backend without changing this file.
const devProxyTarget =
  process.env.VITE_PROXY_TARGET || "http://10.210.0.49:5024";
const isDesktopBuild = process.env.VITE_LAZYMIND_MODE === "desktop";

// Expose the globally loaded Excel preview library as an ES module.
function jsPreviewExcelShimPlugin(): Plugin {
  const RESOLVED_ID = "\0virtual:js-preview-excel-shim";

  return {
    name: "js-preview-excel-shim",
    enforce: "pre",
    resolveId(id) {
      if (id === "@js-preview/excel") return RESOLVED_ID;
    },
    load(id) {
      if (id === RESOLVED_ID) {
        return `const jsPreviewExcel = window.jsPreviewExcel;\nexport default jsPreviewExcel;\n`;
      }
    },
  };
}

export default defineConfig({
  plugins: [jsPreviewExcelShimPlugin(), react(), svgr()],
  base: "/",
  resolve: {
    // Keep application imports independent of the current file location.
    alias: {
      "@": path.resolve(__dirname, "src"),
    },
  },
  css: {
    // Use Sass's modern compiler API for both supported syntaxes.
    preprocessorOptions: {
      scss: {
        api: "modern-compiler",
      },
      sass: {
        api: "modern-compiler",
      },
    },
  },
  server: {
    port: 5173,
    // Forward API and local-service requests to the selected backend.
    proxy: {
      "/api": {
        target: devProxyTarget,
        changeOrigin: true,
        timeout: 3 * 60 * 1000,
        proxyTimeout: 3 * 60 * 1000,
      },
      "/_local": {
        target: devProxyTarget,
        changeOrigin: true,
        timeout: 3 * 60 * 1000,
        proxyTimeout: 3 * 60 * 1000,
      },
    },
  },
  build: {
    outDir: "dist",
    rollupOptions: {
      onwarn(warning, warn) {
        // Third-party client components emit harmless directives in desktop builds.
        if (
          isDesktopBuild &&
          warning.code === "MODULE_LEVEL_DIRECTIVE" &&
          warning.message.includes('"use client"') &&
          warning.id?.includes("node_modules")
        ) {
          return;
        }

        warn(warning);
      },
      output: {
        manualChunks(id) {
          // Split monaco-editor into its own chunk to avoid bundling it with the main app.
          // This also prevents Node.js OOM during Vite build by keeping chunk sizes manageable.
          if (id.includes('monaco-editor')) {
            return 'monaco-editor';
          }
          // Keep the graph editor dependencies out of the main application chunk.
          if (id.includes('@xyflow/react') || id.includes('@xyflow/')) {
            return 'xyflow';
          }
        },
      },
    },
  },
});
