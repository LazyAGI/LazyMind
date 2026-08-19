import pytest
from lazyllm.tools.agent import ToolDomainError

from lazymind.chat.engine.tools import multimodal
from lazymind.chat.engine.tools.infra import image_generation_support as image_support


def test_vision_extractor_rejects_pdf_before_vlm(monkeypatch, tmp_path):
    pdf = tmp_path / 'invoice.pdf'
    pdf.write_bytes(b'%PDF-1.4\n')

    def fail_automodel(*args, **kwargs):
        raise AssertionError('AutoModel should not be called for PDFs')

    monkeypatch.setattr(multimodal, 'AutoModel', fail_automodel)

    with pytest.raises(ToolDomainError, match='only supports image files') as captured:
        multimodal.vision_extractor(str(pdf))

    assert 'kb_tmp_search' in str(captured.value)


def test_run_image_model_uses_declared_role(monkeypatch):
    captured = {}

    class _FakeModel:
        def __init__(self, *, model):
            captured['model'] = model

        def __call__(self, *_args, **_kwargs):
            return 'raw'

    monkeypatch.setattr(image_support, 'AutoModel', _FakeModel)
    monkeypatch.setattr(image_support, '_parse_generated_files', lambda _raw: ['/tmp/fake.png'])
    monkeypatch.setattr(image_support, '_relocate_generated_images', lambda _paths: ['/tmp/final.png'])
    monkeypatch.setattr(image_support, '_register_generated_image_paths', lambda _paths: None)
    monkeypatch.setattr(
        image_support,
        '_build_image_payload',
        lambda local_path, label: {
            'local_path': local_path,
            'image_url': '/static-files/ai_generated/final.png?sig=test',
            'image_markdown': '![final](/static-files/ai_generated/final.png?sig=test)',
        },
    )

    result = image_support.run_image_model('image_editor', 'make it brighter')

    assert captured['model'] == 'image_editor'
    assert result['local_path'] == '/tmp/final.png'
