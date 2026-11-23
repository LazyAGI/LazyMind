import { BASE_URL } from '@/components/request';
import { GITHUB_STAR_SUPPORT_ACCEPTED_EVENT, GITHUB_STAR_SUPPORT_ACCEPTED_STORAGE_KEY } from './api';

function serviceScope() { return BASE_URL || window.location.origin; }

function confirmedSupportKey(owner: string) {
  return `${GITHUB_STAR_SUPPORT_ACCEPTED_STORAGE_KEY}:confirmed:${JSON.stringify([serviceScope(), owner])}`;
}

export function hasConfirmedGitHubStarSupport(owner: string) {
  try { return localStorage.getItem(confirmedSupportKey(owner)) === 'true'; } catch { return false; }
}

export function publishGitHubStarSupport(owner: string, confirmed = false) {
  if (confirmed) {
    try { localStorage.setItem(confirmedSupportKey(owner), 'true'); } catch { /* Core still persists the preference when browser storage is unavailable. */ }
  }
  const noticeId = Array.from(crypto.getRandomValues(new Uint32Array(4)), value => value.toString(16)).join('-');
  const notice = { service_scope: serviceScope(), owner_user_id: owner, notice_id: noticeId };
  window.dispatchEvent(new CustomEvent(GITHUB_STAR_SUPPORT_ACCEPTED_EVENT, { detail: notice }));
  try { localStorage.setItem(GITHUB_STAR_SUPPORT_ACCEPTED_STORAGE_KEY, JSON.stringify(notice)); } catch {
    // Same-window invalidation still works when browser storage is unavailable.
  }
}

export function subscribeGitHubStarSupport(owner: string, invalidate: () => void) {
  function receive(value: unknown) {
    if (!value || typeof value !== 'object') return;
    const notice = value as { service_scope?: unknown; owner_user_id?: unknown; notice_id?: unknown };
    if (notice.service_scope === serviceScope() && notice.owner_user_id === owner
      && typeof notice.notice_id === 'string' && notice.notice_id) invalidate();
  }
  const event = (value: Event) => receive((value as CustomEvent).detail);
  const storage = (value: StorageEvent) => {
    if (value.storageArea !== localStorage || value.key !== GITHUB_STAR_SUPPORT_ACCEPTED_STORAGE_KEY || !value.newValue) return;
    try { receive(JSON.parse(value.newValue)); } catch { /* Ignore unrelated malformed browser data. */ }
  };
  window.addEventListener(GITHUB_STAR_SUPPORT_ACCEPTED_EVENT, event);
  window.addEventListener('storage', storage);
  return () => {
    window.removeEventListener(GITHUB_STAR_SUPPORT_ACCEPTED_EVENT, event);
    window.removeEventListener('storage', storage);
  };
}
