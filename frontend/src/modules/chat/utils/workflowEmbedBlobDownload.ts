import { downloadWorkflowFile, getWorkflowDownloadHost, type WorkflowDownloadSource } from './workflowDownload';

type Status = 'blob-download-started' | 'blob-download-returned' | 'blob-download-error';

/** Deliver scripted local Blob downloads, including detached FileSaver anchors.
 * Scoped by the entry function below; does not alter file generation or bytes.
 * Synthetic download activation is consumed, because non-cancelable detached
 * clicks cannot be intercepted by document event listeners.
 */
export function installBlobDownloadAdapter(
  deliver: (source: WorkflowDownloadSource) => Promise<void>,
  report: (status: Status, detail?: string) => void,
): () => void {
  const prototype = HTMLAnchorElement.prototype;
  const originalClick = prototype.click;
  const originalDispatch = prototype.dispatchEvent;
  const clickDescriptor = Object.getOwnPropertyDescriptor(prototype, 'click');
  const dispatchDescriptor = Object.getOwnPropertyDescriptor(prototype, 'dispatchEvent');
  const pending = new Set<HTMLAnchorElement>();
  const controller = new AbortController();
  let stopped = false;
  const notify = (status: Status, detail?: string) => {
    if (!stopped) { try { report(status, detail); } catch { /* Save feedback must not interrupt file delivery. */ } }
  };
  const activate = (anchor: HTMLAnchorElement): boolean => {
    if (stopped || anchor.ownerDocument !== document || !anchor.download) return false;
    const href = anchor.href;
    if (!href.startsWith(`blob:${window.location.origin}/`)) return false;
    if (pending.has(anchor)) return true;
    pending.add(anchor);
    const filename = anchor.download;
    notify('blob-download-started');
    // Begin reading immediately, before a library can revoke its temporary URL.
    void (async () => {
      try {
        const response = await fetch(href, { signal: controller.signal });
        if (!response.ok) throw new Error('Unable to read the generated file.');
        const blob = await response.blob();
        if (stopped) return;
        await deliver({ filename, blob });
        notify('blob-download-returned');
      } catch (error) {
        notify('blob-download-error', error instanceof Error ? error.message : String(error));
      } finally { pending.delete(anchor); }
    })();
    return true;
  };
  function click(this: HTMLAnchorElement) {
    if (!activate(this)) originalClick.call(this);
  }
  function dispatch(this: HTMLAnchorElement, event: Event): boolean {
    if (event.type === 'click' && !event.defaultPrevented && activate(this)) {
      if (event.cancelable) event.preventDefault();
      return !event.defaultPrevented;
    }
    return originalDispatch.call(this, event);
  }
  prototype.click = click;
  prototype.dispatchEvent = dispatch;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    controller.abort();
    pending.clear();
    if (prototype.click === click) {
      if (clickDescriptor) Object.defineProperty(prototype, 'click', clickDescriptor);
      else Reflect.deleteProperty(prototype, 'click');
    }
    if (prototype.dispatchEvent === dispatch) {
      if (dispatchDescriptor) Object.defineProperty(prototype, 'dispatchEvent', dispatchDescriptor);
      else Reflect.deleteProperty(prototype, 'dispatchEvent');
    }
    window.removeEventListener('pagehide', stop);
  };
  window.addEventListener('pagehide', stop, { once: true });
  return stop;
}

export function installWorkflowEmbedBlobDownloads(): () => void {
  if (new URLSearchParams(window.location.search).get('workbuddyBlobDownloads') !== '1') return () => {};
  try {
    const host = getWorkflowDownloadHost();
    if (!host) return () => {};
    return installBlobDownloadAdapter(downloadWorkflowFile, (status, detail) => {
      window.parent.postMessage({ type: 'lazymind.workflow.compatibility',
        bridgeId: host.bridgeId, status, ...(detail ? { detail: detail.slice(0, 500) } : {}),
      }, host.origin);
    });
  } catch { return () => {}; }
}
