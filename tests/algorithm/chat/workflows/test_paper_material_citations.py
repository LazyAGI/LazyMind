"""Study material provenance must stay separate from bibliographic references."""
import pytest

from lazymind.document_tools.paper import publication


PAPERS = [dict(id='ref-001', citation='A study[Z].')]


def test_known_study_attachment_is_not_rendered_as_a_bibliography_entry():
    marker = '[文献:upload-records]'
    body = f'Recorded results {marker}[文献:ref-001].\n\n`{marker}`\n\n```md\n{marker}\n```'
    result = publication.normalize_source_citations(body, PAPERS, material_source_ids=['upload-records'])
    assert result == body.replace('Recorded results ' + marker, 'Recorded results ')
    published = publication.publish_citations(result, PAPERS)
    assert '#paper-ref-ref-001' in published
    assert '#paper-ref-upload-records' not in published


def test_unknown_or_bibliographic_upload_ids_are_not_silently_removed():
    body = '[文献:upload-unknown][文献:upload-citable]'
    papers = [dict(id='upload-citable', citation='An uploaded publication[Z].')]
    result = publication.normalize_source_citations(body, papers, material_source_ids=['upload-citable'])
    assert result == body
    with pytest.raises(ValueError):
        publication.publish_citations(result, papers)
