import {isSupportedPageURL} from './capture.js';

const elements = {
  state: document.querySelector('#state'),
  gateway: document.querySelector('#gateway'),
  pairing: document.querySelector('#pairing'),
  pairingCode: document.querySelector('#pairingCode'),
  connected: document.querySelector('#connected'),
  deviceId: document.querySelector('#deviceId'),
  message: document.querySelector('#message'),
  browserName: document.querySelector('#browserName'),
  pairingBrowserName: document.querySelector('#pairingBrowserName'),
  grant: document.querySelector('#grant'),
  revokeSite: document.querySelector('#revokeSite'),
  capture: document.querySelector('#capture'),
};

let currentSite = null;
let pageReadingGranted = false;
let currentConnectionState = 'disconnected';
let busy = false;

document.querySelector('#saveGateway').addEventListener('click', () => runGatewayAction('save_gateway', {
  gateway_url: elements.gateway.value,
}, '地址已保存', false));
document.querySelector('#pair').addEventListener('click', () => runGatewayAction('pair', {
  code: elements.pairingCode.value,
  gateway_url: elements.gateway.value,
}, '配对成功', true));
document.querySelector('#grant').addEventListener('click', () => grantPageReading());
document.querySelector('#revokeSite').addEventListener('click', () => revokePageReading());
document.querySelector('#capture').addEventListener('click', async () => {
  const result = await run('capture_now', {}, '');
  if (result) showMessage(`抓取成功：${result.content?.returned_chars || 0} 字符`, false);
});
document.querySelector('#reconnect').addEventListener('click', () => run('reconnect', {}, '已请求重新连接'));
document.querySelector('#disconnect').addEventListener('click', () => run('disconnect', {}, '已断开'));

void refresh();
setInterval(() => void refresh(), 1000);

async function run(type, payload, successMessage) {
  setBusy(true);
  try {
    const response = await chrome.runtime.sendMessage({type, ...payload});
    if (!response?.ok) throw new Error(response?.error?.message || '操作失败');
    if (successMessage) showMessage(successMessage, false);
    await refresh();
    return response.result;
  } catch (error) {
    showMessage(String(error?.message || error), true);
    return null;
  } finally {
    setBusy(false);
  }
}

async function runGatewayAction(type, payload, successMessage, includePageReading) {
  try {
    const gateway = new URL(elements.gateway.value.trim());
    if (gateway.protocol !== 'http:' && gateway.protocol !== 'https:') {
      throw new Error('LazyMind 地址必须使用 http 或 https');
    }
    const origins = [`${gateway.origin}/*`];
    if (includePageReading) origins.push('http://*/*', 'https://*/*');
    const granted = await chrome.permissions.request({origins});
    if (!granted) {
      throw new Error(includePageReading
        ? '配对需要允许扩展连接 LazyMind 并读取网页'
        : '需要授权扩展访问该 LazyMind 地址');
    }
  } catch (error) {
    showMessage(String(error?.message || 'LazyMind 地址格式无效'), true);
    return null;
  }
  return run(type, payload, successMessage);
}

async function grantPageReading() {
  setBusy(true);
  try {
    // chrome.permissions.request must be invoked directly from the popup click
    // handler. Forwarding it to the service worker loses the user gesture.
    const granted = await chrome.permissions.request({origins: ['http://*/*', 'https://*/*']});
    if (!granted) throw new Error('用户未授予网页读取权限');
    showMessage('网页读取已开启，后续无需逐站点授权', false);
    await refresh();
  } catch (error) {
    showMessage(String(error?.message || error), true);
  } finally {
    setBusy(false);
  }
}

async function revokePageReading() {
  setBusy(true);
  try {
    const removed = await chrome.permissions.remove({origins: ['http://*/*', 'https://*/*']});
    if (!removed) throw new Error('网页读取权限尚未开启');
    showMessage('网页读取授权已关闭；对话仍可按 URL 打开受管窗口', false);
    await refresh();
  } catch (error) {
    showMessage(String(error?.message || error), true);
  } finally {
    setBusy(false);
  }
}

async function refresh() {
  const [response, tabs] = await Promise.all([
    chrome.runtime.sendMessage({type: 'get_state'}),
    chrome.tabs.query({active: true, lastFocusedWindow: true}),
  ]);
  const tabURL = tabs[0]?.url || '';
  currentSite = isSupportedPageURL(tabURL) ? {url: tabURL} : null;
  pageReadingGranted = await chrome.permissions.contains({
    origins: ['http://*/*', 'https://*/*'],
  });
  if (!response?.ok) return;
  const state = response.result;
  elements.browserName.textContent = state.browser_version
    ? `${state.browser_name} ${state.browser_version}`
    : state.browser_name;
  elements.pairingBrowserName.textContent = state.browser_name;
  elements.gateway.value = state.gateway_url;
  elements.pairing.hidden = state.paired;
  elements.connected.hidden = !state.paired;
  elements.deviceId.textContent = state.device_id || '';
  currentConnectionState = state.connection_state;
  elements.state.textContent = stateLabel(state.connection_state);
  elements.state.dataset.status = state.connection_state;
  renderCaptureControls();
  if (state.last_error) showMessage(state.last_error, true);
}

function stateLabel(state) {
  return {
    connected: '已连接', connecting: '连接中', disconnected: '已断开',
    unauthorized: '认证失败', unpaired: '未配对',
  }[state] || state;
}

function showMessage(message, error) {
  elements.message.textContent = message;
  elements.message.classList.toggle('error', error);
}

function setBusy(nextBusy) {
  busy = nextBusy;
  document.querySelectorAll('button').forEach((button) => { button.disabled = nextBusy; });
  if (!nextBusy) renderCaptureControls();
}

function renderCaptureControls() {
  elements.grant.hidden = pageReadingGranted;
  elements.revokeSite.hidden = !pageReadingGranted;
  if (busy) return;
  elements.grant.disabled = false;
  elements.revokeSite.disabled = !pageReadingGranted;
  elements.capture.disabled = !currentSite
    || !pageReadingGranted
    || currentConnectionState !== 'connected';
}
