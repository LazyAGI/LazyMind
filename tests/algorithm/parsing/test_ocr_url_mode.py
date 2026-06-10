from lazyllm.tools.rag.readers.ocrReader.ocr_service import OcrServiceVariant, resolve_ocr_variant


def test_mineru_online_url_detection():
    assert resolve_ocr_variant('mineru', '') == OcrServiceVariant.ONLINE
    assert resolve_ocr_variant('mineru', 'https://mineru.net/api/v4/file-urls/batch') == OcrServiceVariant.ONLINE
    assert resolve_ocr_variant('mineru', 'http://mineru:8000/api/v1/pdf_parse') == OcrServiceVariant.OFFLINE
    assert resolve_ocr_variant('mineru', 'http://172.24.176.1:20234/api/v1/pdf_parse') == OcrServiceVariant.OFFLINE


def test_paddle_online_url_detection():
    assert resolve_ocr_variant('paddleocr', '') == OcrServiceVariant.ONLINE
    assert resolve_ocr_variant(
        'paddleocr',
        'https://k4q3k6o0l1hbx6jc.aistudio-app.com/layout-parsing',
    ) == OcrServiceVariant.ONLINE
    assert resolve_ocr_variant('paddleocr', 'http://paddleocr:8080') == OcrServiceVariant.OFFLINE
