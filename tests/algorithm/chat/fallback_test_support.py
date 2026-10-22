import copy
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import lazyllm
from lazyllm.tools.agent import fc_register
from lazymind.chat.engine.agent_runtime import AgentExecutionOptions
from lazymind.chat.engine.agent_runtime.models import ModelCandidate
from lazymind.chat.engine.agent_runtime.model_fallback import FallbackLLM, create_fallback_state


EFFECT_RECEIPTS = []


@fc_register(host_file='NONE')
def record_fallback_effect(value: str) -> str:
    """Record one isolated effect.

    Args:
        value: Value to record.
    """
    EFFECT_RECEIPTS.append(value)
    return 'effect-receipt: ' + value


class ScriptedProvider:
    def __init__(self):
        self.requests = []
        self.script = {}
        self.on_request = None
        provider = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_POST(self):
                request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                provider.requests.append(copy.deepcopy(request))
                override = (provider.on_request(copy.deepcopy(request))
                            if provider.on_request is not None else None)
                model = request['model']
                count = sum(item['model'] == model for item in provider.requests)
                script = provider.script.get(model)
                step = override if override is not None else (
                    script[min(count - 1, len(script) - 1)] if script else None)
                if (step and 'error' in step) or (not step and model == 'model-a' and count > 1):
                    body = json.dumps({'error': {
                        'message': 'controlled model unavailable',
                        'code': (step or {}).get('error', 'model_not_found'),
                        'type': (step or {}).get('type', 'invalid_request_error'),
                    }}).encode()
                    status, content_type = (step or {}).get('status', 404), 'application/json'
                else:
                    if step:
                        message, finish = step['message'], step.get('finish', 'stop')
                    elif model == 'model-a':
                        message, finish = {'role': 'assistant', 'content': '', 'tool_calls': [{
                            'id': 'effect-call', 'index': 0, 'type': 'function',
                            'function': {'name': 'record_fallback_effect',
                                         'arguments': '{"value": "once"}'},
                        }]}, 'tool_calls'
                    else:
                        has_receipt = any(item.get('role') == 'tool'
                                          and 'effect-receipt: once' in item.get('content', '')
                                          for item in request['messages'])
                        content = 'completed via B' if has_receipt else 'missing tool receipt'
                        message, finish = {'role': 'assistant', 'content': content}, 'stop'
                    if self.path.rstrip('/').endswith('/messages'):
                        content = [{'type': 'text', 'text': message.get('content', '')}]
                        if request.get('stream'):
                            frames = [
                                {'type': 'message_start', 'message': {'usage': {'input_tokens': 11}}},
                                {'type': 'content_block_delta', 'index': 0,
                                 'delta': {'type': 'text_delta', 'text': message.get('content', '')}},
                                {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn'},
                                 'usage': {'output_tokens': 7},
                                 'padding': 'x' * 1024 if step and step.get('disconnect') else ''},
                                {'type': 'message_stop'},
                            ]
                            body = ''.join('event: ' + item['type'] + '\ndata: ' + json.dumps(item) + '\n\n'
                                           for item in frames).encode()
                            content_type = 'text/event-stream'
                        else:
                            body = json.dumps({'type': 'message', 'content': content, 'stop_reason': 'end_turn',
                                               'usage': {'input_tokens': 11, 'output_tokens': 7}}).encode()
                            content_type = 'application/json'
                    elif request.get('stream'):
                        chunk = {'id': 'controlled', 'model': model, 'choices': [{
                            'index': 0, 'delta': message, 'finish_reason': finish,
                        }]}
                        if step and step.get('usage'):
                            chunk['usage'] = step['usage']
                        frames = step.get('frames') if step else None
                        body = ''.join('data: ' + json.dumps(frame) + '\n\n'
                                       for frame in (frames or [chunk]))
                        if not (step and step.get('disconnect')):
                            body += 'data: [DONE]\n\n'
                        body = body.encode()
                        content_type = 'text/event-stream'
                    else:
                        body = json.dumps({'id': 'controlled', 'model': model, 'choices': [{
                            'index': 0, 'message': message, 'finish_reason': finish,
                        }]}).encode()
                        content_type = 'application/json'
                    status = 200
                self.send_response(status)
                self.send_header('Content-Type', content_type)
                self.send_header('Content-Length', str(len(body) + (100 if step and step.get('disconnect') else 0)))
                self.end_headers()
                self.wfile.write(body)

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=lambda: self.server.serve_forever(poll_interval=0.01), daemon=True)
        self.thread.start()
        self.url = f'http://127.0.0.1:{self.server.server_port}/v1/'

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def config(self, model):
        return {'llm': {'source': 'openai', 'model': model, 'base_url': self.url,
                        'api_key': 'fake-fallback-key', 'max_input_tokens': 32768}}


def candidate(provider, model, *, source='openai', window=32768):
    config = provider.config(model)
    config['llm']['source'] = source
    return ModelCandidate('own:' + model, 'own', model, provider_group_id=model,
                          credential_revision=1, max_input_tokens=window, llm_config=config)


def wrapper(current, alternatives, *, initial=None):
    role = current.llm_config['llm']
    raw = lazyllm.OnlineChatModule(
        source=role['source'], model=role['model'], type='llm',
        url=role['base_url'], api_key=role['api_key'], stream=False)
    options = AgentExecutionOptions(
        llm_config=current.llm_config, model_candidates=(current, *alternatives),
        current_model_candidate_id=current.candidate_id, initial_model_switch=initial)
    return FallbackLLM(create_fallback_state(raw, options))


def succeed(provider, model):
    provider.script[model] = [{'message': {'role': 'assistant', 'content': 'done:' + model}}]


def fail(provider, model):
    provider.script[model] = [{'error': 'model_not_found', 'status': 404, 'type': 'invalid_request_error'}]


def switches(events):
    return [item['runtime_event']['data'] for item in events
            if (item.get('runtime_event') or {}).get('type') == 'model_switched']


VALID_SUMMARY = '\n'.join(
    [
        '## Current task',
        'Ship summary compression.',
        '## Key constraints',
        'Do not delete original history.',
        '## Progress and decisions',
        'Stage1 prune done; Stage2 pending.',
        '## Important files and tool results',
        'Touched summarizer.py; run_script exit 0.',
        '## Pending work',
        'Wire tests and lint.',
        '## Active skills',
        '[]',
        '## Artifact coordinates',
        '[]',
        '## Citation map',
        '[]',
        '## Spill paths',
        '[]',
    ]
)


def sse_frame(delta, *, finish=None, usage=None, padding=''):
    result = {'id': 'controlled', 'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}]}
    if usage is not None:
        result['usage'] = usage
    if padding:
        result['padding'] = padding
    return result


def broken_output():
    return {'message': {'role': 'assistant', 'content': ''}, 'disconnect': True, 'frames': [
        sse_frame({'reasoning_content': 'BAD-THOUGHT ' * 60},
                  usage={'prompt_tokens': 10, 'completion_tokens': 3}),
        sse_frame({'content': 'BAD-BODY [[1.1]]\n:::export{title="bad draft" filename="bad.md"}\nBAD-DRAFT\n<thi'}),
        sse_frame({}, padding='x' * 4096),
    ]}


def request_tokens_for(llm, input, history, files, kwargs):
    from lazymind.chat.engine.agent_runtime.context_estimator import estimate_tokens
    from lazymind.chat.engine.agent_runtime.message_fields import estimate_message_tokens
    return llm.prepare_request(input, llm_chat_history=history, lazyllm_files=files, **kwargs).estimate_tokens(
        estimate_tokens, estimate_message_tokens)
