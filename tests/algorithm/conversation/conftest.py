"""A local controlled provider shared by API and worker acceptance tests."""
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest


@pytest.fixture
def provider():
    calls = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            calls.append(body)
            text = '\n'.join(str(message.get('content', '')) for message in body['messages'])
            if '严格按response_schema只输出JSON。输入：\n' in text:
                payload = json.loads(text.rsplit('严格按response_schema只输出JSON。输入：\n', 1)[1])
                if payload.get('mode') == 'scope_audit':
                    result = {'keep': [item['id'] for item in payload['items']], 'reject': []}
                else:
                    result = {'candidate_operations': [],
                              'assignments': [{'id': item['id'], 'group_id': 'free'}
                                              for item in payload['conversations']]}
            else:
                result = {'title': '处理工作邮件', 'initial_intent_summary': '处理日常邮件和工作任务',
                          'intent_status': 'ready', 'missing_context': []}
                if '批量处理彼此独立的会话' in text:
                    inputs = json.loads(text.rsplit('开场资料：\n', 1)[1])
                    result = {'items': [{'id': item['id'], **result} for item in inputs]}
            content = json.dumps(result, ensure_ascii=False)
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream' if body.get('stream') else 'application/json')
            self.end_headers()
            try:
                if body.get('stream'):
                    for delta, finish in [({'role': 'assistant'}, None), ({'content': content}, None), ({}, 'stop')]:
                        event = {
                            'id': 'fixture', 'object': 'chat.completion.chunk', 'created': int(time.time()),
                            'model': body.get('model'),
                            'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}],
                        }
                        self.wfile.write(('data: ' + json.dumps(event) + '\n\n').encode())
                        self.wfile.flush()
                    self.wfile.write(b'data: [DONE]\n\n')
                else:
                    self.wfile.write(json.dumps({
                        'id': 'fixture', 'object': 'chat.completion', 'model': body.get('model'),
                        'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': content},
                                     'finish_reason': 'stop'}],
                        'usage': {'prompt_tokens': 10, 'completion_tokens': 10, 'total_tokens': 20},
                    }).encode())
            except (BrokenPipeError, ConnectionResetError):
                pass

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield {'llm': {'source': 'openai', 'model': 'controlled-conversation',
                       'base_url': f'http://127.0.0.1:{server.server_port}/v1', 'skip_auth': True}}, calls
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
