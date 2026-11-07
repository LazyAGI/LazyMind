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


@pytest.mark.parametrize('citation,expected', [
    ('[M1, M3]', '[source-1][source-3]'),
    ('[M1–M3]', '[source-1][source-2][source-3]'),
    ('[M1-M2, M3]', '[source-1][source-2][source-3]'),
    ('[文献:M1，M2]', '[文献:source-1][文献:source-2]'),
])
def test_citation_groups_resolve_each_registered_source(citation, expected):
    bindings = MaterialBindings([{'id': f'source-{index}'} for index in range(1, 4)])
    assert bindings.restore_citations(citation) == expected


@pytest.mark.parametrize('citation', ['[M1, M4]', '[M1–M999999999]', '[M3-M1]', '[M1-M2-M3]'])
def test_invalid_citation_groups_keep_identity_validation(citation):
    bindings = MaterialBindings([{'id': f'source-{index}'} for index in range(1, 4)])
    with pytest.raises(ValueError, match='unavailable source'):
        bindings.restore_citations(citation)
