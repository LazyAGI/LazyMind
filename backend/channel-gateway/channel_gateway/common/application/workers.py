from __future__ import annotations

import logging
import threading
import uuid
from dataclasses import replace
from typing import Callable

from channel_gateway.common.application.messages import ChannelMessageService
from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    NativeTargetError,
    OutboundMessage,
)
from channel_gateway.common.errors import RetryableProviderSideEffectError
from channel_gateway.common.ports.messaging import MessageWorkerRepository
from channel_gateway.common.ports.messaging import (
    DeliveryProviderRegistry,
    OutboxWorkRepository,
)


_logger = logging.getLogger(__name__)
_INBOUND_LEASE_SECONDS = 120
_OUTBOUND_LEASE_SECONDS = 120
# Core currently has no durable idempotency contract. Retrying a returned
# application error could duplicate a completed turn, so only lease recovery
# after a process crash can re-enter an inbound message.
_MAX_INBOUND_ATTEMPTS = 1
_MAX_PROVIDER_SIDE_EFFECT_ATTEMPTS = 5
_MAX_OUTBOUND_ATTEMPTS = 5
_WELCOME_MESSAGE = """我是 LazyMind，你的个人 AI 助手。这里与 LazyMind 网页端使用同一账号、普通会话和历史记录。

你可以直接用自然语言：
1. “帮我创建一个新会话，并整理今天的周报”
2. “列出我的历史会话”或“切到第 2 个会话”
3. “这轮使用 AI学习资料 知识库”
4. “查看当前可用的知识库、Skill 和工具”
5. “总结当前会话的进展并给出下一步”

直接发送消息即可继续。"""


class LeaseLostError(RuntimeError):
    pass


class _LeaseHeartbeat:
    def __init__(
        self,
        renew: Callable[[], bool],
        *,
        name: str,
        interval_seconds: int = 30,
    ):
        self._renew = renew
        self._interval_seconds = interval_seconds
        self._stop = threading.Event()
        self._lost = threading.Event()
        self._thread = threading.Thread(
            target=self._run,
            name=name,
            daemon=True,
        )

    def __enter__(self):
        self._thread.start()
        return self

    def __exit__(self, _exc_type, _exc_value, _traceback):
        self._stop.set()
        self._thread.join(timeout=1.0)

    def ensure_owned(self) -> None:
        if self._lost.is_set():
            raise LeaseLostError('Channel work lease was lost')

    def _run(self) -> None:
        while not self._stop.wait(self._interval_seconds):
            try:
                if not self._renew():
                    self._lost.set()
                    return
            except Exception:
                _logger.exception('channel_lease_renewal_failed')
                self._lost.set()
                return


class MessageWorker:
    def __init__(
        self,
        *,
        store: MessageWorkerRepository,
        messages: ChannelMessageService,
        worker_count: int = 2,
    ):
        self._store = store
        self._messages = messages
        self._worker_count = max(1, worker_count)
        self._stop = threading.Event()
        self._threads: list[threading.Thread] = []

    def start(self) -> None:
        if self._threads:
            return
        for index in range(self._worker_count):
            thread = threading.Thread(
                target=self._run,
                args=(f'message_{uuid.uuid4().hex}',),
                name=f'channel-message-{index}',
                daemon=True,
            )
            self._threads.append(thread)
            thread.start()

    def stop(self) -> None:
        self._stop.set()
        for thread in self._threads:
            thread.join(timeout=2.0)
        self._threads.clear()

    def _run(self, claim_owner: str) -> None:
        while not self._stop.is_set():
            try:
                inbound = self._store.claim_next_inbound(
                    claim_owner,
                    lease_seconds=_INBOUND_LEASE_SECONDS,
                )
                if inbound is None:
                    self._stop.wait(0.5)
                    continue
                self._process(inbound, claim_owner)
            except Exception:
                _logger.exception('channel_message_worker_failed')
                self._stop.wait(1.0)

    def _process(self, inbound: ClaimedInbound, claim_owner: str) -> None:
        fallback = OutboundMessage(
            provider=inbound.provider,
            account_id=inbound.account_id,
            order_key=inbound.order_key,
            recipient_id=inbound.recipient_id,
            provider_context=inbound.provider_context,
            text='LazyMind 暂时无法处理这条消息，请稍后重试。',
            intent_kind='failed',
        )
        try:
            with _LeaseHeartbeat(
                lambda: self._store.renew_inbound_lease(
                    inbound.inbox_id,
                    claim_owner,
                    lease_seconds=_INBOUND_LEASE_SECONDS,
                ),
                name='channel-inbound-lease',
            ) as lease:
                lease.ensure_owned()
                result = self._messages.process(
                    provider=inbound.provider,
                    account_id=inbound.account_id,
                    external_address_hash=inbound.external_address_hash,
                    owner_user_id=inbound.owner_user_id,
                    text=inbound.text,
                    request_id=f'channel_{inbound.message_key[:24]}',
                    surface=str(
                        inbound.provider_context.get('surface')
                        or 'direct'
                    ),
                    provider_context=inbound.provider_context,
                )
                lease.ensure_owned()
                outbound = [
                    replace(
                        fallback,
                        order_key=(
                            result.target.external_address_hash
                            if result.target
                            else fallback.order_key
                        ),
                        recipient_id=(
                            result.target.recipient_id
                            if result.target
                            else fallback.recipient_id
                        ),
                        provider_context=(
                            result.target.provider_context
                            if result.target
                            else fallback.provider_context
                        ),
                        text=result.text,
                        intent_kind=result.intent_kind.value,
                        metadata={
                            'core_events': list(result.core_events),
                            'sources': list(result.sources),
                            'presentations': [
                                presentation.to_dict()
                                for presentation
                                in result.presentations
                            ],
                            'suppress_text_when_presented': (
                                result.suppress_text_when_presented
                            ),
                        },
                    )
                ]
                if self._store.welcome_pending(inbound.account_id):
                    outbound.append(
                        replace(
                            outbound[0],
                            text=_WELCOME_MESSAGE,
                            intent_kind='welcome',
                            purpose='welcome',
                            metadata={},
                        )
                    )
                if not self._store.complete_inbound(
                    inbound.inbox_id,
                    claim_owner,
                    outbound,
                    native_operation=(
                        self._native_operation(
                            inbound,
                            result,
                        )
                    ),
                ):
                    _logger.warning(
                        'channel_inbound_completion_fenced inbox_id=%s',
                        inbound.inbox_id,
                    )
                    return
            _logger.info(
                'channel_inbound_completed inbox_id=%s intent=%s',
                inbound.inbox_id,
                result.intent_kind.value,
            )
        except LeaseLostError:
            _logger.warning(
                'channel_inbound_lease_lost inbox_id=%s',
                inbound.inbox_id,
            )
        except NativeTargetError as exc:
            target = exc.target
            targeted_fallback = replace(
                fallback,
                order_key=target.external_address_hash,
                recipient_id=target.recipient_id,
                provider_context=target.provider_context,
            )
            _logger.exception(
                'channel_inbound_processing_failed_after_target '
                'inbox_id=%s',
                inbound.inbox_id,
            )
            self._store.record_inbound_failure(
                inbound.inbox_id,
                claim_owner,
                error=exc.__class__.__name__,
                fallback=targeted_fallback,
                max_attempts=_MAX_INBOUND_ATTEMPTS,
                native_operation={
                    'account_id': inbound.account_id,
                    'provider': inbound.provider,
                    'operation_id': target.operation_id,
                },
            )
        except RetryableProviderSideEffectError as exc:
            _logger.warning(
                'channel_provider_side_effect_uncertain '
                'inbox_id=%s attempt=%s',
                inbound.inbox_id,
                inbound.attempt_count,
            )
            self._store.record_inbound_failure(
                inbound.inbox_id,
                claim_owner,
                error=exc.__class__.__name__,
                fallback=fallback,
                max_attempts=_MAX_PROVIDER_SIDE_EFFECT_ATTEMPTS,
                native_operation={
                    'account_id': inbound.account_id,
                    'provider': inbound.provider,
                    'operation_id': (
                        f'channel_{inbound.message_key[:24]}'
                    ),
                },
            )
        except Exception as exc:
            _logger.exception(
                'channel_inbound_processing_failed inbox_id=%s attempt=%s',
                inbound.inbox_id,
                inbound.attempt_count,
            )
            self._store.record_inbound_failure(
                inbound.inbox_id,
                claim_owner,
                error=exc.__class__.__name__,
                fallback=fallback,
                max_attempts=_MAX_INBOUND_ATTEMPTS,
                native_operation={
                    'account_id': inbound.account_id,
                    'provider': inbound.provider,
                    'operation_id': (
                        f'channel_{inbound.message_key[:24]}'
                    ),
                },
            )

    @staticmethod
    def _native_operation(
        inbound: ClaimedInbound,
        result,
    ) -> dict | None:
        target = result.target
        if target is None or not target.operation_id:
            return None
        return {
            'account_id': inbound.account_id,
            'provider': inbound.provider,
            'operation_id': target.operation_id,
            'result': {
                'text': result.text,
                'intent_kind': result.intent_kind.value,
                'core_events': list(result.core_events),
                'sources': list(result.sources),
                'presentations': [
                    presentation.to_dict()
                    for presentation in result.presentations
                ],
                'suppress_text_when_presented': (
                    result.suppress_text_when_presented
                ),
            },
        }


class DeliveryWorker:
    def __init__(
        self,
        *,
        store: OutboxWorkRepository,
        providers: DeliveryProviderRegistry,
        worker_count: int = 2,
    ):
        self._store = store
        self._providers = providers
        self._worker_count = max(1, worker_count)
        self._stop = threading.Event()
        self._threads: list[threading.Thread] = []

    def start(self) -> None:
        if self._threads:
            return
        for index in range(self._worker_count):
            thread = threading.Thread(
                target=self._run,
                args=(f'delivery_{uuid.uuid4().hex}',),
                name=f'channel-delivery-{index}',
                daemon=True,
            )
            self._threads.append(thread)
            thread.start()

    def stop(self) -> None:
        self._stop.set()
        for thread in self._threads:
            thread.join(timeout=2.0)
        self._threads.clear()

    def _run(self, claim_owner: str) -> None:
        while not self._stop.is_set():
            outbound = None
            try:
                outbound = self._store.claim_next_outbound(
                    claim_owner,
                    lease_seconds=_OUTBOUND_LEASE_SECONDS,
                )
                if outbound is None:
                    self._stop.wait(0.5)
                    continue
                provider = self._providers.delivery(outbound.provider)
                if provider is None:
                    raise RuntimeError(
                        f'No delivery provider for {outbound.provider}'
                    )
                with _LeaseHeartbeat(
                    lambda outbound=outbound:
                    self._store.renew_outbound_lease(
                        outbound.outbox_id,
                        claim_owner,
                        lease_seconds=_OUTBOUND_LEASE_SECONDS,
                    ),
                    name='channel-outbound-lease',
                ) as lease:
                    lease.ensure_owned()
                    parts = outbound.rendered_parts
                    if not parts:
                        parts = provider.render(outbound)
                        lease.ensure_owned()
                        if not self._store.save_rendered_parts(
                            outbound.outbox_id,
                            claim_owner,
                            parts,
                        ):
                            raise RuntimeError(
                                'Cannot persist rendered channel parts'
                            )
                        outbound = replace(outbound, rendered_parts=parts)
                    self._deliver(
                        outbound,
                        provider,
                        claim_owner,
                        lease,
                    )
                    lease.ensure_owned()
                    if not self._store.complete_outbound(
                        outbound.outbox_id,
                        claim_owner,
                    ):
                        raise RuntimeError(
                            'Channel outbox completion was fenced'
                        )
            except LeaseLostError:
                if outbound is not None:
                    _logger.warning(
                        'channel_outbound_lease_lost outbox_id=%s',
                        outbound.outbox_id,
                    )
            except Exception as exc:
                if outbound is not None:
                    _logger.exception(
                        'channel_outbound_failed outbox_id=%s attempt=%s',
                        outbound.outbox_id,
                        outbound.attempt_count,
                    )
                    self._store.record_outbound_failure(
                        outbound.outbox_id,
                        claim_owner,
                        error=exc.__class__.__name__,
                        max_attempts=_MAX_OUTBOUND_ATTEMPTS,
                    )
                else:
                    _logger.exception('channel_delivery_worker_failed')
                self._stop.wait(1.0)

    def _deliver(
        self,
        outbound,
        provider,
        claim_owner: str,
        lease: _LeaseHeartbeat,
    ) -> None:
        for part_index in range(
            outbound.next_part_index,
            len(outbound.rendered_parts),
        ):
            lease.ensure_owned()
            part = outbound.rendered_parts[part_index]
            saved_state = dict(
                outbound.provider_state.get(str(part_index)) or {}
            )
            prepared_state = provider.prepare_part(
                outbound,
                part,
                part_index=part_index,
                saved_state=saved_state,
            )
            if prepared_state != saved_state:
                if not self._store.save_outbound_part_state(
                    outbound.outbox_id,
                    claim_owner,
                    part_index,
                    prepared_state,
                ):
                    raise RuntimeError('Cannot persist provider delivery state')
            lease.ensure_owned()
            provider.send_part(
                outbound,
                part,
                part_index=part_index,
                idempotency_key=str(
                    uuid.uuid5(
                        uuid.NAMESPACE_URL,
                        f'lazymind:{outbound.outbox_id}:part:{part_index}',
                    )
                ),
                saved_state=prepared_state,
            )
            lease.ensure_owned()
            if not self._store.advance_outbound(
                outbound.outbox_id,
                claim_owner,
                part_index + 1,
            ):
                raise RuntimeError('Cannot advance channel outbox')
