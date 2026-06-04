import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import svgr from "vite-plugin-svgr";
import path from "node:path";
var devProxyTarget = process.env.VITE_PROXY_TARGET || "http://localhost:5024";
function jsPreviewExcelShimPlugin() {
    var RESOLVED_ID = "\0virtual:js-preview-excel-shim";
    return {
        name: "js-preview-excel-shim",
        enforce: "pre",
        resolveId: function (id) {
            if (id === "@js-preview/excel")
                return RESOLVED_ID;
        },
        load: function (id) {
            if (id === RESOLVED_ID) {
                return "const jsPreviewExcel = window.jsPreviewExcel;\nexport default jsPreviewExcel;\n";
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
    server: {
        port: 5173,
        proxy: {
            "/api": {
                target: devProxyTarget,
                changeOrigin: true,
                timeout: 3 * 60 * 1000,
                proxyTimeout: 3 * 60 * 1000,
            },
        },
    },
    build: {
        outDir: "dist",
    },
});
