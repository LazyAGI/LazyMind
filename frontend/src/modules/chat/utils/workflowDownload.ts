import { AssistantBridgeRequestError } from '@/runtime/assistantSession';

/** File delivery adapter; the Workflow UI and file generation stay shared. */
export interface WorkflowDownloadSource {
  filename: string;
  blob?: Blob;
  href?: string;
}

interface DownloadHost { origin: string; bridgeId: string }

export class WorkflowDownloadCancelled extends Error {
  constructor() { super('Save cancelled.'); }
}

export function getWorkflowDownloadHost(): DownloadHost | null {
  const params = new URLSearchParams(window.location.search);
  const origin = params.get('downloadHostOrigin');
  const bridgeId = params.get('downloadBridgeId');
  if (!origin && !bridgeId) return null;
  // A configured but invalid bridge must not silently fall back to a blocked download.
  const url = new URL(origin || '');
  if (window.parent === window || !/^\/workflow-runs\/[^/]+\/embed\/?$/.test(window.location.pathname)
    || url.origin !== origin || url.protocol !== 'http:'
    || !['127.0.0.1', 'localhost'].includes(url.hostname)
    || !bridgeId || !/^[a-zA-Z0-9-]{20,80}$/.test(bridgeId)) {
    throw new Error('Download connection is invalid. Reopen this panel.');
  }
  return { origin, bridgeId };
}

function requestHost<T = void>(host: DownloadHost, action: 'capabilities' | 'save' | 'logs', payload: object = {}, signal?: AbortSignal): Promise<T> {
  const requestId = crypto.randomUUID();
  const type = action === 'logs' ? 'lazymind.workflow.host-logs' : 'lazymind.workflow.download';
  return new Promise((resolve, reject) => {
    const finish = (error?: Error, result?: T) => {
      clearTimeout(timer);
      window.removeEventListener('message', receive);
      window.removeEventListener('pagehide', closed);
      signal?.removeEventListener('abort', closed);
      error ? reject(error) : resolve(result as T);
    };
    const receive = (event: MessageEvent) => {
      const data = event.data;
      if (event.source !== window.parent || event.origin !== host.origin
        || data?.type !== `${type}.result`
        || data.bridgeId !== host.bridgeId || data.requestId !== requestId) return;
      if (data.ok === true) finish(undefined, data.result as T);
      else if (action === 'save' && data.cancelled === true) finish(new WorkflowDownloadCancelled());
      else if (action === 'logs') finish(new AssistantBridgeRequestError('Unable to read Workflow host logs.', data.code));
      else finish(new Error(typeof data.error === 'string' ? data.error : 'Download failed or was cancelled.'));
    };
    const closed = () => finish(new Error('Download panel was closed.'));
    const timer = window.setTimeout(() => finish(new Error('Download response timed out. Check the save dialog before retrying.')),
      action === 'logs' ? 17_000 : action === 'capabilities' ? 5000 : 310_000);
    if (signal?.aborted) { closed(); return; }
    signal?.addEventListener('abort', closed, { once: true });
    window.addEventListener('message', receive);
    window.addEventListener('pagehide', closed, { once: true });
    try {
      window.parent.postMessage({ type, bridgeId: host.bridgeId,
        requestId, action, ...payload }, host.origin);
    } catch (error) { finish(error instanceof Error ? error : new Error(String(error))); }
  });
}

export function readEmbeddedWorkflowHostLogs<T>(grant: string, signal: AbortSignal): Promise<T> | undefined {
  const host = getWorkflowDownloadHost();
  return host ? requestHost<T>(host, 'logs', { grant }, signal) : undefined;
}

export async function downloadWorkflowFile(source: WorkflowDownloadSource): Promise<void> {
  const host = getWorkflowDownloadHost();
  if (host) {
    await requestHost(host, 'capabilities');
    let blob = source.blob;
    if (!blob && source.href) {
      // Fetch in the original page: never pass credentials or private URLs to the host.
      const response = await fetch(source.href);
      if (!response.ok) throw new Error('Unable to read the download file.');
      blob = await response.blob();
    }
    if (!blob) throw new Error('Download file is unavailable.');
    await requestHost(host, 'save', { filename: source.filename, blob });
    return;
  }
  const url = source.href ?? (source.blob ? URL.createObjectURL(source.blob) : undefined);
  if (!url) throw new Error('Download file is unavailable.');
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = source.filename;
  anchor.rel = 'noopener';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  if (!source.href) window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
