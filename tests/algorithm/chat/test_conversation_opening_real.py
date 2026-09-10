"""Real-model acceptance; set OPENING_MODEL_URL to opt in and OPENING_REPORT to save results."""
import json
import os
import time
from pathlib import Path

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from lazymind.chat.api.llm_task_routes import router

pytestmark = pytest.mark.skipif(not os.environ.get('OPENING_MODEL_URL'), reason='real model endpoint not configured')

cases = json.loads(Path(__file__).with_name('conversation_opening_cases.json').read_text())
filler = '\n'.join(f'背景资料记录 {i}：系统提供文档上传、查询、分类与历史记录功能。' for i in range(1200))
goal = '\n本次唯一任务：排查静海项目的 PostgreSQL 连接池泄漏，输出排查方案。\n'
for position in ['start', 'middle', 'end']:
    split = {'start': 0, 'middle': len(filler) // 2, 'end': len(filler)}[position]
    cases.append([f'long_{position}', 'ready', [['user', filler[:split] + goal + filler[split:]]], []])


@pytest.fixture(scope='module')
def client():
    app = FastAPI()
    app.include_router(router)
    with TestClient(app) as client:
        yield client


@pytest.fixture(scope='module')
def report():
    return []


@pytest.mark.parametrize('name,expected,messages,attachments', cases, ids=[case[0] for case in cases])
def test_opening_real_model(client, report, name, expected, messages, attachments):
    config = {'llm': {'source': 'openai', 'model': os.environ.get('OPENING_MODEL_NAME', 'Qwen/Qwen3.8-Flash-Next'),
                      'base_url': os.environ['OPENING_MODEL_URL'], 'skip_auth': True}}
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
    description = output.get('title', '') + output.get('initial_intent_summary', '')
    required = {
        'clear': ['LazyMind', '对话'],
        'clarify_after': ['LazyMind', '检索'],
        'general': ['倒排索引'],
        'attachment_ready': ['合同', '风险'],
        'unconfirmed_suggestion': ['LazyMind', '标题'],
        'answer_then_clarification': ['本地', '检索'],
        'data_instruction': ['销售'],
    }
    forbidden = {
        'unconfirmed_suggestion': ['Redis', 'Celery', '重构'],
        'answer_then_clarification': ['embedding', 'BM25', '重排序'],
        'data_instruction': ['hacked', '数据库改造'],
    }
    passed = passed and all(word in description for word in required.get(name, []))
    passed = passed and not any(word in description for word in forbidden.get(name, []))
    if name.startswith('long_'):
        passed = passed and 'PostgreSQL' in description and '连接池' in description
    row = {'case': name, 'passed': passed, 'elapsed_seconds': round(time.monotonic() - started, 2), 'response': result}
    report.append(row)
    if report_path := os.environ.get('OPENING_REPORT'):
        Path(report_path).write_text(json.dumps(report, ensure_ascii=False, indent=2))
    assert passed, row
