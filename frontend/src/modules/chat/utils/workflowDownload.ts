/** File delivery adapter; the Workflow UI and file generation stay shared. */
export interface WorkflowDownloadSource {
  filename: string;
  blob?: Blob;
  href?: string;
}

interface DownloadHost { origin: string; bridgeId: string }

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

function requestHost(host: DownloadHost, action: 'capabilities' | 'save', payload: object = {}): Promise<void> {
  const requestId = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const finish = (error?: Error) => {
      clearTimeout(timer);
      window.removeEventListener('message', receive);
      window.removeEventListener('pagehide', closed);
      error ? reject(error) : resolve();
    };
    const receive = (event: MessageEvent) => {
      const data = event.data;
      if (event.source !== window.parent || event.origin !== host.origin
        || data?.type !== 'lazymind.workflow.download.result'
        || data.bridgeId !== host.bridgeId || data.requestId !== requestId) return;
      if (data.ok === true) finish();
      else finish(new Error(typeof data.error === 'string' ? data.error : 'Download failed or was cancelled.'));
    };
    const closed = () => finish(new Error('Download panel was closed.'));
    const timer = window.setTimeout(() => finish(new Error('Download response timed out. Check the save dialog before retrying.')),
      action === 'capabilities' ? 5000 : 310_000);
    window.addEventListener('message', receive);
    window.addEventListener('pagehide', closed, { once: true });
    try {
      window.parent.postMessage({ type: 'lazymind.workflow.download', bridgeId: host.bridgeId,
        requestId, action, ...payload }, host.origin);
    } catch (error) { finish(error instanceof Error ? error : new Error(String(error))); }
  });
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
