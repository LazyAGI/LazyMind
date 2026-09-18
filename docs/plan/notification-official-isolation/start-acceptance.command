#!/bin/zsh
# Local acceptance launcher; reuse the existing packaged CLI and session adapter.
set -eu
export LAZYMIND_ACCEPTANCE_REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
export LAZYMIND_ACCEPTANCE_ROOT="$HOME/Library/Application Support/LazyMind-Notification-Acceptance"
export LAZYMIND_ACCEPTANCE_APP="$LAZYMIND_ACCEPTANCE_REPO/desktop/dist/mac-arm64/LazyMind.app"
export LAZYMIND_ACCEPTANCE_BIN="$LAZYMIND_ACCEPTANCE_APP/Contents/Resources/runtime/bin/lazymind"
export LAZYMIND_HOME="$LAZYMIND_ACCEPTANCE_ROOT/credentials"
export LAZYMIND_DESKTOP_RUNTIME_ROOT="$LAZYMIND_ACCEPTANCE_ROOT/runtime"
export LAZYMIND_DESKTOP_AGENT_CONNECTOR="$LAZYMIND_ACCEPTANCE_REPO/docs/plan/task-notification-review-fixes/acceptance-cli.sh"
export LAZYMIND_JWT_TTL_MINUTES=2
export PYTHONDONTWRITEBYTECODE=1
/usr/bin/python3 - <<'PY'
import os
import subprocess
from pathlib import Path

os.umask(0o077)
root = Path(os.environ['LAZYMIND_ACCEPTANCE_ROOT'])
app = Path(os.environ['LAZYMIND_ACCEPTANCE_APP'])
executable = app / 'Contents/MacOS/LazyMind'
if not executable.is_file():
    raise SystemExit('桌面包不存在，请先按验收手册构建。')
root.mkdir(parents=True, exist_ok=True)
pidfile = root / 'app.pid'
running = False
if pidfile.exists() and pidfile.read_text().strip().isdigit():
    result = subprocess.run(['ps', '-p', pidfile.read_text().strip(), '-o', 'command='], capture_output=True, text=True)
    running = result.returncode == 0 and result.stdout.strip().startswith(str(executable) + ' ') and str(root / 'profile') in result.stdout
with (root / 'launch.log').open('a') as log:
    child = subprocess.Popen([str(executable), '--user-data-dir=' + str(root / 'profile')], stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
if not running:
    pidfile.write_text(str(child.pid) + '\n')
print('已请求显示 LazyMind 窗口。' if running else 'LazyMind 正在启动，首次初始化请稍候。')
PY
