from pathlib import Path

from lazymind.relay_payload_server import _expand_payload_args


def test_expand_payload_args_removes_consumed_file(tmp_path: Path):
    payload = tmp_path / 'function.payload'
    payload.write_text('serialized-function', encoding='ascii')

    expanded = _expand_payload_args([f'--function-file={payload}', '--open_port=18004'])

    assert expanded == ['--function=serialized-function', '--open_port=18004']
    assert not payload.exists()
