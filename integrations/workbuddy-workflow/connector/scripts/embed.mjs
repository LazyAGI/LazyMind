import { mkdir, copyFile, writeFile } from 'node:fs/promises';
const destination = new URL('../../../../local/lazymind-cli/internal/workbuddyhost/assets/connector.mjs', import.meta.url);
await mkdir(new URL('.', destination), { recursive: true });
await copyFile(new URL('../dist/main.mjs', import.meta.url), destination);

const { startPresentationFrame } = await import('../dist/diagnostics/presentation-frame.mjs');
await writeFile(new URL('presentation.html', destination), `<!doctype html><html><head><meta charset="utf-8"><meta name="lazymind-workflow-frontend" content="__LAZYMIND_FRONTEND_URL__"><style>
html,body{height:100%;margin:0;overflow:hidden;background:transparent}iframe{display:block;width:100%;height:100%;border:0}
#notice{position:fixed;bottom:8px;left:8px;right:8px;font:14px system-ui;padding:8px;background:Canvas;color:CanvasText}
#retry{position:fixed;top:12px;right:12px;padding:6px 12px;cursor:pointer}
[hidden]{display:none!important}</style></head><body><p id="notice" role="status">正在载入 Workflow…</p><button id="retry" hidden>重新载入</button>
<iframe id="workflow" title="Workflow 运行页"></iframe><script>(${startPresentationFrame.toString()})()</script></body></html>`);
