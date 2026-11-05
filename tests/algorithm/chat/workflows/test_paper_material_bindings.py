"""Pure binding checks: no model, files, or monkeypatch required."""
import copy

import pytest

from lazymind.document_tools.paper.material_bindings import MaterialBindings


def test_canonical_identity_round_trip_without_model_recopy():
    identifier = 'upload-17cae16226173b1c269c5a71cde4c14c6a34c189ed9e69380936615c861bc78a'
    source = {'id': identifier, 'title': 'System brief', 'metadata': {'file_id': identifier}}
    original = copy.deepcopy(source)
    bindings = MaterialBindings([source])
    view = bindings.model_view({'papers': [source], 'question': f'Background [{identifier}]'})
    assert view['papers'][0]['id'] == 'M1'
    assert view['question'] == 'Background [M1]'
    assert bindings.restore_citations('Research [M1]') == f'Research [{identifier}]'
    view['papers'][0]['metadata']['file_id'] = 'changed'
    assert source == original
    assert bindings.materials['M1'] == original


def test_unknown_markers_cannot_become_material_ids():
    with pytest.raises(ValueError, match='unavailable source'):
        MaterialBindings([{'id': 'known'}]).restore_citations('[M2]')


def test_material_views_do_not_replace_id_prefixes_inside_other_words():
    bindings = MaterialBindings([{'id': 'SRC-1'}, {'id': 'a'}])
    assert bindings.model_view('[SRC-1] [SRC-10] data') == '[M1] [SRC-10] data'
