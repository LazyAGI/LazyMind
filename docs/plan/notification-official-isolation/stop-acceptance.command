#!/bin/zsh
# Stop only the isolated acceptance runtime, after the operator finishes all tasks.
set -eu
export LAZYMIND_ACCEPTANCE_REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
/usr/bin/python3 - <<'PY'
import json, os, signal, subprocess, time
from pathlib import Path
root = Path.home() / 'Library/Application Support/LazyMind-Notification-Acceptance'
app = Path(os.environ['LAZYMIND_ACCEPTANCE_REPO']) / 'desktop/dist/mac-arm64/LazyMind.app'
executable = app / 'Contents/MacOS/LazyMind'
pidfile = root / 'app.pid'
if not pidfile.is_file() or not pidfile.read_text().strip().isdigit():
    raise SystemExit('没有可核对的验收进程 PID；未结束任何进程。')
pid = int(pidfile.read_text().strip())
def matches():
    p = subprocess.run(['ps', '-p', str(pid), '-o', 'command='], capture_output=True, text=True)
    return p.returncode == 0 and p.stdout.strip().startswith(str(executable) + ' ') and str(root / 'profile') in p.stdout
if not matches():
    raise SystemExit('验收进程已退出或 PID 不匹配；未结束任何进程。')
s = json.loads((root / 'runtime/state/runtime-state.json').read_text())
resources = app / 'Contents/Resources/runtime'
if Path(s['resourcesRoot']).resolve() != resources.resolve():
    raise SystemExit('运行资源路径不匹配；已停止操作。')
args = [str(resources / 'bin/local-runtime-manager'), 'down', '--profile', 'desktop',
        '--runtime-root', str(root / 'runtime'), '--repo-root', s['repoRoot'],
        '--resources-root', str(resources), '--owner-token', s['ownerToken']]
try:
    result = subprocess.run(args, capture_output=True, text=True, timeout=55)
except subprocess.TimeoutExpired:
    raise SystemExit('后台停止超时；保留进程，请排查后再操作。')
if result.returncode:
    raise SystemExit('后台停止失败；保留进程，请排查后再操作。')
if matches():
    os.kill(pid, signal.SIGTERM)
    for _ in range(10):
        if not matches():
            break
        time.sleep(1)
if matches():
    os.kill(pid, signal.SIGKILL)
    time.sleep(1)
if matches():
    raise SystemExit('验收主进程尚未退出，请检查。')
print('验收后台及桌面主进程已停止；数据已保留。')
PY
