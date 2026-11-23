import { createRequire } from "node:module";
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import svgr from "vite-plugin-svgr";
import path from "node:path";
import { pdfCmapsPlugin } from "./scripts/vite/pdfCmaps";

export default defineConfig(({ mode }) => {
  const require = createRequire(import.meta.url);
  // React-PDF may resolve a different PDF.js version than the direct dependency.
  const pdfjsRoot = path.dirname(require.resolve("pdfjs-dist/package.json", { paths: [path.dirname(require.resolve("react-pdf/package.json"))] }));
  const env = loadEnv(mode, __dirname);
  // Read the same mode-specific environment files as the application.
  const devProxyTarget = env.VITE_PROXY_TARGET || "http://localhost:8090";
  const isDesktopBuild = env.VITE_LAZYMIND_MODE === "desktop";

  return {
    plugins: [pdfCmapsPlugin(pdfjsRoot), react(), svgr()],
    base: "/",
    optimizeDeps: {
      // The PDF worker is loaded as an asset URL, not as a prebundled module.
      exclude: ["@react-pdf-worker/pdf.worker.min.mjs?url"],
    },
    resolve: {
      // Keep application imports independent of the current file location.
      alias: {
        "@": path.resolve(__dirname, "src"),
        "@react-pdf-worker": path.join(pdfjsRoot, "build"),
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
      // The local proxy allowlist expects this origin; do not silently switch ports.
      strictPort: true,
      // Forward API and local-service requests to the selected backend.
      proxy: {
        "/api": {
          target: devProxyTarget,
          changeOrigin: true,
          ws: true,
          timeout: 3 * 60 * 1000,
          proxyTimeout: 3 * 60 * 1000,
        },
        "/_local": {
          target: devProxyTarget,
          changeOrigin: true,
          timeout: 3 * 60 * 1000,
          proxyTimeout: 3 * 60 * 1000,
        },
        // Local pnpm preview: serve curated assets via compose/nginx frontend.
        "/showcase-assets": {
          target: devProxyTarget,
          changeOrigin: true,
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
            // Keep Vite's shared preload helper out of the lazy Monaco chunk.
            if (id.includes('vite/preload-helper')) {
              return 'preload-helper';
            }
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
  };
});
