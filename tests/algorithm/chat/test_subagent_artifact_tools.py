import os
import shutil
from types import SimpleNamespace

from lazymind.chat.engine.subagent import tools


def test_build_file_artifact_accepts_normalized_path_object(monkeypatch, tmp_path):
    workspace = tmp_path / 'workspace'
    workspace.mkdir()
    source = tmp_path / 'report.docx'
    source.write_bytes(b'docx-content')

    def copy_into_workspace(path):
        destination = workspace / os.path.basename(path)
        shutil.copy2(path, destination)
        return destination.name

    monkeypatch.setattr(
        tools,
        'require_context',
        lambda: SimpleNamespace(
            workspace_path=str(workspace),
            copy_into_workspace=copy_into_workspace,
        ),
    )

    value, content_type = tools._build_artifact_value(
        {'path': str(source)},
        'file',
    )

    assert content_type == 'file'
    assert value['filename'] == 'report.docx'
    assert value['path'] == str(workspace / 'report.docx')
    assert value['size'] == len(b'docx-content')
