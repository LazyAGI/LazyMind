"""Read a host-bound manuscript; never accept model-invented paths or fetch URLs."""
from pathlib import Path

MAX_FILE_BYTES = 20 * 1024 * 1024
MAX_MANUSCRIPT_CHARS = 200_000


def read_manuscript(path: str) -> str:
    """Reuse LazyLLM document readers for PDF/DOCX, preserving the full text."""
    source = Path(path)
    if not source.is_file():
        raise ValueError('未找到已绑定的论文文件，请重新上传')
    if source.stat().st_size > MAX_FILE_BYTES:
        raise ValueError('论文文件超过 20 MB，请提供较小的可解析版本')
    suffix = source.suffix.lower()
    if suffix in {'.md', '.markdown', '.txt'}:
        content = source.read_text(encoding='utf-8-sig')
    elif suffix in {'.pdf', '.docx'}:
        # Same readers used by Writer; no LLM summarization or silent truncation.
        if suffix == '.pdf':
            from lazyllm.tools.rag.readers.pdfReader import PDFReader
            reader = PDFReader()
        else:
            from lazyllm.tools.rag.readers.docxReader import DocxReader
            reader = DocxReader()
        nodes = reader(str(source))
        parts = [str(node.text or '').strip() for node in nodes]
        if suffix == '.pdf' and any(not part for part in parts):
            raise ValueError('论文 PDF 存在无法提取文字的页面，请提供带完整文本的 PDF、DOCX 或正文')
        content = '\n\n'.join(parts)
    else:
        raise ValueError('论文文件仅支持 PDF、DOCX、Markdown 或 TXT')
    if not content.strip():
        raise ValueError('未能提取论文正文，请提供可解析的论文文件')
    if len(content) > MAX_MANUSCRIPT_CHARS:
        raise ValueError('论文正文超过当前 20 万字符限制，请缩小审查范围')
    return content
