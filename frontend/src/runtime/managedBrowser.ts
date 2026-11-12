import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import { getApiBaseUrl } from './apiBase';

export interface ManagedBrowserStatus {
  state: string;
  error?: string;
  deviceId?: string;
  engine?: 'builtin' | 'edge';
  edgeAvailable?: boolean;
}

interface ManagedBrowserBridge {
  browserSessionSet: (value: { server_url: string; user_id: string; access_token: string } | null) => Promise<ManagedBrowserStatus>;
  browserStatus: () => Promise<ManagedBrowserStatus>;
  browserSelect?: (engine: 'builtin' | 'edge') => Promise<ManagedBrowserStatus>;
  browserOpen: (url: string) => Promise<unknown>;
}

function bridge(): ManagedBrowserBridge | undefined {
  const value = (window as Window & { lazymindDesktop?: Partial<ManagedBrowserBridge> }).lazymindDesktop;
  return value?.browserSessionSet && value.browserOpen && value.browserStatus
    ? value as ManagedBrowserBridge : undefined;
}

export function hasManagedBrowser(): boolean { return Boolean(bridge()); }

export async function syncManagedBrowser(): Promise<ManagedBrowserStatus> {
  const desktop = bridge();
  if (!desktop) throw new Error('Desktop browser is unavailable');
  const user = AgentAppsAuth.getUserInfo();
  return desktop.browserSessionSet(user?.token ? {
    server_url: getApiBaseUrl(), user_id: user.userId || user.username, access_token: user.token,
  } : null);
}

export async function managedBrowserStatus(): Promise<ManagedBrowserStatus> {
  const desktop = bridge();
  if (!desktop) throw new Error('Desktop browser is unavailable');
  return desktop.browserStatus();
}

export async function openManagedBrowser(url: string): Promise<void> {
  await syncManagedBrowser();
  await bridge()!.browserOpen(url);
}

export async function selectManagedBrowser(engine: 'builtin' | 'edge'): Promise<ManagedBrowserStatus> {
  const desktop = bridge();
  if (!desktop?.browserSelect) throw new Error('Browser selection is unavailable; restart the updated Desktop app');
  await syncManagedBrowser();
  return desktop.browserSelect(engine);
}

export function startManagedBrowserSync(): () => void {
  if (!hasManagedBrowser()) return () => {};
  let retryTimer: number | undefined;
  let revision = 0;
  const sync = () => {
    window.clearTimeout(retryTimer);
    const currentRevision = ++revision;
    // Desktop handles reconnects after accepting the session. Retry here only
    // when Desktop could not accept the session update.
    void syncManagedBrowser().catch(() => {
      if (currentRevision === revision) retryTimer = window.setTimeout(sync, 30_000);
    });
  };
  sync();
  window.addEventListener(AUTH_USER_CHANGE_EVENT, sync);
  return () => {
    revision++;
    window.removeEventListener(AUTH_USER_CHANGE_EVENT, sync);
    window.clearTimeout(retryTimer);
  };
}
