const english = new URLSearchParams(location.search).get('lang') === 'en';
const retry = document.getElementById('retry');
const home = document.getElementById('home');
const status = document.getElementById('status');
if (english) {
  document.documentElement.lang = 'en';
  document.getElementById('title').textContent = 'This page cannot be displayed';
  document.getElementById('description').textContent = 'Try reloading this page. Unsaved changes may be lost.';
  retry.textContent = 'Retry'; home.textContent = 'Back to home';
}
async function recover(action) {
  if (retry.disabled) return;
  retry.disabled = home.disabled = true;
  status.textContent = english ? 'Reloading…' : '正在重新加载…';
  try { if ((await window.pageRecovery[action]()).ok) return; } catch { /* Show only safe recovery text. */ }
  status.textContent = english ? 'Unable to load the page. Please try again.' : '页面仍无法加载，请重试。';
  retry.disabled = home.disabled = false;
  retry.focus();
}
retry.addEventListener('click', () => void recover('retry'));
home.addEventListener('click', () => void recover('home'));
retry.focus();
