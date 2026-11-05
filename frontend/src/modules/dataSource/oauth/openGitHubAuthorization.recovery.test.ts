import { afterEach, expect, it, vi } from 'vitest';
import { openGitHubCLIAuthorization } from './openManagedAuthorization';

const url = 'https://github.com/login/device';
afterEach(() => vi.restoreAllMocks());

it('uses a verifiable blank window and removes its opener before navigating', async () => {
  const target = { closed: false, opener: window, document: document.implementation.createHTMLDocument(),
    close: vi.fn(), location: { replace: vi.fn() } };
  const navigations = vi.fn();
  target.document.addEventListener('click', event => {
    event.preventDefault(); // jsdom cannot navigate; real Referer behavior is verified separately.
    const link = event.target as HTMLAnchorElement;
    navigations({ href: link.href, rel: link.rel, target: link.target,
      policy: link.referrerPolicy || link.getAttribute('referrerpolicy'), opener: target.opener });
  });
  const open = vi.spyOn(window, 'open').mockReturnValue(target as unknown as Window);
  await expect(openGitHubCLIAuthorization(url)).resolves.toEqual({ ok: true });
  expect(open).toHaveBeenCalledWith('about:blank', '_blank');
  expect(open).toHaveBeenCalledTimes(1);
  expect(navigations).toHaveBeenCalledOnce();
  expect(navigations).toHaveBeenCalledWith({ href: url, rel: 'noreferrer', target: '_self',
    policy: 'no-referrer', opener: null });
  expect(target.location.replace).not.toHaveBeenCalled();
});

it('reports a genuinely blocked blank window without opening another page', async () => {
  const open = vi.spyOn(window, 'open').mockReturnValue(null);
  await expect(openGitHubCLIAuthorization(url)).resolves.toEqual({ ok: false, reason: 'blocked' });
  expect(open).toHaveBeenCalledTimes(1);
});

it('rejects non-device URLs without opening any window', async () => {
  const open = vi.spyOn(window, 'open');
  await expect(openGitHubCLIAuthorization('https://example.invalid/device')).resolves.toEqual({ ok: false, reason: 'invalid' });
  expect(open).not.toHaveBeenCalled();
});

