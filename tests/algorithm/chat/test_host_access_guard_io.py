"""Identity validation blocks stale approvals before the real file tool runs."""
import pytest

from lazyllm.tools.agent import HostFileIntent, ToolExecutionError
from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard


def guard(path, operation='write'):
    return HostAccessGuard((HostFileIntent(str(path.resolve()), operation),))


def test_guard_rejects_target_replaced_while_pending(tmp_path):
    target = tmp_path / 'image.png'
    target.write_bytes(b'approved')
    outside = tmp_path / 'outside.png'
    outside.write_bytes(b'private')
    access = guard(target, 'read')
    target.unlink()
    target.symlink_to(outside)
    with pytest.raises(ToolExecutionError):
        access.validate()
    assert outside.read_bytes() == b'private'


@pytest.mark.parametrize('phase', ['on_poll', 'on_claim'])
@pytest.mark.parametrize('replacement', ['parent_link', 'parent_directory', 'leaf'])
def test_replaced_path_is_rejected_before_write(workspace_runtime, tmp_path, phase, replacement):
    root = tmp_path / 'workspace'
    root.mkdir()
    parent = root / 'parent'
    parent.mkdir()
    target = parent / 'out.txt'
    target.write_text('approved', encoding='utf-8')
    outside = tmp_path / 'outside'
    outside.mkdir()
    middleware, core, _ = workspace_runtime(root)

    def replace(operation):
        if replacement == 'leaf':
            target.rename(parent / 'original.txt')
            target.write_text('new identity', encoding='utf-8')
        else:
            parent.rename(root / 'original')
            if replacement == 'parent_link':
                parent.symlink_to(outside, target_is_directory=True)
            else:
                parent.mkdir()
        operation.update(status='allowed', decision='allowed')
    setattr(core, phase, replace)
    result = middleware.execute_with_records({'function': {
        'name': 'write', 'arguments': {'path': str(target), 'content': 'must not write'},
    }}).results[0]
    assert result['ok'] is False
    assert list(outside.iterdir()) == []
    if replacement == 'leaf':
        assert target.read_text(encoding='utf-8') == 'new identity'
    else:
        assert not target.exists()
        assert (root / 'original' / 'out.txt').read_text(encoding='utf-8') == 'approved'


def test_guard_rejects_missing_parent_replaced_by_link(tmp_path):
    target = tmp_path / 'future' / 'out.bin'
    outside = tmp_path / 'outside'
    outside.mkdir()
    access = guard(target)
    target.parent.symlink_to(outside, target_is_directory=True)
    with pytest.raises(ToolExecutionError):
        access.validate()
    assert list(outside.iterdir()) == []


def test_guard_advances_only_its_successful_mutations(tmp_path):
    target, other = tmp_path / 'output', tmp_path / 'other'
    access = guard(target)
    other.mkdir()
    access.record_changes((HostFileIntent(str(other), 'write'),))
    target.mkdir()
    with pytest.raises(ToolExecutionError):
        access.validate()
    access.record_changes((HostFileIntent(str(target), 'write'),))
    access.validate()
    (target / 'result.txt').write_text('output', encoding='utf-8')
    access.validate()
    access.close()
    assert access.targets == []
