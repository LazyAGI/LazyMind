"""Opt-in real-model acceptance, run in the chat container with OPENING_MODEL_URL.

python /app/algorithm/tests/chat/acceptance/conversation_opening.py
Writes JSON evidence to OPENING_REPORT; never changes the user's model selections.
"""
import json
import os
import time
from pathlib import Path

from fastapi import FastAPI
from fastapi.testclient import TestClient
from lazymind.chat.api.llm_task_routes import router

app = FastAPI()
app.include_router(router)
client = TestClient(app)
cases = json.loads(Path(__file__).with_name('conversation_opening_cases.json').read_text())
filler = '\n'.join(f'背景资料记录 {i}：系统提供文档上传、查询、分类与历史记录功能。' for i in range(1200))
goal = '\n本次唯一任务：排查静海项目的 PostgreSQL 连接池泄漏，输出排查方案。\n'
for position in ['start', 'middle', 'end']:
    split = {'start': 0, 'middle': len(filler)//2, 'end': len(filler)}[position]
    cases.append([f'long_{position}', 'ready', [['user', filler[:split]+goal+filler[split:]]], []])
config = {'llm': {'source': 'openai', 'model': os.environ.get('OPENING_MODEL_NAME', 'Qwen/Qwen3.8-Flash-Next'),
                  'base_url': os.environ['OPENING_MODEL_URL'], 'skip_auth': True, 'max_input_tokens': 262144}}
report = []
for name, expected, messages, attachments in cases:
    started = time.monotonic()
    response = client.post('/api/chat/llm-task:run', json={
        'mode': 'llm', 'task_type': 'conversation.describe_opening', 'llm_config': config,
        'input': {'messages': [{'role': role, 'content': content} for role, content in messages],
                  'data': {'attachments': attachments}},
        'options': {'timeout_seconds': 60},
    })
    result = response.json()
    output = result.get('output', {})
    actual = output.get('intent_status') or result.get('error_code')
    passed = response.status_code == 200 and actual == expected
    if name.startswith('long_'):
        passed = passed and 'PostgreSQL' in json.dumps(output) and '连接池' in json.dumps(output,ensure_ascii=False)
        passed = passed and result['usage']['estimated_input_tokens'] > 4096
    row = {'case': name, 'passed': passed, 'elapsed_seconds': round(time.monotonic()-started,2), 'response': result}
    report.append(row)
    Path(os.environ.get('OPENING_REPORT','/tmp/conversation-opening-report.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2))
    print(json.dumps(row,ensure_ascii=False),flush=True)
assert all(row['passed'] for row in report), 'Inspect OPENING_REPORT for failed cases'
