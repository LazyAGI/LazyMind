"""Actual descriptor IO stays inside approved host effects across path swaps."""
import os

import pytest

from lazyllm.tools.agent import HostFileIntent, ToolExecutionError
from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard


def guard(path, operation='write'):
    return HostAccessGuard((HostFileIntent(str(path), operation),))


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
    with pytest.raises(ToolExecutionError):
        access.open_read(target)
    assert outside.read_bytes() == b'private'


def test_guard_no_side_effect_if_missing_parent_becomes_link(tmp_path):
    target = tmp_path / 'future' / 'out.bin'
    outside = tmp_path / 'outside'
    outside.mkdir()
    access = guard(target)
    target.parent.symlink_to(outside, target_is_directory=True)
    with pytest.raises((ToolExecutionError, OSError)):
        access.makedirs(target.parent)
    with pytest.raises((ToolExecutionError, OSError)):
        access.open_write(target)
    assert list(outside.iterdir()) == []


def test_guard_declared_new_output_directory_allows_descendants(tmp_path):
    output = tmp_path / 'new' / 'store'
    access = guard(output)
    access.makedirs(output / 'nested')
    with access.open_write(output / 'nested' / 'result.bin') as stream:
        stream.write(b'output')
    assert (output / 'nested' / 'result.bin').read_bytes() == b'output'
    with access.open_read(output / 'nested' / 'result.bin') as stream:
        assert stream.read() == b'output'
    access.close()


def test_guard_existing_parent_replaced_by_directory_is_rejected(tmp_path):
    parent = tmp_path / 'parent'
    parent.mkdir()
    target = parent / 'out.bin'
    access = guard(target)
    parent.rename(tmp_path / 'original')
    parent.mkdir()
    with pytest.raises((ToolExecutionError, OSError)):
        access.open_write(target)
    assert list(parent.iterdir()) == []


def test_guard_existing_leaf_replaced_does_not_truncate(tmp_path):
    target = tmp_path / 'out.bin'
    target.write_bytes(b'old')
    access = guard(target)
    target.rename(tmp_path / 'original.bin')
    target.write_bytes(b'new identity')
    with pytest.raises(ToolExecutionError):
        access.open_write(target)
    assert target.read_bytes() == b'new identity'


def test_guard_child_link_injected_after_admission_cannot_escape(tmp_path):
    output, outside = tmp_path / 'output', tmp_path / 'outside'
    output.mkdir()
    outside.mkdir()
    secret = outside / 'secret.bin'
    secret.write_bytes(b'secret')
    access = guard(output)
    (output / 'child').symlink_to(outside, target_is_directory=True)
    for operation in [lambda: access.open_read(output / 'child' / 'secret.bin'),
                      lambda: access.open_write(output / 'child' / 'secret.bin'),
                      lambda: access.makedirs(output / 'child' / 'new')]:
        with pytest.raises((ToolExecutionError, OSError)):
            operation()
    assert secret.read_bytes() == b'secret'
    assert not (outside / 'new').exists()
    assert all('secret.bin' not in files for _, _, files in access.walk(output))


def test_guard_copy_and_move_are_scoped_and_no_replace(tmp_path):
    source, destination = tmp_path / 'source', tmp_path / 'destination'
    source.write_bytes(b'data')
    access = HostAccessGuard((HostFileIntent(str(source), 'delete'), HostFileIntent(str(destination), 'write')))
    access.copy(source, destination)
    with pytest.raises(FileExistsError):
        access.rename(source, destination)
    assert source.read_bytes() == destination.read_bytes() == b'data'
    access.rename(source, destination, overwrite=True)
    assert not source.exists()
    assert destination.read_bytes() == b'data'


def test_guard_delete_does_not_follow_injected_child_link(tmp_path):
    tree, outside = tmp_path / 'tree', tmp_path / 'outside'
    tree.mkdir()
    outside.mkdir()
    secret = outside / 'secret'
    secret.write_text('keep')
    access = guard(tree, 'delete')
    (tree / 'link').symlink_to(outside, target_is_directory=True)
    access.delete(tree, recursive=True)
    assert not tree.exists()
    assert secret.read_text() == 'keep'


def test_guard_actual_open_rejects_parent_swap_after_check(tmp_path, monkeypatch):
    directory, outside = tmp_path / 'directory', tmp_path / 'outside'
    directory.mkdir()
    outside.mkdir()
    target = directory / 'result.bin'
    access = guard(target)
    original = access.check_path

    def swap_after_check(path, operation='read'):
        approved = original(path, operation)
        directory.rename(tmp_path / 'original')
        directory.symlink_to(outside, target_is_directory=True)
        return approved

    monkeypatch.setattr(access, 'check_path', swap_after_check)
    with pytest.raises((ToolExecutionError, OSError)):
        access.open_write(target)
    assert list(outside.iterdir()) == []


def test_read_tree_with_child_link_never_walks_external_directory(tmp_path):
    tree, outside = tmp_path / 'tree', tmp_path / 'outside'
    tree.mkdir()
    outside.mkdir()
    (outside / 'private').write_text('private')
    (tree / 'link').symlink_to(outside, target_is_directory=True)
    access = guard(tree, 'read')
    assert all('private' not in files for _, _, files in access.walk(tree))
