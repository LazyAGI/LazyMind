"""Per-request F4 client controls, never provider API generation parameters."""
from dataclasses import dataclass
from functools import partial
import os
import time

from .code_response import _reasoning_text
from ..analysis import trace as diagnostics


@dataclass(frozen=True)
class Policy:
    connect: int = 15
    read: int = 600
    attempts: int = 2


def configuration():
    values = {}
    for name, default, upper in [('CONNECT_TIMEOUT', 15, 120), ('READ_TIMEOUT', 600, 3600),
                                  ('MAX_ATTEMPTS', 2, 3)]:
        try:
            value = int(os.environ.get('LAZYMIND_PAPER_MODEL_' + name, str(default)))
            if not 1 <= value <= upper:
                raise ValueError
        except ValueError as exc:
            raise ValueError('F4 模型客户端配置无效: ' + name) from exc
        values[name] = value
    return Policy(values['CONNECT_TIMEOUT'], values['READ_TIMEOUT'], values['MAX_ATTEMPTS'])


def _stream_parser(parser_type, **settings):
    emit = settings.get('emit_message')
    if emit is not None:
        def emit_channels(message):
            choices = message.get('choices')
            if not isinstance(choices, list):
                return emit(message)
            preview = []
            for choice in choices:
                if isinstance(choice, dict):
                    key = 'message' if 'message' in choice else 'delta'
                    delta = choice.get(key)
                    if isinstance(delta, dict) and (text := _reasoning_text(delta)):
                        choice = dict(choice, **{key: dict(delta, reasoning_content=text)})
                preview.append(choice)
            # Normalize only the preview copy; the SDK retains its native frames.
            emit(dict(message, choices=preview))
        settings['emit_message'] = emit_channels
    return parser_type(**settings)


def configure(client, policy, *, structured=False):
    from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule
    from lazyllm.module.llms.onlinemodule.base.onlineChatModuleBase import LazyLLMOnlineChatModuleBase

    supported = isinstance(client, (OnlineChatModule, LazyLLMOnlineChatModuleBase))
    if supported:
        if structured:
            from lazyllm import ChatPrompter
            client.prompt(ChatPrompter(enable_system=False))
        # AutoModel creates this client for this request. Dynamic routers pass
        # their constructor timeout to each supplier when it is first created.
        # Passing timeout in forward kwargs would also put it in the API body.
        client._timeout = (policy.connect, policy.read)
        # Keep the provider's content/reasoning fields separate for this F4 call.
        # The SDK's default formatter concatenates them into <think> text.
        client.formatter(lambda message: message)
        if isinstance(client, LazyLLMOnlineChatModuleBase):
            supplier = client
        else:
            from lazyllm import globals
            with globals.stack_enter(client.identities):
                supplier = client._get_supplier()
        # A cached supplier keeps the formatter and timeout from its creation.
        # Configure the actual F4 caller as well as the outer router.
        if structured:
            supplier.prompt(client._prompt)
        supplier.formatter(client._formatter)
        supplier._timeout = client._timeout
        # Only this F4 client uses the preview callback; provider parsing stays intact.
        supplier.RESPONSE_PARSER_CLASS = partial(_stream_parser, supplier.RESPONSE_PARSER_CLASS)
        if supplier.PROVIDER_NAME == 'minimax':
            supplier._model_optional_params['reasoning_split'] = True
    return supported


def retry_connection(exc):
    """Only reconnect before a response; never repeat a long read timeout."""
    terminal = getattr(exc, 'terminal', None)
    failure = getattr(terminal, 'failure', None)
    if (failure is None or getattr(failure, 'response_started', True)
            or getattr(terminal, 'has_semantic_output', True)):
        return False
    if getattr(getattr(failure, 'origin', None), 'value', None) != 'transport':
        return False
    names = {type(item).__name__ for item in diagnostics.exception_chain(exc)}
    if names & {'ReadTimeout', 'ReadTimeoutError', 'Timeout', 'SSLError', 'ProxyError'}:
        return False
    return bool(names & {'ConnectTimeout', 'ConnectTimeoutError', 'ConnectionError', 'NewConnectionError',
                         'ConnectionResetError', 'ConnectionAbortedError', 'RemoteDisconnected'})


def call(client, prompt, options, policy, supported):
    attempts = policy.attempts if supported else 1
    for attempt in range(1, attempts + 1):
        try:
            with diagnostics.stage('model_transport_attempt', attempt=attempt, controlled=supported,
                                   connect_timeout_seconds=policy.connect if supported else None,
                                   read_timeout_seconds=policy.read if supported else None,
                                   sdk_max_attempts=1 if supported else None):
                # max_retries is consumed by the common SDK forward signature.
                # The application records each reconnect instead of hiding five
                # long requests inside a single code-generation attempt.
                controls = {'max_retries': 1} if supported else {}
                return client(prompt, **options, **controls)
        except Exception as exc:
            if not supported or attempt == attempts or not retry_connection(exc):
                raise
            time.sleep(1)
