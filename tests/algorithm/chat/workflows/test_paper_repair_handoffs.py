"""Regression coverage for real chart publication and revision/review handoffs."""
import copy
from unittest.mock import Mock

import pytest

from lazymind.document_tools.paper import contracts, peer_review, publication, workflow
from lazymind.document_tools.paper.review.schema import ReviewResult, text_digest
from lazymind.document_tools.paper.writing import revise_document


def test_redraw_rebinds_only_chart_urls_and_keeps_old_resource(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path))
    old = dict(id='score', svg='<svg xmlns="http://www.w3.org/2000/svg"><text>2</text></svg>')
    new = dict(old, svg='<svg xmlns="http://www.w3.org/2000/svg"><text fill="blue">2</text></svg>')
    original = '# Paper\nSee [Score](#block-score).\n\n<a id="block-score"></a>\n![Score](media-placeholder://score)'
    published = publication.publish_charts(original, [old])
    fenced = '\n\n```md\n' + published + '\n```'
    updated = publication.refresh_chart_sources(published + fenced, [old], [new])
    assert updated == published.replace(publication.chart_uri(old), publication.chart_uri(new)) + fenced
    publication.publish_charts(updated, [new])
    assert len(list(tmp_path.rglob('*.svg'))) == 2
    with pytest.raises(ValueError, match='registered SVG'):
        publication.refresh_chart_sources(published, [new], [old])


def test_revision_rechecks_new_numbers_keeps_pending_and_preserves_other_sections(monkeypatch):
    source = '# Paper\n\n## Results\nThe time is 10 milliseconds.\n\n## Discussion\nDo not change this.'
    revised = source.replace('10 milliseconds', '10 seconds')
    missing = dict(severity='minor', category='evidence', location='Missing experiment', quote=None,
                   problem='Independent validation missing', suggestion='Supply results', action_type='needs_experiment')
    old = peer_review.build_output(ReviewResult(summary='Review', issues=[missing], limitations=[]),
                                   dict(topic='Paper', manuscript=source))
    inputs = contracts.validate_input('revise_document', dict(topic='Paper', manuscript=source,
        request='Only correct time units', review=old['review'], review_data=old['review_data'], selected_issue_ids=['R1']))
    inputs_before = copy.deepcopy(inputs)
    monkeypatch.setattr(revise_document, '_modify_manuscript', lambda *a, **kw: dict(manuscript=revised, is_demo=False))
    def review(value):
        assert value['manuscript'] == revised
        return peer_review.build_output(ReviewResult(summary='Fresh check', issues=[dict(
            severity='suggestion', category='language', location='Discussion', quote='Do not change this.',
            problem='Style can improve', suggestion='Optional editing', action_type='text_revision')],
            limitations=[], fact_claims=[]), value)
    checker = Mock(side_effect=review)
    monkeypatch.setattr(peer_review, 'run', checker)
    result = revise_document.run(inputs)
    checker.assert_called_once()
    contracts.validate_output('revise_document', result)
    assert result['manuscript'] == revised
    assert result['review_data']['manuscript_sha256'] == text_digest(revised)
    assert result['review_data']['manuscript_sha256'] != old['review_data']['manuscript_sha256']
    assert len(result['review_data']['fact_checks']) == 5
    assert any(issue['action_type'] == 'needs_experiment' and '尚未独立核实' in issue['problem']
               for issue in result['review_data']['issues'])
    assert inputs == inputs_before
    wrong = copy.deepcopy(result)
    wrong['review_data']['manuscript_sha256'] = text_digest(source)
    with pytest.raises(ValueError, match='当前稿件'):
        contracts.validate_output('revise_document', wrong)
