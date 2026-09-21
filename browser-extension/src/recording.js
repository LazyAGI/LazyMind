// Explicit, tab-scoped recording. No global keyboard hook, audio, or background-tab tracking.
export class BrowserRecorder {
  constructor(browser = globalThis.chrome) {
    this.browser = browser;
    this.sessions = new Map();
    this.onUpdated = (tabId, info) => {
      if (info.status !== 'complete') return;
      for (const session of this.sessions.values()) {
        if (session.tabId === tabId) { session.gaps = true; void this.install(session).catch(() => { session.gaps = true; }); }
      }
    };
    browser.tabs.onUpdated.addListener(this.onUpdated);
  }

  async dispatch(action, payload = {}) {
    if (action === 'recording_targets') {
      const tabs = await this.browser.tabs.query({});
      return { targets: tabs.filter((tab) => /^https?:\/\//.test(tab.url || '')).map((tab) => ({
        tab_id: String(tab.id), title: String(tab.title || '').slice(0, 200), origin: new URL(tab.url).origin,
      })) };
    }
    if (action === 'recording_start') {
      if (this.sessions.size) throw new Error('A browser recording is already running');
      const tabId = /^\d+$/.test(String(payload.tab_id)) ? Number(payload.tab_id) : String(payload.tab_id);
      const tab = await this.browser.tabs.get(tabId);
      if (!/^https?:\/\//.test(tab.url || '')) throw new Error('Select an HTTP or HTTPS page');
      const session = { id: crypto.randomUUID(), tabId, startedAt: Number(payload.started_at), expiresAt: Date.now() + 125000, gaps: false, events: [], bytes: 0 };
      if (!Number.isFinite(session.startedAt) || Math.abs(Date.now() - session.startedAt) > 30000) throw new Error('Invalid recording clock');
      this.sessions.set(session.id, session);
      try { await this.install(session); } catch (error) { this.sessions.delete(session.id); throw error; }
      if (this.sessions.get(session.id) !== session) {
        try { await this.browser.scripting.executeScript({ target: { tabId: session.tabId, allFrames: false }, func: readPageRecording, args: [session.id, true] }); } catch {}
        throw new Error('Recording cancelled');
      }
      session.timer = setTimeout(() => void this.cancel(session.id), 130000);
      session.timer.unref?.();
      return { session_id: session.id, limitations: ['top_level_document_only'] };
    }
    const session = this.sessions.get(payload.session_id);
    if (!session) throw new Error('Browser recording expired or disconnected');
    if (action === 'recording_cancel') { await this.cancel(session.id); return { cancelled: true }; }
    if (action !== 'recording_read' && action !== 'recording_stop') throw new Error('Unsupported recording action');
    try {
      const [execution] = await this.browser.scripting.executeScript({
        target: { tabId: session.tabId, allFrames: false }, func: readPageRecording,
        args: [session.id, action === 'recording_stop'],
      });
      const buffered = session.events.splice(0); session.bytes = 0;
      if (!execution?.result) { session.gaps = true; return { events: buffered, gaps: true }; }
      return { events: [...buffered, ...(execution.result.events || [])], gaps: session.gaps || execution.result.gaps };
    } catch {
      session.gaps = true;
      const buffered = session.events.splice(0); session.bytes = 0;
      return { events: buffered, gaps: true };
    } finally {
      if (action === 'recording_stop') await this.cancel(session.id);
    }
  }

  receive(message, sender) {
    const session = this.sessions.get(message.session_id);
    if (!session || sender.tab?.id !== session.tabId || sender.frameId !== 0 || Date.now() >= session.expiresAt) return;
    const event = message.event;
    if (!event || !['click', 'keydown', 'input', 'dom'].includes(event.kind) || !Number.isFinite(event.seconds)) return;
    const size = JSON.stringify(event).length;
    if (size > 100000 || session.bytes + size > 1000000 || session.events.length >= 300) { session.gaps = true; return; }
    session.events.push(event); session.bytes += size;
  }

  async install(session) {
    if (Date.now() >= session.expiresAt) return;
    const [execution] = await this.browser.scripting.executeScript({
      target: { tabId: session.tabId, allFrames: false }, func: installPageRecording,
      args: [session.id, session.startedAt, session.expiresAt],
    });
    if (!execution?.result?.installed) throw new Error('Page recording permission is unavailable. Enable page access in the LazyMind Browser extension.');
  }

  async cancel(id) {
    const session = this.sessions.get(id);
    if (!session) return;
    this.sessions.delete(id); clearTimeout(session.timer);
    try {
      await this.browser.scripting.executeScript({ target: { tabId: session.tabId, allFrames: false }, func: readPageRecording, args: [id, true] });
    } catch { /* Navigated/closed pages have already lost their listeners. */ }
  }

  async dispose() {
    this.browser.tabs.onUpdated.removeListener(this.onUpdated);
    await Promise.all([...this.sessions.keys()].map((id) => this.cancel(id)));
  }
}

// These functions are self-contained because browser.scripting serializes their source.
export function installPageRecording(id, startedAt, expiresAt) {
  const slot = '__lazymindSkillRecording';
  if (globalThis[slot]?.id === id) return { installed: true };
  globalThis[slot]?.stop?.();
  const events = [];
  let dropped = false, stopped = false, snapshotTimer, expiryTimer;
  let previousNodes = new Map(), previousPage = "";
  const clean = (value, max = 500) => String(value ?? '').slice(0, max);
  const target = (el) => {
    if (!(el instanceof Element)) return {};
    let selector = el.id ? `#${CSS.escape(el.id)}` : '';
    if (!selector) {
      const parts = [];
      for (let node = el; node && parts.length < 6; node = node.parentElement) {
        const tag = node.tagName.toLowerCase();
        const siblings = node.parentElement ? [...node.parentElement.children].filter((item) => item.tagName === node.tagName) : [node];
        parts.unshift(`${tag}:nth-of-type(${siblings.indexOf(node) + 1})`);
      }
      selector = parts.join(' > ');
    }
    return { tag: el.tagName.toLowerCase(), selector, role: clean(el.getAttribute('role'), 40),
      label: clean(el.getAttribute('aria-label') || (el.matches('input,textarea,select') ? el.getAttribute('placeholder') : el.textContent), 160) };
  };
  const push = (kind, data) => {
    if (stopped || Date.now() >= expiresAt) return;
    if (events.length >= 300) { dropped = true; return; }
    const event = { seconds: Math.max(0, (Date.now() - startedAt) / 1000), kind, ...data };
    if (globalThis.chrome?.runtime?.id) {
      // Send before a click navigates away; isolated extension scripts can reach this channel.
      void chrome.runtime.sendMessage({ type: 'recording_event', session_id: id, event }).catch(() => { dropped = true; });
    } else events.push(event);
  };
  const snapshot = () => {
    snapshotTimer = undefined;
    if (stopped) return;
    const nodes = [];
    for (const el of document.querySelectorAll('h1,h2,h3,button,a,input,textarea,select,[role],label,p,li,td,th')) {
      if (nodes.length >= 120) break;
      if (el.closest('script,style,[hidden],[aria-hidden="true"]') || !el.getClientRects().length) continue;
      nodes.push(target(el));
    }
    const url = clean(location.origin + location.pathname, 500);
    const title = clean(document.title, 160);
    const page = `${url}\n${title}`;
    const current = new Map(nodes.map((node) => [node.selector, JSON.stringify(node)]));
    const partial = page === previousPage;
    const changed = partial ? nodes.filter((node) => previousNodes.get(node.selector) !== current.get(node.selector)) : nodes;
    const removed = partial ? [...previousNodes.keys()].filter((selector) => !current.has(selector)) : [];
    if (!partial || changed.length || removed.length) push('dom', { url, title, nodes: changed, removed, partial });
    previousNodes = current; previousPage = page;
  };
  const queueSnapshot = () => { if (!stopped && !snapshotTimer) snapshotTimer = setTimeout(snapshot, 500); };
  const click = (event) => {
    if (!event.isTrusted) return;
    push('click', { target: target(event.target), x: Math.round(event.clientX), y: Math.round(event.clientY), button: event.button });
    queueSnapshot();
  };
  const keydown = (event) => {
    if (!event.isTrusted || event.repeat) return;
    const el = event.target;
    push('keydown', { target: target(el), key: clean(event.key, 30),
      modifiers: [event.ctrlKey && 'Control', event.metaKey && 'Meta', event.altKey && 'Alt', event.shiftKey && 'Shift'].filter(Boolean) });
  };
  const input = (event) => {
    if (!event.isTrusted) return;
    const el = event.target;
    push('input', { target: target(el), input_type: clean(event.inputType, 40),
      value: clean(el?.value ?? (el?.isContentEditable ? el.textContent : ''), 500) });
    queueSnapshot();
  };
  const observer = new MutationObserver(queueSnapshot);
  observer.observe(document.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: ['role', 'aria-label', 'aria-expanded', 'hidden'], characterData: true });
  document.addEventListener('click', click, true);
  document.addEventListener('keydown', keydown, true);
  document.addEventListener('input', input, true);
  document.addEventListener('change', input, true);
  const stop = () => {
    stopped = true; clearTimeout(snapshotTimer); clearTimeout(expiryTimer); observer.disconnect();
    document.removeEventListener('click', click, true); document.removeEventListener('keydown', keydown, true);
    document.removeEventListener('input', input, true); document.removeEventListener('change', input, true);
    if (globalThis[slot]?.id === id) delete globalThis[slot];
  };
  globalThis[slot] = { id, stop, drain: () => ({ events: events.splice(0), gaps: dropped }) };
  expiryTimer = setTimeout(stop, Math.max(0, expiresAt - Date.now()));
  snapshot();
  return { installed: true };
}

export function readPageRecording(id, stop) {
  const recording = globalThis.__lazymindSkillRecording;
  if (!recording || recording.id !== id) return { events: [], gaps: true };
  const result = recording.drain();
  if (stop) recording.stop();
  return result;
}
