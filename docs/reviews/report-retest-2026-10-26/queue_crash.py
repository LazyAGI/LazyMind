import os, sys, tempfile, subprocess, json
from pathlib import Path
with tempfile.TemporaryDirectory() as td:
 env=dict(os.environ,LAZYLLM_HOME=td)
 code="import os; from lazyllm.common.queue import SQLiteQueue; q=SQLiteQueue('report-crash'); print(q.db_path,flush=True); q._lock.acquire(); os._exit(0)"
 p=subprocess.run([sys.executable,'-c',code],env=env,capture_output=True,text=True,timeout=15,check=True)
 assert str(Path(td)/'.lazyllm_filesystem_queue.db') in p.stdout,p.stdout
 lock=Path(td)/'.lazyllm_filesystem_queue.db.lock'
 assert lock.exists()
 code2="from lazyllm.common.queue import SQLiteQueue; q=SQLiteQueue('report-recover'); q._enqueue('a','ok'); assert q._dequeue('a') == ['ok']; print('recovered')"
 p=subprocess.Popen([sys.executable,'-c',code2],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 try:
  stdout,stderr=p.communicate(timeout=5)
  result={'crash_left_lock':True,'recovered_without_manual_removal':p.returncode==0,'output':stdout.strip()}
 except subprocess.TimeoutExpired:
  p.kill();p.communicate();result={'crash_left_lock':True,'recovered_without_manual_removal':False,'blocked_over_5_seconds':True}
 Path('/tmp/report-retest-1026/queue-crash.json').write_text(json.dumps(result,indent=2))
 print(result)
