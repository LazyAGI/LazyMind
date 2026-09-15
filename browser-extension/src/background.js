import {captureCurrentPage} from './capture.js';
import {assertCaptureAuthorized} from './capture_authorization.js';
import {BrowserController} from './controller.js';
import {detectBrowserIdentity} from './browser_identity.js';

const DEFAULT_GATEWAY = 'http://127.0.0.1:8090';
const RECONNECT_ALARM = 'lazymind-browser-reconnect';
const SOCKET_KEEPALIVE_MS = 20_000;
const controller = new BrowserController();
const browserIdentity = detectBrowserIdentity();

let socket = null;
let reconnectTimer = null;
let keepAliveTimer = null;
let reconnectAttempt = 0;
let connectionState = 'disconnected';
let lastError = '';

chrome.runtime.onInstalled.addListener(async () => {
  const current = await chrome.storage.local.get(['gateway_url']);
  if (!current.gateway_url) await chrome.storage.local.set({gateway_url: DEFAULT_GATEWAY});
  await chrome.alarms.create(RECONNECT_ALARM, {periodInMinutes: 1});
  void connect();
});

chrome.runtime.onStartup.addListener(() => void connect());
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === RECONNECT_ALARM && connectionState === 'disconnected') void connect();
});

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  void handlePopupMessage(message)
    .then((result) => sendResponse({ok: true, result}))
    .catch((error) => sendResponse({
      ok: false,
      error: {code: error?.code || 'EXTENSION_ERROR', message: String(error?.message || error)},
    }));
  return true;
});

void connect();

async function handlePopupMessage(message = {}) {
  switch (message.type) {
    case 'get_state': return getState();
    case 'save_gateway': return saveGateway(message.gateway_url);
    case 'pair': return pair(message.code, message.gateway_url);
    case 'disconnect': return disconnectAndForget();
    case 'capture_now':
      await assertPopupCaptureAuthorized();
      return captureCurrentPage();
    case 'reconnect':
      await connect(true);
      return getState();
    default:
      throw extensionError('INVALID_MESSAGE', '未知扩展消息');
  }
}

async function getState() {
  const stored = await chrome.storage.local.get([
    'gateway_url', 'device_id', 'device_token', 'device_name',
  ]);
  return {
    gateway_url: stored.gateway_url || DEFAULT_GATEWAY,
    paired: Boolean(stored.device_id && stored.device_token),
    device_id: stored.device_id || '',
    device_name: stored.device_name || '',
    browser_name: browserIdentity.name,
    browser_version: browserIdentity.version,
    connection_state: connectionState,
    last_error: lastError,
  };
}

async function saveGateway(rawURL) {
  const gatewayURL = normalizeGatewayURL(rawURL);
  await chrome.storage.local.set({gateway_url: gatewayURL});
  closeSocket();
  await connect(true);
  return getState();
}

async function pair(rawCode, rawGatewayURL) {
  const gatewayURL = normalizeGatewayURL(rawGatewayURL || DEFAULT_GATEWAY);
  const code = String(rawCode || '').trim().toUpperCase();
  if (!code) throw extensionError('PAIRING_CODE_REQUIRED', '请输入 LazyMind 生成的配对码');
  const response = await fetch(`${gatewayURL}/api/browser/v1/pair`, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      code,
      device_name: browserIdentity.deviceName,
      browser: browserIdentity.name,
      browser_version: browserIdentity.version,
      extension_version: chrome.runtime.getManifest().version,
      version: chrome.runtime.getManifest().version,
    }),
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok || !body.device_id || !body.device_token) {
    throw extensionError(
      body.error?.code || 'PAIRING_FAILED',
      body.error?.message || `配对失败（HTTP ${response.status}）`,
    );
  }
  await chrome.storage.local.set({
    gateway_url: gatewayURL,
    device_id: body.device_id,
    device_token: body.device_token,
    device_name: browserIdentity.deviceName,
  });
  closeSocket();
  await connect(true);
  return getState();
}

async function disconnectAndForget() {
  closeSocket();
  await chrome.storage.local.remove(['device_id', 'device_token', 'device_name']);
  connectionState = 'unpaired';
  lastError = '';
  return getState();
}

async function assertPopupCaptureAuthorized() {
  const stored = await chrome.storage.local.get(['device_id', 'device_token']);
  assertCaptureAuthorized({
    deviceId: stored.device_id,
    deviceToken: stored.device_token,
    connectionState,
  });
}

async function connect(force = false) {
  if (!force && (connectionState === 'connecting' || connectionState === 'connected')) return;
  const stored = await chrome.storage.local.get(['gateway_url', 'device_id', 'device_token']);
  if (!stored.device_id || !stored.device_token) {
    connectionState = 'unpaired';
    return;
  }
  closeSocket();
  connectionState = 'connecting';
  lastError = '';
  const gatewayURL = normalizeGatewayURL(stored.gateway_url || DEFAULT_GATEWAY);
  const wsURL = new URL(`${gatewayURL}/api/browser/v1/connect`);
  wsURL.protocol = wsURL.protocol === 'https:' ? 'wss:' : 'ws:';
  const currentSocket = new WebSocket(wsURL.toString(), ['lazymind.browser.v1']);
  socket = currentSocket;

  currentSocket.addEventListener('open', () => {
    currentSocket.send(JSON.stringify({
      type: 'hello',
      protocol_version: '1',
      device_id: stored.device_id,
      device_token: stored.device_token,
    }));
  });
  currentSocket.addEventListener('message', (event) => void handleSocketMessage(currentSocket, event.data));
  currentSocket.addEventListener('close', () => {
    if (socket !== currentSocket) return;
    stopSocketKeepalive();
    socket = null;
    if (connectionState === 'unauthorized') return;
    connectionState = 'disconnected';
    scheduleReconnect();
  });
  currentSocket.addEventListener('error', () => {
    if (socket !== currentSocket) return;
    lastError = '无法连接 LazyMind Browser Gateway';
  });
}

async function handleSocketMessage(currentSocket, rawMessage) {
  let message;
  try { message = JSON.parse(rawMessage); } catch { return; }
  if (message.type === 'hello_ack') {
    connectionState = 'connected';
    lastError = '';
    reconnectAttempt = 0;
    startSocketKeepalive(currentSocket);
    return;
  }
  if (message.type === 'hello_error') {
    connectionState = 'unauthorized';
    lastError = '设备授权已失效。请先登录 LazyMind，断开当前设备后使用新的配对码重新连接。';
    currentSocket.close();
    return;
  }
  if (message.type !== 'command' || !message.id) return;
  if (Number(message.deadline_ms) > 0 && Date.now() > Number(message.deadline_ms)) {
    sendResult(currentSocket, message.id, null, extensionError('ACTION_TIMEOUT', '命令到达时已经过期'));
    return;
  }
  try {
    const result = message.action === 'capture_current_page'
      ? await captureCurrentPage(message.payload || {})
      : await controller.dispatch(message.action, message.payload || {});
    sendResult(currentSocket, message.id, result, null);
  } catch (error) {
    sendResult(currentSocket, message.id, null, error);
  }
}

function sendResult(currentSocket, id, result, error) {
  if (currentSocket.readyState !== WebSocket.OPEN) return;
  currentSocket.send(JSON.stringify(error ? {
    type: 'result', id, ok: false,
    error: {
      code: error?.code || 'ACTION_FAILED',
      message: String(error?.message || error),
      details: error?.details,
    },
  } : {type: 'result', id, ok: true, result: result || {}}));
}

function scheduleReconnect() {
  if (reconnectTimer) clearTimeout(reconnectTimer);
  const delay = Math.min(1000 * (2 ** reconnectAttempt), 30000);
  reconnectAttempt += 1;
  reconnectTimer = setTimeout(() => void connect(), delay);
}

function startSocketKeepalive(currentSocket) {
  stopSocketKeepalive();
  keepAliveTimer = setInterval(() => {
    if (socket !== currentSocket || currentSocket.readyState !== WebSocket.OPEN) return;
    // Application messages keep a Manifest V3 service worker alive. WebSocket
    // control-frame pings do not reach JavaScript and therefore are not enough.
    currentSocket.send(JSON.stringify({type: 'ping', at: Date.now()}));
  }, SOCKET_KEEPALIVE_MS);
}

function stopSocketKeepalive() {
  if (keepAliveTimer) clearInterval(keepAliveTimer);
  keepAliveTimer = null;
}

function closeSocket() {
  if (reconnectTimer) clearTimeout(reconnectTimer);
  reconnectTimer = null;
  stopSocketKeepalive();
  if (socket) socket.close();
  socket = null;
}

function normalizeGatewayURL(rawURL) {
  let parsed;
  try { parsed = new URL(String(rawURL || '').trim()); } catch {
    throw extensionError('INVALID_GATEWAY_URL', 'LazyMind 地址格式无效');
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw extensionError('INVALID_GATEWAY_URL', 'LazyMind 地址必须使用 http 或 https');
  }
  parsed.pathname = parsed.pathname.replace(/\/$/, '');
  parsed.search = '';
  parsed.hash = '';
  return parsed.toString().replace(/\/$/, '');
}

function extensionError(code, message, details) {
  const error = new Error(message);
  error.code = code;
  error.details = details;
  return error;
}
