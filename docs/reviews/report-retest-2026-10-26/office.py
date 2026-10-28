import importlib.util,tempfile,os,shutil,json,time,shlex
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
from docx import Document
spec=importlib.util.spec_from_file_location('office_probe','backend/office-convert-service/main.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
with tempfile.TemporaryDirectory() as td:
 root=Path(td);exe=shutil.which('soffice');(root/'libreoffice').write_text('#!/bin/sh\nexec '+shlex.quote(exe)+' \"$@\"\n');(root/'libreoffice').chmod(0o755)
 os.environ['PATH']=str(root)+os.pathsep+os.environ['PATH']
 src=root/'中文 测试.docx';d=Document();d.add_paragraph('Report conversion probe 123');d.save(src)
 def run(i):
  dst=root/f'out-{i}.pdf';m._run_libreoffice_convert(src,dst);return dst.stat().st_size
 start=time.monotonic()
 with ThreadPoolExecutor(max_workers=2) as pool:sizes=list(pool.map(run,[1,2]))
 result={'concurrent_conversions':2,'pdf_sizes':sizes,'seconds':round(time.monotonic()-start,2),'boundary':'temporary libreoffice alias to installed soffice; not default deployment; not Windows'}
 Path('/tmp/report-retest-1026/office.json').write_text(json.dumps(result,indent=2));print(result)
