"""Controlled OpenAI transport fixture for organizer product-path acceptance."""
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

state = {'delay': 0, 'calls': 0}

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(json.dumps(state).encode())

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        if self.path == '/control':
            state['delay'] = body.get('delay', 0)
            self.send_response(200); self.end_headers(); self.wfile.write(b'{}'); return
        state['calls'] += 1
        time.sleep(state['delay'])
        text = '\n'.join(str(message.get('content', '')) for message in body['messages'])
        if '严格按response_schema只输出JSON。输入：\n' in text:
            payload = json.loads(text.rsplit('严格按response_schema只输出JSON。输入：\n', 1)[1])
            if payload.get('mode') == 'scope_audit':
                result = {'keep': [x['id'] for x in payload['items']], 'reject': []}
            else:
                groups = payload.get('groups', [])
                target = next((x['id'] for x in groups if x.get('kind') == 'candidate'), None)
                operations = [] if target else [{'op': 'create', 'id': 'cand_work', 'name': '工作任务', 'scope': '处理日常邮件和工作任务'}]
                result = {'candidate_operations': operations, 'assignments': [{'id': x['id'], 'group_id': target or 'cand_work'} for x in payload['conversations']]}
        else:
            result = {'title': '处理工作邮件', 'initial_intent_summary': '处理日常邮件和工作任务', 'intent_status': 'ready', 'missing_context': []}
        content = json.dumps(result, ensure_ascii=False)
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream' if body.get('stream') else 'application/json')
        self.end_headers()
        try:
            if body.get('stream'):
                for delta, finish in [({'role': 'assistant'}, None), ({'content': content}, None), ({}, 'stop')]:
                    event = {'id': 'fixture', 'object': 'chat.completion.chunk', 'created': int(time.time()), 'model': body.get('model'), 'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}]}
                    self.wfile.write(('data: ' + json.dumps(event) + '\n\n').encode()); self.wfile.flush()
                self.wfile.write(b'data: [DONE]\n\n')
            else:
                self.wfile.write(json.dumps({'id': 'fixture', 'object': 'chat.completion', 'model': body.get('model'), 'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': content}, 'finish_reason': 'stop'}], 'usage': {'prompt_tokens': 10, 'completion_tokens': 10, 'total_tokens': 20}}).encode())
        except (BrokenPipeError, ConnectionResetError):
            pass

if __name__ == '__main__':
    ThreadingHTTPServer(('0.0.0.0', 19098), Handler).serve_forever()
