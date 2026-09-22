"""Bounded cloud document subprocesses, independent of Workflow execution."""

import asyncio
import json
import os
from pathlib import Path
import sys

from .reading import DocumentReadError

_slots = asyncio.Semaphore(4)
_WORKER = Path(__file__).with_name('read_worker.py')


async def run_document_operation(operation, request):
    if operation not in {'read', 'browse', 'search'}:
        raise DocumentReadError('INVALID_ARGUMENT', 'unsupported document operation')
    if _slots.locked():
        raise DocumentReadError('BUSY', 'document workers are busy; retry later')
    async with _slots:
        data = request.model_dump(mode='json')
        data['tool_config'] = {key: value.get_secret_value() for key, value in request.tool_config.items()}
        # Credentials travel through stdin only, never command arguments or a temporary file.
        startup = asyncio.create_task(asyncio.create_subprocess_exec(
            sys.executable, str(_WORKER), operation,
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
            env={**os.environ, 'PYTHONDONTWRITEBYTECODE': '1'},
        ))
        process = None
        try:
            process = await asyncio.shield(startup)
            stdout, _ = await process.communicate(json.dumps(data).encode())
            if process.returncode:
                raise DocumentReadError('PROVIDER_UNAVAILABLE', 'document worker exited without a result')
            try:
                result = json.loads(stdout)
            except ValueError:
                raise DocumentReadError('PROVIDER_UNAVAILABLE', 'invalid document worker response') from None
            if 'error' in result:
                raise DocumentReadError(result['error']['code'], result['error']['message'])
            return result['result']
        except OSError:
            raise DocumentReadError('PROVIDER_UNAVAILABLE', 'document worker could not run') from None
        finally:
            if process is None and not startup.done():
                process = await asyncio.shield(startup)
            elif process is None and not startup.cancelled() and startup.exception() is None:
                process = startup.result()
            if process is not None and process.returncode is None:
                try:
                    process.kill()
                except ProcessLookupError:
                    pass
            # Reap the child on timeout/cancellation before acknowledging completion.
            if process is not None:
                await asyncio.shield(process.wait())
