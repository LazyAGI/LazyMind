"""Internal, model-free reading of a single directory email."""
from __future__ import annotations

import asyncio
import re

from fastapi import APIRouter, Depends
from pydantic import BaseModel, SecretStr

from lazymind.chat.api.agent_control_routes import require_env_input_service
from lazymind.chat.engine.tools.mail import _IMAPBackend

router = APIRouter()


class MailReadRequest(BaseModel):
    provider: str
    email: str
    secret: SecretStr
    message_ref: str
    message_id: str


def read_mail(req: MailReadRequest) -> dict:
    try:
        backend = _IMAPBackend({'provider': req.provider, 'email': req.email,
                                'secret': req.secret.get_secret_value()})
        mail = backend.read_preview(req.message_ref, req.message_id)
        return {'mail': {key: mail[key] for key in (
            'subject', 'from', 'to', 'cc', 'date', 'mailbox', 'body', 'attachments', 'truncated')}}
    except Exception as exc:
        # Never return provider exceptions: they can contain account/server details.
        reason = str(exc).lower()
        if isinstance(exc, TimeoutError) or 'timed out' in reason:
            code = 'mail_timeout'
        elif 'authorization' in reason or 'authentication' in reason:
            code = 'mail_authorization_failed'
        elif 'not found' in reason or reason == 'mail_location_changed':
            code = 'mail_location_changed'
        else:
            code = 'mail_read_failed'
        return {'error_code': code}


@router.post('/api/mail/read', dependencies=[Depends(require_env_input_service)])
async def read_directory_mail(req: MailReadRequest) -> dict:
    if not re.fullmatch(r'.+::[1-9][0-9]*', req.message_ref) or not re.fullmatch(r'<[^<>\r\n]+>', req.message_id):
        return {'error_code': 'mail_locator_missing'}
    try:
        return await asyncio.wait_for(asyncio.to_thread(read_mail, req), timeout=30)
    except TimeoutError:
        return {'error_code': 'mail_timeout'}
