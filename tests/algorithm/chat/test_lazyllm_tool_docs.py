import os
import subprocess
import sys
from pathlib import Path
from typing import get_args, get_type_hints


REPO_ROOT = Path(__file__).resolve().parents[3]


def test_save_chat_artifact_declares_exact_content_type_choices():
    from lazymind.chat.engine.tools.local_file.workspace import save_chat_artifact

    annotation = get_type_hints(save_chat_artifact)['content_type']

    assert get_args(annotation) == ('text', 'json', 'file')


def test_postponed_tool_annotations_are_resolved_before_registration():
    namespace = {}
    exec(
        'from __future__ import annotations\n'
        'from typing import Optional\n'
        'def tool(value: Optional[str] = None) -> Optional[str]:\n'
        '    """A test tool."""\n'
        '    return value\n',
        namespace,
    )
    tool = namespace['tool']
    assert isinstance(tool.__annotations__['value'], str)

    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs([tool])

    assert not isinstance(tool.__annotations__['value'], str)


def test_googledrive_docs_are_loaded_on_demand_without_loading_all_docs():
    script = r'''
import sys
from lazyllm import init_session, locals as lazyllm_locals
from lazyllm.tools.agent.toolsManager import ToolManager, _build_tool_desc
from lazyllm.tools.fs.supplier.googledrive import GoogleDriveFS
from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs

assert GoogleDriveFS.search.__doc__ is None
fs = GoogleDriveFS(dynamic_auth=True)
ensure_lazyllm_tool_docs([{'tools': [fs]}])
assert GoogleDriveFS.search.__doc__
assert 'lazyllm.docs.tools.tool_fs' in sys.modules
assert 'lazyllm.docs.tools.tool_rag' not in sys.modules

init_session()
lazyllm_locals['_lazyllm_agent'] = {'workspace': {}}
manager = ToolManager([(fs, lambda _instance: 'secret-token')])
manager._tool_call['get_GoogleDriveFS_methods']({})
search_tool = next(tool for tool in manager.all_tools if tool.name == 'GoogleDriveFS_search')
description = _build_tool_desc(search_tool)['function']['description']
assert description.startswith('Search the live source Google Drive')
'''
    env = os.environ.copy()
    env['LAZYLLM_INIT_DOC'] = 'False'
    env['PYTHONPATH'] = os.pathsep.join((
        str(REPO_ROOT / 'algorithm' / 'lazyllm'),
        str(REPO_ROOT / 'algorithm'),
        env.get('PYTHONPATH', ''),
    ))
    result = subprocess.run(
        [sys.executable, '-c', script],
        cwd=REPO_ROOT,
        env=env,
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert result.returncode == 0, result.stdout + result.stderr
