import { createPreviewRenderer } from "./createPreviewRenderer";

export { default as RenderHtml } from "./render-html";
export { default as RenderTxt } from "./render-txt";
export { default as RenderMarkdown } from "./render-markdown";
export const RenderPpt = createPreviewRenderer(() => import("./render-ppt"));
export const RenderExcel = createPreviewRenderer(() => import("./render-excel"));
export const RenderWord = createPreviewRenderer(() => import("./render-word"));
