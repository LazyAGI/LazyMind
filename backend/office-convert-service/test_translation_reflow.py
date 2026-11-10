import json
import tempfile
import unittest
from pathlib import Path
import fitz
import main
from translation_pdf import render_preserving_pages


class TranslationReflowTests(unittest.TestCase):
    def test_short_first_page_title_is_not_discarded_as_author_metadata(self):
        with tempfile.TemporaryDirectory(dir='/var/lib/lazymind/uploads') as directory:
            root = Path(directory)
            with fitz.open() as source:
                page = source.new_page(width=595, height=842)
                page.insert_text((120, 200), 'Attention Is All You Need', fontsize=14)
                source.save(root / 'source.pdf')
            main.extract_pdf_translation_layout(
                main.PDFTranslationLayoutRequest(
                    source_path=str(root / 'source.pdf'),
                    output_path=str(root / 'layout.json'),
                )
            )
            blocks = json.loads((root / 'layout.json').read_text())['blocks']
            self.assertTrue(
                any('Attention Is All You Need' in block['text'] for block in blocks)
            )

    def test_inline_math_is_excluded_from_translated_prose_runs(self):
        with tempfile.TemporaryDirectory(dir='/var/lib/lazymind/uploads') as directory:
            root = Path(directory)
            with fitz.open() as source:
                page = source.new_page()
                page.insert_text((50, 100), 'The coefficient ', fontsize=11)
                page.insert_text((130, 100), 'a', fontname='symb', fontsize=11)
                page.insert_text((140, 100), ' is positive.', fontsize=11)
                symbol_box = fitz.Rect(130, 88, 136, 103)
                pixels = page.get_pixmap(clip=symbol_box).samples
                source.save(root / 'source.pdf')
                main.extract_pdf_translation_layout(main.PDFTranslationLayoutRequest(
                    source_path=str(root / 'source.pdf'), output_path=str(root / 'layout.json'),
                ))
                blocks = json.loads((root / 'layout.json').read_text())['blocks']
                self.assertEqual([block['text'] for block in blocks], ['The coefficient', 'is positive.'])
                translations = {block['id']: value for block, value in zip(blocks, ['系数', '为正。'])}
                render_preserving_pages(source, blocks, translations, main.TRANSLATION_FONT_PATH)
                self.assertEqual(source[0].get_text().count('a'), 1)
                self.assertIn('系数', source[0].get_text())
                self.assertIn('为正', source[0].get_text())
                self.assertEqual(pixels, source[0].get_pixmap(clip=symbol_box).samples)

    def test_non_latin_inline_math_prose_remains_translatable(self):
        for prefix, suffix in [('系数', '为正数'), ('係数', 'は正です')]:
            with self.subTest(prefix=prefix), tempfile.TemporaryDirectory(
                dir='/var/lib/lazymind/uploads'
            ) as directory, fitz.open() as source:
                root = Path(directory)
                page = source.new_page()
                page.insert_text((50, 100), prefix, fontname='china-s', fontsize=11)
                page.insert_text((74, 100), 'a', fontname='symb', fontsize=11)
                page.insert_text((84, 100), suffix, fontname='china-s', fontsize=11)
                page.insert_text((132, 100), 'b', fontname='symb', fontsize=11)
                page.insert_text((142, 100), 'x', fontsize=11)
                page.insert_text((151, 100), 'c', fontname='symb', fontsize=11)
                page.insert_text((161, 100), '42', fontsize=11)
                source.save(root / 'source.pdf')
                main.extract_pdf_translation_layout(main.PDFTranslationLayoutRequest(
                    source_path=str(root / 'source.pdf'), output_path=str(root / 'layout.json'),
                ))
                blocks = json.loads((root / 'layout.json').read_text())['blocks']
                self.assertEqual([block['text'] for block in blocks], [prefix, suffix])
                translations = {block['id']: value for block, value in zip(blocks, ['Coefficient', 'is positive'])}
                render_preserving_pages(source, blocks, translations, main.TRANSLATION_FONT_PATH)
                text = source[0].get_text()
                compact = ''.join(text.split())
                self.assertIn('Coefficient', compact)
                self.assertIn('ispositive', compact)
                self.assertIn('x', text)
                self.assertIn('42', text)
                native_symbols = [span['text'].strip() for block in source[0].get_text('dict')['blocks']
                                  for line in block.get('lines', []) for span in line['spans']
                                  if span['font'] == 'Symbol']
                self.assertEqual(native_symbols, ['a', 'b', 'c'])

    def test_extreme_translation_publishes_continuous_pdf(self):
        with tempfile.TemporaryDirectory(dir='/var/lib/lazymind/uploads') as directory:
            root = Path(directory)
            with fitz.open() as source:
                source.new_page(width=300, height=400)
                source.save(root / 'source.pdf')
            (root / 'layout.json').write_text(
                json.dumps(
                    {
                        'blocks': [
                            {
                                'id': 'a',
                                'page': 1,
                                'bbox': [30, 50, 65, 65],
                                'text': 'Source',
                                'styleLevel': 'body',
                            },
                        ]
                    }
                )
            )
            (root / 'translations.json').write_text(
                json.dumps({'a': '完整译文。' * 1000 + '最后一句。'})
            )
            result = main.render_pdf_translation(
                main.PDFTranslationRenderRequest(
                    source_path=str(root / 'source.pdf'),
                    layout_path=str(root / 'layout.json'),
                    translations_path=str(root / 'translations.json'),
                    output_path=str(root / 'output.pdf'),
                )
            )
            self.assertEqual(result['supplemental_block_count'], 0)
            with fitz.open(root / 'output.pdf') as d:
                self.assertIn(
                    '最后一句。', ''.join(p.get_text() for p in d).replace('\n', '')
                )
                self.assertGreater(len(d), 1)


if __name__ == '__main__':
    unittest.main()
