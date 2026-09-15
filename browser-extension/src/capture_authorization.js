export function assertCaptureAuthorized({deviceId, deviceToken, connectionState} = {}) {
  if (!String(deviceId || '').trim() || !String(deviceToken || '').trim()) {
    throw authorizationError(
      'PAIRING_REQUIRED',
      '请先登录 LazyMind，在“设置 → 系统工具 → 依赖安装 → 浏览器控制扩展”中生成配对码并完成配对。',
    );
  }
  if (connectionState !== 'connected') {
    throw authorizationError(
      'LAZYMIND_CONNECTION_REQUIRED',
      'LazyMind 当前未连接或登录授权已失效。请先登录 LazyMind，并在扩展中重新连接后再抓取。',
    );
  }
}

function authorizationError(code, message) {
  const error = new Error(message);
  error.code = code;
  return error;
}
