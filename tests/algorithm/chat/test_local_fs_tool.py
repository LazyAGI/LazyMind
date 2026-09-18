"""Generic file tool behavior; LocalFS data-source scopes do not enter this API."""
from pathlib import Path

import pytest
from lazyllm.tools.agent import FileSystemToolkit, ToolManager
from lazyllm.tools.agent.file_tool import read, write, edit, ls, glob, grep, mkdir, move, remove, stat


def test_filesystem_tools_are_immediately_visible():
    manager = ToolManager([FileSystemToolkit()])
    assert [item['function']['name'] for item in manager.tools_description] == [
        'read', 'write', 'edit', 'ls', 'glob', 'grep', 'mkdir', 'move', 'remove', 'stat']


def test_file_lifecycle(tmp_path):
    folder = tmp_path / 'project'
    mkdir(str(folder))
    path = str(folder / 'note.txt')
    write(path, 'first', mode='create')
    write(path, '\nsecond', mode='append')
    edit(path, 'first', 'updated')
    assert read(path)['content'] == 'updated\nsecond'
    assert ls(str(folder))['entries'] == ['note.txt']
    assert glob('**/*.txt', str(folder))['paths'] == [path]
    assert grep('second', str(folder))['results'][0]['path'] == path
    assert stat(path)['size'] == len('updated\nsecond')
    with pytest.raises(Exception):
        edit(path, 'missing', 'other')
    assert read(path)['content'] == 'updated\nsecond'
    moved = str(folder / 'moved.txt')
    move(path, moved)
    with pytest.raises(OSError):
        remove(str(folder))
    remove(str(folder), recursive=True)
    assert not folder.exists()


def test_remove_symlink_does_not_remove_target(tmp_path):
    target = tmp_path / 'keep.txt'
    target.write_text('keep')
    link = tmp_path / 'link.txt'
    link.symlink_to(target)
    manager = ToolManager([FileSystemToolkit()])
    batch = manager.prepare_tool_calls({'function': {'name': 'remove', 'arguments': {'path': str(link)}}})
    assert not batch[0].ready
    assert target.read_text() == 'keep'
    assert link.is_symlink()


@pytest.mark.parametrize('pattern,expected', [
    ('*.yml', {'root.yml', 'backend/child.yml'}),
    ('/*.yml', {'root.yml'}),
    ('backend/*.yml', {'backend/child.yml'}),
    ('**/*.{yaml,yml}', {'root.yml', 'backend/child.yml', 'backend/deep/nested.yaml'}),
])
def test_glob_ripgrep_path_filters(tmp_path, pattern, expected):
    (tmp_path / 'backend/deep').mkdir(parents=True)
    for name in ('root.yml', 'backend/child.yml', 'backend/deep/nested.yaml'):
        (tmp_path / name).write_text('value: 1')
    result = glob(pattern, str(tmp_path))
    assert {Path(p).relative_to(tmp_path).as_posix() for p in result['paths']} == expected
    assert not result['truncated']


def test_glob_limits_and_invalid_pattern(tmp_path):
    from lazyllm.tools.agent import ToolExecutionError
    (tmp_path / 'first.yml').touch()
    (tmp_path / 'second.yml').touch()
    result = glob('*.yml', str(tmp_path), max_results=1)
    assert len(result['paths']) == 1 and result['truncated']
    assert glob('*.absent', str(tmp_path))['paths'] == []
    with pytest.raises(ToolExecutionError):
        glob('[', str(tmp_path))
