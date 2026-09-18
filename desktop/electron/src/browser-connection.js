// Credentials stay in the main process. Pairing uses the existing authenticated
// Core endpoint; no public auto-pair endpoint or shared secret is introduced.
class BrowserConnection {
  constructor({ WebSocket, fetch, createController, version, browserVersion, onError = () => {} }) {
    Object.assign(this, { WebSocket, fetch, createController, version, browserVersion, onError });
    this.generation = 0;
    this.state = 'signed_out';
    this.queue = Promise.resolve();
  }

  status() { return { state: this.state, error: this.error || '', deviceId: this.device?.device_id || '' }; }

  async setSession(value) {
    if (!value?.access_token || !value?.user_id) return this.clear();
    const server = new URL(value.server_url);
    if (!['http:', 'https:'].includes(server.protocol) || server.username || server.password) {
      throw new Error('Invalid LazyMind server URL');
    }
    const serverURL = server.href.replace(/\/$/, '');
    const identity = `${serverURL}\n${value.user_id}`;
    if (identity === this.identity) {
      this.auth = { ...value, server_url: serverURL };
      if (!this.socket && !this.connecting) this.schedule(0);
      return this.status();
    }
    await this.clear();
    this.identity = identity;
    this.auth = { ...value, server_url: serverURL };
    const generation = this.generation;
    let controller;
    try {
      controller = await this.createController(serverURL, value.user_id);
    } catch (error) {
      if (generation === this.generation) {
        this.identity = null;
        this.state = 'error';
        this.error = error.message;
      }
      throw error;
    }
    if (generation !== this.generation) { await controller.dispose(); return this.status(); }
    this.controller = controller;
    this.schedule(0);
    return this.status();
  }

  async clear() {
    this.generation++;
    clearTimeout(this.timer);
    clearTimeout(this.handshakeTimer);
    this.socket?.close();
    this.socket = null;
    this.connecting = false;
    this.auth = null;
    this.identity = null;
    this.device = null;
    this.state = 'signed_out';
    this.error = '';
    const controller = this.controller;
    this.controller = null;
    if (controller) await controller.dispose();
  }

  schedule(delay = 2000) {
    clearTimeout(this.timer);
    if (!this.auth || !this.controller) return;
    this.timer = setTimeout(() => void this.connect(), delay);
    this.timer.unref?.();
  }

  async json(path, body, authenticated, auth) {
    const response = await this.fetch(`${auth.server_url}${path}`, {
      method: 'POST', redirect: 'error', signal: AbortSignal.timeout(10000),
      headers: {
        'Content-Type': 'application/json',
        ...(authenticated ? { Authorization: `Bearer ${auth.access_token}` } : {}),
      },
      body: JSON.stringify(body),
    });
    if (!response.ok) throw new Error(`Browser connection failed (HTTP ${response.status})`);
    const result = await response.json();
    return result.data || result;
  }

  async connect() {
    if (!this.auth || this.socket || this.connecting) return;
    const generation = this.generation;
    const auth = this.auth;
    this.connecting = true;
    this.state = 'connecting';
    try {
      if (!this.device) {
        const pairing = await this.json('/api/core/browser/manage/pairings', {}, true, auth);
        if (generation !== this.generation) return;
        const device = await this.json('/api/browser/v1/pair', {
          code: pairing.code, device_name: this.controller.browserName || 'LazyMind Browser',
          browser: this.controller.browserName || 'LazyMind Browser',
          browser_version: this.controller.browserVersion ?? this.browserVersion, version: this.version,
        }, false, auth);
        if (generation !== this.generation) return;
        if (!device.device_id || !device.device_token) throw new Error('Invalid browser device response');
        this.device = device;
      }
      const url = new URL(`${auth.server_url}/api/browser/v1/connect`);
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
      const socket = new this.WebSocket(url.href, ['lazymind.browser.v1'], { maxPayload: 16 << 20 });
      this.socket = socket;
      this.handshakeTimer = setTimeout(() => {
        if (this.socket === socket && this.state !== 'connected') socket.terminate();
      }, 10000);
      socket.on('open', () => {
        if (generation !== this.generation) return;
        socket.send(JSON.stringify({ type: 'hello', protocol_version: '1', ...this.device }));
      });
      socket.on('message', (raw) => {
        if (generation !== this.generation || this.socket !== socket) return;
        let message;
        try { message = JSON.parse(String(raw)); } catch { return; }
        if (message.type === 'hello_ack') {
          clearTimeout(this.handshakeTimer);
          this.state = 'connected'; this.error = '';
        } else if (message.type === 'hello_error') {
          // Core currently stores devices in memory. Re-pair automatically after restart.
          this.device = null;
          socket.close();
        } else if (message.type === 'command' && message.id && this.state === 'connected') {
          const controller = this.controller;
          this.queue = this.queue.catch(() => {}).then(async () => {
            if (generation !== this.generation || this.socket !== socket) return;
            let result;
            try {
              if (message.deadline_ms > 0 && Date.now() > message.deadline_ms) {
                throw Object.assign(new Error('Browser command expired'), { code: 'ACTION_TIMEOUT' });
              }
              result = { ok: true, result: await controller.dispatch(message.action, message.payload || {}) };
            } catch (error) {
              result = { ok: false, error: { code: error.code || 'ACTION_FAILED', message: error.message } };
            }
            if (generation === this.generation && this.socket === socket && socket.readyState === 1) {
              socket.send(JSON.stringify({ type: 'result', id: message.id, ...result }));
            }
          });
        }
      });
      socket.on('error', () => { if (this.socket === socket) this.error = 'Browser gateway unavailable'; });
      socket.on('close', () => {
        if (generation !== this.generation || this.socket !== socket) return;
        clearTimeout(this.handshakeTimer);
        this.socket = null;
        this.state = 'disconnected';
        this.schedule();
      });
    } catch (error) {
      if (generation !== this.generation) return;
      this.state = 'error'; this.error = error.message;
      this.onError(error);
      this.schedule(5000);
    } finally {
      if (generation === this.generation) this.connecting = false;
    }
  }

  async open(url) {
    if (!this.controller) throw new Error('Sign in to LazyMind before opening the browser');
    return this.controller.dispatch('open', { url, allow_private_network: true });
  }
}

module.exports = { BrowserConnection };
