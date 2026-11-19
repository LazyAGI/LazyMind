"""Re-render one test artifact from its saved translations, then verify text."""
import sys
from main import PDFTranslationRenderRequest, render_pdf_translation
from verify_translation_output import verify

prefix = sys.argv[1]
print(render_pdf_translation(PDFTranslationRenderRequest(
    source_path=prefix + '.source.pdf', layout_path=prefix + '.input-layout.json',
    translations_path=prefix + '.input-layout.translations.json',
    output_path=prefix + '.translated.pdf', target_language='zh',
)), flush=True)
result = verify(prefix)
print(result, flush=True)
sys.exit(bool(result['missing_blocks']))
