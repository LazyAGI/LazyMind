import logging
import os
import shutil
import subprocess
import tempfile
import threading
from pathlib import Path
from typing import Iterable

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel


logging.basicConfig(level=logging.INFO, format='%(message)s', force=True)
logger = logging.getLogger('office-convert-service')
logger.setLevel(logging.INFO)

app = FastAPI(
    title='Office Convert Service',
    description='A standalone service for converting Office documents to PDF',
    version='1.0.0',
    docs_url='/docs',
    redoc_url=None,
    openapi_url='/openapi.json',
)

OFFICE_EXTENSIONS = {'.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx'}
DEFAULT_ALLOWED_ROOTS = '/var/lib/lazymind/uploads'
DEFAULT_TIMEOUT_SECONDS = 900
DEFAULT_CONCURRENCY = 4


class ConvertRequest(BaseModel):
    source_path: str


class ConvertResponse(BaseModel):
    pdf_path: str
    reused: bool = False
    provider: str = 'libreoffice'


def _allowed_roots() -> list[Path]:
    raw = os.getenv('OFFICE_CONVERT_ALLOWED_ROOTS', DEFAULT_ALLOWED_ROOTS)
    roots: list[Path] = []
    for part in raw.split(','):
        part = part.strip()
        if not part:
            continue
        roots.append(Path(part).resolve())
    return roots


def _timeout_seconds() -> int:
    raw = (os.getenv('OFFICE_CONVERT_TIMEOUT_SECONDS') or '').strip()
    if not raw:
        return DEFAULT_TIMEOUT_SECONDS
    try:
        value = int(raw)
    except ValueError:
        return DEFAULT_TIMEOUT_SECONDS
    if value <= 0:
        return DEFAULT_TIMEOUT_SECONDS
    return value


def _concurrency() -> int:
    raw = (os.getenv('OFFICE_CONVERT_CONCURRENCY') or '').strip()
    if not raw:
        return DEFAULT_CONCURRENCY
    try:
        value = int(raw)
    except ValueError:
        return DEFAULT_CONCURRENCY
    if value <= 0:
        return DEFAULT_CONCURRENCY
    return value


_convert_semaphore = threading.BoundedSemaphore(_concurrency())


def _is_under_any_root(path: Path, roots: Iterable[Path]) -> bool:
    resolved = path.resolve()
    for root in roots:
        try:
            resolved.relative_to(root)
            return True
        except ValueError:
            continue
    return False


def _expected_pdf_path(source: Path) -> Path:
    return source.with_name(f'{source.stem}.pdf')


def _validate_source_path(source_path: str) -> Path:
    source = Path(source_path).expanduser().resolve()
    if not source.exists() or not source.is_file():
        raise HTTPException(status_code=404, detail='source file not found')
    if source.suffix.lower() not in OFFICE_EXTENSIONS:
        raise HTTPException(status_code=400, detail='source file is not a supported office document')
    if not _is_under_any_root(source, _allowed_roots()):
        raise HTTPException(status_code=400, detail='source path is outside allowed roots')
    return source


def _reuse_if_fresh(source: Path, target: Path) -> bool:
    if not target.exists() or not target.is_file() or target.stat().st_size <= 0:
        return False
    return target.stat().st_mtime >= source.stat().st_mtime


def _run_libreoffice_convert(source: Path, target: Path) -> None:
    output_dir = target.parent
    output_dir.mkdir(parents=True, exist_ok=True)

    with (
        tempfile.TemporaryDirectory(dir=str(output_dir)) as tmpdir,
        tempfile.TemporaryDirectory(prefix='lo-profile-') as profile_dir,
    ):
        tmp_output_dir = Path(tmpdir)
        profile_uri = Path(profile_dir).resolve().as_uri()
        command = [
            'libreoffice',
            f'-env:UserInstallation={profile_uri}',
            '--headless',
            '--nologo',
            '--nofirststartwizard',
            '--nolockcheck',
            '--nodefault',
            '--convert-to',
            'pdf',
            str(source),
            '--outdir',
            str(tmp_output_dir),
        ]
        logger.info('running libreoffice convert source=%s target=%s', source, target)
        try:
            completed = subprocess.run(
                command,
                check=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=_timeout_seconds(),
            )
        except subprocess.TimeoutExpired as exc:
            raise HTTPException(status_code=504, detail=f'libreoffice convert timeout: {exc}') from exc
        except subprocess.CalledProcessError as exc:
            stderr = (exc.stderr or '').strip()
            stdout = (exc.stdout or '').strip()
            detail = stderr or stdout or str(exc)
            raise HTTPException(status_code=500, detail=f'libreoffice convert failed: {detail}') from exc

        converted_tmp = tmp_output_dir / f'{source.stem}.pdf'
        if not converted_tmp.exists() or converted_tmp.stat().st_size <= 0:
            stdout = (completed.stdout or '').strip()
            stderr = (completed.stderr or '').strip()
            raise HTTPException(status_code=500, detail=f'converted pdf not found; stdout={stdout}; stderr={stderr}')

        shutil.move(str(converted_tmp), str(target))


@app.get('/health')
def health() -> dict[str, str]:
    return {'status': 'ok'}


@app.post('/v1/office/to-pdf', response_model=ConvertResponse)
def convert_office_to_pdf(req: ConvertRequest) -> ConvertResponse:
    source = _validate_source_path(req.source_path)
    target = _expected_pdf_path(source)

    if _reuse_if_fresh(source, target):
        logger.info('reuse converted pdf source=%s target=%s', source, target)
        return ConvertResponse(pdf_path=str(target), reused=True)

    logger.info('waiting for convert slot source=%s concurrency=%d', source, _concurrency())
    with _convert_semaphore:
        _run_libreoffice_convert(source, target)
    if not target.exists() or target.stat().st_size <= 0:
        raise HTTPException(status_code=500, detail='converted pdf not found after libreoffice run')

    logger.info('convert succeeded source=%s target=%s size=%d', source, target, target.stat().st_size)
    return ConvertResponse(pdf_path=str(target), reused=False)
