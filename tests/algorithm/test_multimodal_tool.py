from lazymind.chat.engine.tools import multimodal


def test_vision_extractor_rejects_pdf_before_vlm(monkeypatch, tmp_path):
    pdf = tmp_path / 'invoice.pdf'
    pdf.write_bytes(b'%PDF-1.4\n')

    def fail_automodel(*args, **kwargs):
        raise AssertionError('AutoModel should not be called for PDFs')

    monkeypatch.setattr(multimodal, 'AutoModel', fail_automodel)

    result = multimodal.vision_extractor(str(pdf))

    assert result['success'] is False
    assert result['tool'] == 'vision_extractor'
    assert result['error']['type'] == 'UnsupportedFileType'
    assert 'only supports image files' in result['error']['reason']
    assert 'kb_tmp_search' in result['error']['reason']
