"""Generic file tool behavior; LocalFS data-source scopes do not enter this API."""
from pathlib import Path

import pytest
from lazyllm.tools.agent import FileSystemToolkit, ToolManager
from lazyllm.tools.agent.file_tool import read, write, edit, ls, glob, grep, mkdir, move, remove, stat


def test_filesystem_tools_are_immediately_visible():
    manager = ToolManager([FileSystemToolkit()])
    assert [item['function']['name'] for item in manager.tools_description] == [
        'read', 'write', 'edit', 'ls', 'glob', 'grep', 'mkdir', 'move', 'remove', 'stat']


def test_file_tool_schema_and_prepared_paths(tmp_path):
    from lazyllm.tools import HostFileIntent
    manager = ToolManager([FileSystemToolkit()])
    expected = {
        'read': ({'file_path', 'offset', 'limit'}, {'file_path'}),
        'write': ({'file_path', 'content'}, {'file_path', 'content'}),
        'edit': ({'file_path', 'old_string', 'new_string', 'replace_all'},
                 {'file_path', 'old_string', 'new_string'}),
        'grep': ({'pattern', 'path', 'include'}, {'pattern'}),
        'glob': ({'pattern', 'path'}, {'pattern'}),
        'ls': ({'path', 'limit'}, set()),
        'mkdir': ({'path', 'parents', 'exist_ok'}, {'path'}),
        'move': ({'src', 'dst', 'overwrite', 'create_parents'}, {'src', 'dst'}),
        'remove': ({'path', 'recursive'}, {'path'}),
        'stat': ({'path'}, {'path'}),
    }
    for description in manager.tools_description:
        function = description['function']
        schema = function['parameters']
        fields, required = expected[function['name']]
        assert set(schema['properties']) == fields
        assert set(schema.get('required', [])) == required
        assert schema['additionalProperties'] is False
    for name, arguments, operation in [
        ('read', {'file_path': 'note.txt'}, 'read'),
        ('write', {'file_path': 'note.txt', 'content': 'hello'}, 'write'),
        ('edit', {'file_path': 'note.txt', 'old_string': 'hello', 'new_string': 'world'}, 'write'),
    ]:
        batch = manager.prepare_tool_calls({'function': {'name': name, 'arguments': arguments}},
                                           working_directory=str(tmp_path))
        assert batch[0].ready
        assert batch[0].validated_arguments['file_path'] == str(tmp_path / 'note.txt')
        assert batch[0].host_files == (HostFileIntent(str(tmp_path / 'note.txt'), operation),)
    for name, arguments in [
        ('read', {'path': 'note.txt'}),
        ('write', {'file_path': 'note.txt', 'content': 'x', 'mode': 'append'}),
        ('edit', {'file_path': 'note.txt', 'old_text': 'x', 'new_text': 'y'}),
        ('grep', {'pattern': 'x', 'root': str(tmp_path)}),
        ('grep', {'pattern': 'x', 'glob': '*.txt'}),
        ('glob', {'pattern': '*.txt', 'max_results': 1}),
    ]:
        batch = manager.prepare_tool_calls({'function': {'name': name, 'arguments': arguments}},
                                           working_directory=str(tmp_path))
        assert not batch[0].ready
    assert not (tmp_path / 'note.txt').exists()
    (tmp_path / 'note.txt').write_text('line\n' * 2001)
    batch = manager.prepare_tool_calls({'function': {'name': 'read', 'arguments': {
        'file_path': 'note.txt'}}}, working_directory=str(tmp_path))
    result = manager.execute_prepared(batch).results[0]['value']
    assert result['offset'] == 1 and result['end_line'] == 2000 and result['next_offset'] == 2001


def test_search_defaults_and_explicit_path(tmp_path):
    current, other = tmp_path / 'current', tmp_path / 'other'
    current.mkdir()
    other.mkdir()
    (current / 'note.txt').write_text('first\nneedle here')
    (current / 'skip.py').write_text('needle elsewhere')
    (other / 'remote.txt').write_text('needle outside cwd')
    manager = ToolManager([FileSystemToolkit()])
    def invoke(name, **arguments):
        batch = manager.prepare_tool_calls({'function': {'name': name, 'arguments': arguments}},
                                           working_directory=str(current))
        result = manager.execute_prepared(batch).results[0]
        assert result['ok'], result
        return result['value']
    hits = invoke('grep', pattern='needle', include='*.txt')['results']
    assert [item['path'] for item in hits] == [str(current / 'note.txt')]
    assert invoke('read', file_path=hits[0]['path'], offset=int(hits[0]['line']))['content'] == 'needle here'
    assert invoke('glob', pattern='*.txt')['paths'] == [str(current / 'note.txt')]
    assert invoke('ls')['entries'] == ['note.txt', 'skip.py']
    assert invoke('grep', pattern='needle', path=str(other))['results'][0]['path'] == str(other / 'remote.txt')
    assert invoke('glob', pattern='*.txt', path=str(other))['paths'] == [str(other / 'remote.txt')]
    assert invoke('grep', pattern='absent')['results'] == []


def test_edit_unique_and_replace_all(tmp_path):
    from lazyllm.tools.agent import ToolExecutionError
    path = str(tmp_path / 'note.txt')
    write(path, 'one one')
    for old, new, all_matches in [('one', 'two', False), ('absent', 'two', True),
                                  ('', 'two', False), ('one', 'one', True)]:
        with pytest.raises(ToolExecutionError):
            edit(path, old, new, replace_all=all_matches)
        assert read(path)['content'] == 'one one'
    assert edit(path, 'one', 'two', replace_all=True)['replacements'] == 2
    assert read(path)['content'] == 'two two'


def test_compacted_read_uses_current_schema(tmp_path):
    from lazymind.chat.engine.agent_runtime.compactors import compact_file_result
    path = str(tmp_path / 'note.txt')
    write(path, 'first\nsecond')
    compacted, _ = compact_file_result('read', read(path, limit=1))
    assert f'Continue with read(file_path={path!r}, offset=2)' in compacted


def test_file_lifecycle(tmp_path):
    folder = tmp_path / 'project'
    mkdir(str(folder))
    path = str(folder / 'note.txt')
    write(path, 'first')
    edit(path, 'first', 'first\nsecond')
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
    for index in range(100):
        (tmp_path / f'{index}.yml').touch()
    result = glob('*.yml', str(tmp_path))
    assert len(result['paths']) == 100 and result['truncated']
    assert glob('*.absent', str(tmp_path))['paths'] == []
    with pytest.raises(ToolExecutionError):
        glob('[', str(tmp_path))
