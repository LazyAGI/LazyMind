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
  const sync = () => { void syncManagedBrowser().catch(() => {}); };
  sync();
  window.addEventListener(AUTH_USER_CHANGE_EVENT, sync);
  // Keeps credentials current and retries startup after the local runtime recovers.
  const timer = window.setInterval(sync, 30000);
  return () => {
    window.removeEventListener(AUTH_USER_CHANGE_EVENT, sync);
    window.clearInterval(timer);
  };
}
