import json, os, subprocess, sys, tempfile, time
from pathlib import Path
from lazyllm.module.llms.onlinemodule.base.onlineChatModuleBase import OnlineChatModuleBase
from lazyllm.module.llms.onlinemodule.supplier.siliconflow import SiliconFlowRerank
from lazyllm.common.queue import SQLiteQueue
out = {}
out['16_chat_urls'] = {u: OnlineChatModuleBase._get_chat_url(None, u) for u in ['https://token.sensenova.cn/v1', 'https://token.sensenova.cn/v1/', 'https://token.sensenova.cn/v1/chat/completions']}
r = SiliconFlowRerank(api_key='offline-test-not-a-real-key')
out['32_rerank'] = {'type': r.type, 'urls': {u: r._get_embed_url(u) for u in ['https://api.siliconflow.cn/v1','https://api.siliconflow.cn/v1/','https://api.siliconflow.cn/v1/rerank']}}
with tempfile.TemporaryDirectory() as td:
    child = "from lazyllm.common.queue import SQLiteQueue; q=SQLiteQueue('report_probe'); q._enqueue('probe','ok'); print(q._dequeue('probe'))"
    env = dict(os.environ, LAZYLLM_HOME=td)
    normal = subprocess.run([sys.executable, '-c', child], env=env, capture_output=True, text=True, timeout=15)
    assert normal.returncode == 0, normal.stderr
    lock = Path(td)/'.lazyllm_filesystem_queue.db.lock'
    lock.touch()
    p = subprocess.Popen([sys.executable, '-c', child], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        p.communicate(timeout=3)
        blocked = False
    except subprocess.TimeoutExpired:
        blocked = True
    lock.unlink(missing_ok=True)
    stdout, stderr = p.communicate(timeout=15)
    assert p.returncode == 0, stderr
    out['11_queue'] = {'normal_ok': True,'stale_lock_blocks_over_3_seconds':blocked,'resumes_after_removing_test_lock':p.returncode==0}
child = subprocess.run([sys.executable,'-c',"import sys,requests; print(sys.prefix); print(requests.__version__)"],capture_output=True,text=True,check=True)
out['9_worker'] = {'same_prefix':child.stdout.splitlines()[0]==sys.prefix,'requests_import_ok':True}
for name in ['numpy','pyarrow','faiss','Stemmer','fontTools']:
    p=subprocess.run([sys.executable,'-c',f'import {name}; print(getattr({name}, "__version__", "import-ok"))'],capture_output=True,text=True)
    out['dependency_'+name]={'import_ok':p.returncode==0, 'version':p.stdout.strip() if p.returncode==0 else 'not installed/import failed'}
Path('/tmp/report-retest-1026/probes.json').write_text(json.dumps(out,ensure_ascii=False,indent=2))
print(json.dumps(out,ensure_ascii=False,indent=2))
