import { defineConfig, Plugin } from "vite";
import react from "@vitejs/plugin-react";
import svgr from "vite-plugin-svgr";
import path from "node:path";

const devProxyTarget =
  process.env.VITE_PROXY_TARGET || "http://localhost:8090";
const isDesktopBuild = process.env.VITE_LAZYMIND_MODE === "desktop";

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
    alias: {
      "@": path.resolve(__dirname, "src"),
    },
  },
  css: {
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
          if (id.includes('@xyflow/react') || id.includes('@xyflow/')) {
            return 'xyflow';
          }
        },
      },
    },
  },
});
