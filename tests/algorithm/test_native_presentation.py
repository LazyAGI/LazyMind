import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from zipfile import ZipFile

from pptx import Presentation

spec = importlib.util.spec_from_file_location('native_presentation', (Path(__file__).parents[2] / 'algorithm')
                                              / 'lazymind/chat/engine/tools/file_resources/native_presentation.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class NativePresentationTests(unittest.TestCase):
    def test_template_content_is_replaced_without_overwriting_source(self):
        with tempfile.TemporaryDirectory() as temp:
            source, output = Path(temp) / 'template.pptx', Path(temp) / 'output.pptx'
            template = Presentation()
            template.slides.add_slide(template.slide_layouts[1]).shapes.title.text = 'Sample content'
            template.save(source)
            before = source.read_bytes()
            result = module.build_native_pptx([
                {'title': 'Research', 'bullets': ['Evidence'], 'notes': 'Speaker notes'},
                {'title': 'Conclusion', 'bullets': ['Result']},
            ], output, source)
            actual = Presentation(output)
            self.assertEqual(result['slide_count'], 2)
            self.assertEqual(len(actual.slides), 2)
            self.assertEqual(actual.slides[0].shapes.title.text, 'Research')
            self.assertIn('Evidence', '\n'.join(s.text for s in actual.slides[0].shapes if s.has_text_frame))
            self.assertEqual(actual.slides[0].notes_slide.notes_text_frame.text, 'Speaker notes')
            self.assertEqual(source.read_bytes(), before)
            self.assertEqual(actual.slide_width, template.slide_width)

    def test_plain_presentation_is_editable(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'plain.pptx'
            from test_pptx_font_edit import make_font
            from lazymind.chat.engine.tools.file_resources.font_library import register_font
            with patch.dict('os.environ', {'LAZYMIND_FONT_DIR': str(Path(temp) / 'fonts')}):
                record = register_font(make_font(Path(temp), family='Songti SC', text='PlainEditable text'))
                with patch('lazymind.chat.engine.tools.file_resources.presentation_edit.available_font_families',
                           return_value={'Songti SC'}):
                    module.build_native_pptx([{'title': 'Plain', 'bullets': ['Editable text']}], output)
            with ZipFile(output) as archive:
                self.assertIn(b'embeddedFontLst', archive.read('ppt/presentation.xml'))
                self.assertIn(f'ppt/fonts/lazymind-{record["id"]}.fntdata', archive.namelist())
            actual = Presentation(output)
            self.assertEqual(actual.slides[0].shapes.title.text, 'Plain')
            self.assertEqual(actual.slides[0].shapes.title.text_frame.paragraphs[0].runs[0].font.name, 'Songti SC')


if __name__ == '__main__':
    unittest.main()
