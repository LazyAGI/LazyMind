import tempfile,json,time,types
from pathlib import Path
from lazyllm.module.llms.onlinemodule.dynamic_router import _DynamicSourceRouterMixin
from lazymind.chat.engine.chat_excel_extractor import extract_spreadsheet
class FakeBucket:
 _URL_ALIASES = _DynamicSourceRouterMixin._URL_ALIASES
 _MODEL_ALIASES = _DynamicSourceRouterMixin._MODEL_ALIASES
 @classmethod
 def _get_dynamic_bucket(cls): return {'url':'http://127.0.0.1:12345/v1/','model':'offline-probe'}
out={'36_dynamic':_DynamicSourceRouterMixin._merge_dynamic_forward_kwargs(FakeBucket(),{})}
with tempfile.TemporaryDirectory() as td:
 p=Path(td)/'large.csv'
 line='上海,'+'x'*1024+'\n'
 with p.open('w',encoding='utf-8') as f:
  f.write('city,value\n')
  for i in range(83000):f.write(line)
 start=time.monotonic();r=extract_spreadsheet(str(p))
 out['30_large_csv']={'bytes':p.stat().st_size,'seconds':round(time.monotonic()-start,3),'inline':r.inline,'total_rows':r.total_rows,'preview_chars':len(r.text)}
 assert not r.inline
Path('/tmp/report-retest-1026/extra.json').write_text(json.dumps(out,ensure_ascii=False,indent=2))
print(json.dumps(out,ensure_ascii=False,indent=2))
