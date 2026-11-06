import { afterEach, expect, it, vi } from 'vitest';
import { clearLocalAssistantSession, syncLocalAssistantSession } from './assistantSession';

afterEach(() => { vi.restoreAllMocks(); Reflect.deleteProperty(window, 'lazymindDesktop'); });
const expected = { server_url: 'http://localhost:8090', access_token: 'old', refresh_token: 'old-refresh' };

it('does not infer permission to delete a connector login from an empty browser session', async () => {
  const request = vi.spyOn(globalThis, 'fetch');
  await syncLocalAssistantSession(null, expected.server_url, 1000);
  await clearLocalAssistantSession();
  expect(request).not.toHaveBeenCalled();
});

it('sends the failed credential snapshot with a browser cleanup request', async () => {
  const request = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"ok":true,"cleared":false}', { status: 200 }));
  await clearLocalAssistantSession(1000, expected);
  const init = request.mock.calls[0][1]!;
  expect(init.method).toBe('DELETE');
  expect(JSON.parse(String(init.body))).toEqual({ expected_session: expected, allow_renewed: false });
});

it('forwards explicit logout provenance through the same desktop bridge contract', async () => {
  const clear = vi.fn().mockResolvedValue({ ok: true, cleared: true });
  Object.defineProperty(window, 'lazymindDesktop', { configurable: true, value: { assistantSessionClear: clear } });
  await clearLocalAssistantSession(1000, expected, true);
  expect(clear).toHaveBeenCalledWith({ expected_session: expected, allow_renewed: true });
});
