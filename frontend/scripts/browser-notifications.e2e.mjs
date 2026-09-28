// Use an already installed Playwright module through PLAYWRIGHT_MODULE.
// Real Chromium locks/storage/Notification are used; HTTP/auth are test fixtures.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(import.meta.url);
const { build } = createRequire(require.resolve('vite'))('esbuild');
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = path.resolve(import.meta.dirname, '..');
let browser;
let server;
try {
  const result = await build({
    stdin: { contents: `import { startBrowserNotifications } from ${JSON.stringify(path.join(root, 'src/modules/notifications/browser.ts'))}; startBrowserNotifications();`, resolveDir: root, loader: 'ts' },
    bundle: true, write: false, format: 'iife', platform: 'browser',
    plugins: [{ name: 'fixture', setup(build) {
      build.onResolve({ filter: /^@\/(components\/(auth|request)|runtime\/mode)$/ }, args => ({ path: args.path, namespace: 'fixture' }));
      build.onLoad({ filter: /.*/, namespace: 'fixture' }, args => ({ loader: 'js', contents:
        args.path.endsWith('/mode') ? 'export const isDesktopRuntime=()=>false;' :
        args.path.endsWith('/auth') ? `export const AUTH_USER_CHANGE_EVENT='lazymind:user-change',AUTH_LOGOUT_EVENT='lazymind:logout';export const AgentAppsAuth={getUserInfo:()=>JSON.parse(localStorage.getItem('lazymind:user')||'null')};` :
        `export const BASE_URL=location.origin;export const axiosInstance={request:async c=>{const r=await fetch(c.url,{method:c.method,signal:c.signal,body:c.data?JSON.stringify(c.data):undefined});if(!r.ok)throw new Error('http');return {data:await r.json()}}};`
      }));
    } }],
  });
  let pending = false;
  let receipts = 0;
  const notice = { notification_id: 'e'.repeat(64), user_id: 'owner', task_id: 'e2e-task', channel: 'desktop', status: 'pending', title: 'LazyMind 浏览器通知验收', body: '这是一条浏览器多标签页测试通知。' };
  server = createServer((req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    if (req.url === '/bundle.js') { res.setHeader('Content-Type', 'text/javascript'); res.end(result.outputFiles[0].text); return; }
    if (req.url.startsWith('/api/')) {
      res.setHeader('Content-Type', 'application/json');
      if (req.url.endsWith('/auth/me')) res.end(JSON.stringify({ user_id: 'owner', status: 'active' }));
      else if (req.url === '/api/core/task-center/tasks/e2e-task') res.end(JSON.stringify({ conversation_id: 'safe-conversation' }));
      else if (req.method === 'POST') { receipts++; res.end('{}'); }
      else res.end(JSON.stringify({ data: { device_id: 'browser', items: pending && !receipts ? [notice] : [], next_cursor: '' } }));
      return;
    }
    res.setHeader('Content-Type', 'text/html');
    res.end('<!doctype html><title>LazyMind notification test</title><p>Isolated notification test</p><script src="/bundle.js"></script>');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true });
  const context = await browser.newContext({ permissions: ['notifications'] });
  await context.addInitScript(() => {
    localStorage.setItem('lazymind:user', JSON.stringify({ userId: 'owner', token: 'fixture' }));
    window.noticeCalls = 0;
    window.Notification = new Proxy(window.Notification, { construct(target, args) {
      window.noticeCalls++;
      window.lastNotice = Reflect.construct(target, args);
      return window.lastNotice;
    } });
  });
  const first = await context.newPage(); const second = await context.newPage();
  await Promise.all([first.goto(origin), second.goto(origin)]);
  assert.equal(await first.evaluate(() => !!navigator.locks && Notification.permission === 'granted'), true);
  pending = true;
  await first.waitForFunction(() => Object.values(localStorage).some(v => v.includes('submitting') || v.includes('shown') || v.includes('acked')), null, { timeout: 20000 });
  await new Promise(resolve => setTimeout(resolve, 12000));
  const counts = await Promise.all([first.evaluate(() => window.noticeCalls), second.evaluate(() => window.noticeCalls)]);
  assert.equal(counts[0] + counts[1], 1, 'duplicate OS submission');
  const emitter = counts[0] ? first : second;
  const survivor = counts[0] ? second : first;
  await emitter.evaluate(() => window.lastNotice.dispatchEvent(new Event('click')));
  await emitter.waitForURL('**/agent/chat/home/safe-conversation');
  await emitter.close();
  await survivor.evaluate(() => window.dispatchEvent(new Event('focus')));
  await new Promise(resolve => setTimeout(resolve, 5500));
  assert.equal(await survivor.evaluate(() => window.noticeCalls), 0, 'tab takeover replayed notification');
  await survivor.evaluate(() => { localStorage.removeItem('lazymind:user'); window.dispatchEvent(new Event('lazymind:user-change')); });
  assert.equal(await survivor.evaluate(() => window.noticeCalls), 0);
  console.log(JSON.stringify({ realBrowser: 'Chromium', tabs: 2, notificationSubmissions: 1, takeoverDuplicates: 0, clickRouteVerified: true, browserShowReceipts: receipts, osVisualVerified: false }));
} finally {
  await browser?.close();
  if (server) await new Promise(resolve => server.close(resolve));
}
