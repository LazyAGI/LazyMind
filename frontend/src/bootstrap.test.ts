import { readFileSync } from 'node:fs';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

afterEach(() => { delete window.lazymindDesktop; });

beforeEach(() => {
  const html = readFileSync('index.html', 'utf8');
  document.body.innerHTML = html.slice(html.indexOf('<body>') + 6, html.indexOf('</body>'));
  window.eval(document.querySelector('script:not([type])')!.textContent!);
});

it('reveals keyboard-accessible recovery only on an explicit startup failure', () => {
  const fallback = document.getElementById('startup-error')!;
  const notifyAppReady = vi.fn();
  window.lazymindDesktop = { notifyAppReady };
  expect(fallback.hidden).toBe(true);
  document.dispatchEvent(new Event('lazymind:startup-failed'));
  expect(fallback.hidden).toBe(false);
  expect(document.activeElement?.id).toBe('startup-error-retry');
  expect(notifyAppReady).toHaveBeenCalledOnce();
});

it('does not replace a mounted app for an unrelated resource error', () => {
  document.getElementById('app')!.innerHTML = '<main>Ready</main>';
  document.dispatchEvent(new Event('lazymind:startup-failed'));
  expect(document.getElementById('startup-error')!.hidden).toBe(true);
});
