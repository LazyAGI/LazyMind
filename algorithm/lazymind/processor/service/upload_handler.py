"""
Upload handler for DocumentProcessor: receives multipart files, saves to shared dir,
then POSTs to /doc/add. Used when doc-manager is not available.
"""
import os
import uuid

import httpx
from lazyllm import LOG
from lazyllm.thirdparty import fastapi
from lazyllm.tools.rag.parsing_service.base import AddDocRequest, FileInfo
from lazyllm.tools.rag.utils import BaseResponse, gen_docid

from lazymind.config import config as _cfg

UPLOAD_DIR = _cfg['upload_dir']
DEFAULT_ALGO_ID = _cfg['default_algo_id']
DEFAULT_GROUP = _cfg['default_group']
PROCESSOR_PORT = _cfg['document_processor_port']

os.makedirs(UPLOAD_DIR, exist_ok=True)

app = fastapi.FastAPI()


@app.post('/upload_and_add')
async def upload_and_add(
    request: fastapi.Request,
    files: list[fastapi.UploadFile] = fastapi.File(...),  # noqa B008
    group_name: str = fastapi.Form(None),  # noqa B008
    algo_id: str = fastapi.Form(None),  # noqa B008
    override: bool = fastapi.Form(None),  # noqa B008
):
    # Support query params for proxy compatibility (frontend sends group_name in URL)
    if group_name is None:
        group_name = request.query_params.get('group_name', DEFAULT_GROUP)
    if algo_id is None:
        algo_id = request.query_params.get('algo_id', DEFAULT_ALGO_ID)
    if override is None:
        override = request.query_params.get('override', 'true').lower() in ('1', 'true', 'yes')
    # algo_id is kept for backward compat with callers but is no longer forwarded to
    # AddDocRequest (removed in the node-group refactor).  Log it for audit purposes.
    if algo_id:
        LOG.warning(f'upload_and_add: algo_id={algo_id!r} received but ignored (deprecated)')
    if not files:
        raise fastapi.HTTPException(status_code=400, detail='files is required')
    saved_paths = []
    file_infos = []
    subdir = str(uuid.uuid4())
    dest_dir = os.path.join(UPLOAD_DIR, subdir)
    os.makedirs(dest_dir, exist_ok=True)
    try:
        for f in files:
            filename = f.filename or 'unnamed'
            dest_path = os.path.join(dest_dir, filename)
            content = await f.read()
            with open(dest_path, 'wb') as out:
                out.write(content)
            doc_id = gen_docid(dest_path)
            saved_paths.append(dest_path)
            # group_name is the dataset/KB id used as kb_id in AddDocRequest.
            file_infos.append(FileInfo(file_path=dest_path, doc_id=doc_id))
        req = AddDocRequest(kb_id=group_name, file_infos=file_infos)
        processor_url = f'http://127.0.0.1:{PROCESSOR_PORT}'
        async with httpx.AsyncClient() as client:
            r = await client.post(f'{processor_url}/doc/add', json=req.model_dump(mode='json'), timeout=60.0)
        if r.status_code != 200:
            try:
                err = r.json()
                detail = err.get('detail', r.text)
            except Exception:
                detail = r.text
            raise fastapi.HTTPException(status_code=r.status_code, detail=detail)
        data = r.json()
        return BaseResponse(
            code=200,
            msg='success',
            data={'task_id': data.get('data', {}).get('task_id'), 'ids': [fi.doc_id for fi in file_infos]},
        )
    except fastapi.HTTPException:
        raise
    except Exception as e:
        LOG.error(f'upload_and_add failed: {e}')
        for p in saved_paths:
            try:
                os.remove(p)
            except OSError:
                pass
        try:
            os.rmdir(dest_dir)
        except OSError:
            pass
        raise fastapi.HTTPException(status_code=500, detail=str(e))


def run_upload_server(port: int = 8001):
    import uvicorn
    uvicorn.run(app, host='0.0.0.0', port=port)


if __name__ == '__main__':
    run_upload_server(_cfg['upload_server_port'])
