import unicodedata
import unittest
from pathlib import Path
import fitz
import main
from translation_pdf import render_preserving_pages, visible
from translation_layout import expanded_boxes
from translation_continuation import continue_in_place


class PreservationTests(unittest.TestCase):
    def render(self, doc, blocks, translations):
        return render_preserving_pages(
            doc, blocks, translations, main.TRANSLATION_FONT_PATH
        )

    def block(self, key, rect, text, size=11):
        return dict(id=key, page=1, bbox=list(rect), text=text, fontSize=size)

    def test_uses_blank_space_without_extra_pages_or_links(self):
        with fitz.open() as doc:
            p = doc.new_page(width=595, height=842)
            p.insert_text((50, 100), 'Short source', fontsize=11)
            source = fitz.Rect(p.get_text('blocks')[0][:4])
            translated = (
                '这一段译文比原文更长，需要利用段落右侧和下方的空白，全文留在这一页。'
                * 5
            )
            result = self.render(
                doc, [self.block('a', source, 'Short source')], {'a': translated}
            )
            self.assertEqual(len(doc), 1)
            self.assertEqual(doc[0].get_links(), [])
            self.assertEqual(result['supplemental_block_count'], 0)
            self.assertGreater(result['expanded_block_count'], 0)
            self.assertIn(visible(translated), visible(doc[0].get_text()))
            self.assertGreaterEqual(result['minimum_font_size'], 8)

    def test_neighbouring_column_images_formula_and_table_stay_unchanged(self):
        with fitz.open() as doc:
            p = doc.new_page(width=595, height=842)
            p.insert_text((50, 100), 'Left paragraph', fontsize=11)
            p.insert_text((320, 100), 'Right paragraph', fontsize=11)
            p.insert_text((50, 200), 'E = mc2', fontsize=12)
            p.draw_rect(fitz.Rect(50, 250, 250, 320), color=(0, 0, 0))
            p.draw_line((50, 285), (250, 285), color=(0, 0, 0))
            p.insert_text((60, 275), 'Table cell 42.5', fontsize=10)
            p.insert_image(
                fitz.Rect(320, 250, 450, 340), filename=str(Path(__file__).parent / 'assets' / 'lazymind-logo.png')
            )
            regions = [
                p.search_for('E = mc2')[0],
                fitz.Rect(50, 250, 250, 320),
                fitz.Rect(320, 250, 450, 340),
            ]
            pixels = [p.get_pixmap(clip=r).samples for r in regions]
            blocks = [
                self.block('left', [50, 85, 250, 135], 'Left paragraph'),
                self.block('right', [320, 85, 520, 135], 'Right paragraph'),
            ]
            self.render(
                doc, blocks, {'left': '左栏译文。' * 30, 'right': '右栏译文。' * 30}
            )
            for r, before in zip(regions, pixels):
                self.assertEqual(before, doc[0].get_pixmap(clip=r).samples)
            for label, box in [
                ('左栏译文', fitz.Rect(45, 80, 310, 180)),
                ('右栏译文', fitz.Rect(315, 80, 580, 180)),
            ]:
                self.assertIn(label, doc[0].get_text(clip=box))
            self.assertEqual(len(doc), 1)

    def test_table_cells_are_not_flattened_into_prose(self):
        with fitz.open() as doc:
            p = doc.new_page()
            for y in [100, 125, 150]:
                p.draw_line((50, y), (300, y))
            p.insert_text((60, 120), 'Model 42.5')
            box = fitz.Rect(45, 95, 305, 155)
            before = p.get_pixmap(clip=box).samples
            result = self.render(
                doc,
                [self.block('cell', [60, 105, 200, 123], 'Model 42.5')],
                {'cell': '模型 42.5'},
            )
            self.assertEqual(before, doc[0].get_pixmap(clip=box).samples)
            self.assertEqual(result['preserved_visual_block_ids'], ['cell'])
            self.assertEqual(len(doc), 1)

    def test_extreme_translation_continues_before_following_content(self):
        with fitz.open() as doc:
            p = doc.new_page(width=300, height=400)
            p.insert_text((30, 50), 'BEFORE')
            p.insert_text((30, 100), 'Source')
            p.insert_text((30, 200), 'AFTER')
            p.draw_rect(fitz.Rect(30, 230, 120, 280), color=(0, 0, 0))
            doc.new_page(width=300, height=400).insert_text((30, 50), 'NEXT PAGE')
            translation = '完整译文。' * 5000 + '这是最后一句。'
            result = self.render(
                doc,
                [self.block('huge', [30, 85, 150, 105], 'Source')],
                {'huge': translation},
            )
            text = unicodedata.normalize('NFKC', ''.join(p.get_text() for p in doc))
            self.assertIn(visible(translation), visible(text))
            self.assertLess(text.index('BEFORE'), text.index('完整译文'))
            self.assertLess(text.index('这是最后一句'), text.index('AFTER'))
            self.assertLess(text.index('AFTER'), text.index('NEXT PAGE'))
            self.assertEqual(text.count('BEFORE'), 1)
            self.assertEqual(text.count('AFTER'), 1)
            self.assertNotIn('补充译文', text)
            self.assertGreater(result['continuation_page_count'], 0)
            self.assertEqual(result['supplemental_block_count'], 0)
            self.assertTrue(all(not p.get_links() for p in doc))
            self.assertTrue(any(p.get_drawings() for p in doc))

    def test_continuation_uses_both_columns_and_preserves_following_paragraph(self):
        with fitz.open() as doc:
            p = doc.new_page(width=595, height=842)
            entries = [
                ('long', [30, 85, 250, 105], 'Source'),
                ('left2', [30, 200, 250, 220], 'Left follow'),
                ('right1', [320, 85, 550, 105], 'Right first'),
                ('right2', [320, 200, 550, 220], 'Right follow'),
            ]
            blocks = []
            translations = {}
            for key, box, text in entries:
                p.insert_text((box[0], box[1] + 12), text, fontsize=11)
                blocks.append(self.block(key, box, text))
                translations[key] = text
            translations['long'] = '连续译文。' * 800 + '结束。'
            result = self.render(doc, blocks, translations)
            right_chinese = [
                s
                for b in doc[0].get_text('dict')['blocks']
                for line in b.get('lines', [])
                for s in line['spans']
                if s['bbox'][0] > 300 and '译文' in s['text']
            ]
            self.assertTrue(right_chinese)
            content = visible(''.join(p.get_text() for p in doc))
            self.assertIn(visible(translations['long']), content)
            self.assertEqual(content.count('Rightfollow'), 1)
            self.assertGreater(result['continuation_page_count'], 0)

    def test_continuation_starts_new_page_when_neither_column_has_room(self):
        with fitz.open() as doc:
            page = doc.new_page(width=595, height=400)
            page.insert_text((30, 382), 'PRECEDING', fontsize=11)
            item = dict(id='long', page=0, rect=fitz.Rect(30, 390, 250, 399),
                        size=11, text='FIRSTLINE ' + 'word ' * 100)
            items = [item] + [
                dict(id=key, page=0, rect=fitz.Rect(box))
                for key, box in [
                    ('left', [30, 50, 250, 70]),
                    ('right1', [320, 50, 550, 70]),
                    ('right2', [320, 100, 550, 120]),
                ]
            ]
            continue_in_place(doc, [item], items, [fitz.Font('helv')])
            self.assertNotIn('FIRSTLINE', doc[0].get_text())
            self.assertIn('FIRSTLINE', doc[1].get_text())
            for page in doc:
                for block in page.get_text('dict')['blocks']:
                    for line in block.get('lines', []):
                        for span in line['spans']:
                            self.assertLessEqual(span['bbox'][3], page.rect.height)

    def test_continuation_remaps_existing_internal_links_and_outline(self):
        with fitz.open() as doc:
            doc.new_page(width=300, height=400)
            doc.new_page(width=300, height=400)
            doc[0].insert_text((30, 50), 'BEFORE')
            doc[0].insert_link({'kind': fitz.LINK_GOTO, 'from': fitz.Rect(30, 30, 100, 60),
                                'page': 1, 'to': fitz.Point(30, 50)})
            doc[1].insert_text((30, 50), 'TARGET')
            doc.set_toc([[1, 'Target', 2]])
            item = dict(id='a', page=0, rect=fitz.Rect(30, 100, 200, 120),
                        size=11, text='word ' * 2000)
            continue_in_place(doc, [item], [item], [fitz.Font('helv')])
            link = doc[0].get_links()[0]
            self.assertEqual(link['page'], len(doc) - 1)
            self.assertIn('TARGET', doc[link['page']].get_text())
            self.assertEqual(doc.get_toc(), [[1, 'Target', len(doc)]])

    def test_continuation_packs_source_fragments_across_safe_whitespace(self):
        with fitz.open() as doc:
            page = doc.new_page(width=300, height=400)
            for y, text in [(50, 'BEFORE'), (150, 'FIRST'), (350, 'LAST')]:
                page.insert_text((30, y), text)
            page.insert_link({'kind': fitz.LINK_GOTO, 'from': fitz.Rect(30, 138, 80, 154),
                              'page': 0, 'to': fitz.Point(30, 350)})
            doc.set_toc([[1, 'Last', 1, {'kind': fitz.LINK_GOTO, 'page': 0, 'to': fitz.Point(30, 350)}]])
            item = dict(id='a', page=0, rect=fitz.Rect(30, 80, 200, 100),
                        size=11, text='word ' * 100)
            continue_in_place(doc, [item], [item], [fitz.Font('helv')])
            self.assertEqual(len(doc), 1)
            text = doc[0].get_text()
            self.assertEqual(text.count('FIRST'), 1)
            self.assertEqual(text.count('LAST'), 1)
            self.assertLess(text.index('FIRST'), text.index('LAST'))
            last_box = doc[0].search_for('LAST')[0]
            link = doc[0].get_links()[0]
            self.assertEqual(link['page'], 0)
            self.assertTrue(last_box.contains(link['to']))
            self.assertTrue(last_box.contains(doc.get_toc(simple=False)[0][3]['to']))

    def test_continuation_does_not_split_whitespace_crossed_by_an_image(self):
        with fitz.open() as doc:
            page = doc.new_page(width=300, height=400)
            for y, text in [(50, 'BEFORE'), (150, 'FIRST'), (350, 'LAST')]:
                page.insert_text((30, y), text)
            region = fitz.Rect(150, 120, 180, 370)
            page.insert_image(region, filename=str(Path(__file__).parent / 'assets' / 'lazymind-logo.png'),
                              keep_proportion=False)
            image = page.get_image_info(hashes=True)[0]
            first_top = page.search_for('FIRST')[0].y0
            item = dict(id='a', page=0, rect=fitz.Rect(30, 80, 200, 100),
                        size=11, text='word ' * 100)
            continue_in_place(doc, [item], [item], [fitz.Font('helv')])
            self.assertEqual(len(doc), 2)
            self.assertNotIn('FIRST', doc[0].get_text())
            shift = doc[1].search_for('FIRST')[0].y0 - first_top
            moved = region + (0, shift, 0, shift)
            self.assertTrue(doc[1].rect.contains(moved))
            retained = next(info for info in doc[1].get_image_info(hashes=True)
                            if fitz.Rect(info['bbox']).intersects(moved))
            self.assertEqual(image['digest'], retained['digest'])
            for before, after in zip(image['transform'][:4], retained['transform'][:4]):
                self.assertAlmostEqual(before, after, places=4)
            for before, after in zip(moved, retained['bbox']):
                self.assertAlmostEqual(before, after, places=4)

    def test_continuation_keeps_fitted_paragraph_atomic_and_maps_actual_bounds(self):
        with fitz.open() as doc:
            page = doc.new_page(width=300, height=400)
            page.insert_text((30, 50), 'BEFORE')
            page.insert_text((30, 150), 'REFERENCE START')
            page.insert_text((30, 350), 'REFERENCE TAIL')
            overflow = dict(id='a', page=0, rect=fitz.Rect(30, 80, 200, 100),
                            size=11, text='word ' * 100)
            fitted = dict(id='reference', page=0, rect=fitz.Rect(30, 130, 200, 155),
                          fit={'rect': fitz.Rect(30, 138, 200, 354)})
            mapping = continue_in_place(doc, [overflow], [overflow, fitted], [fitz.Font('helv')])
            self.assertEqual(len(doc), 2)
            pages = next(item['pages'] for item in mapping if item['block_id'] == 'reference')
            self.assertEqual(pages, [2])
            self.assertNotIn('REFERENCE', doc[0].get_text())
            self.assertIn('REFERENCE START', doc[1].get_text())
            self.assertIn('REFERENCE TAIL', doc[1].get_text())

    def test_missing_translation_preserves_source_and_reports_diagnostic(self):
        with fitz.open() as doc:
            doc.new_page().insert_text((50, 70), 'Source')
            result = self.render(doc, [self.block('missing', [50, 50, 250, 100], 'Source')], {})
            self.assertIn('Source', doc[0].get_text())
            self.assertIn('missing_translation', [d['code'] for d in result['diagnostics']])

    def test_expansion_does_not_cross_other_column(self):
        source = fitz.Rect(50, 50, 200, 70)
        other = fitz.Rect(300, 50, 500, 500)
        boxes = expanded_boxes(source, fitz.Rect(0, 0, 595, 842), [other])
        self.assertTrue(all(not (b & other).get_area() for b in boxes))
        self.assertTrue(any(b.width > source.width for b in boxes))

    def test_rotated_chart_label_remains_rotated(self):
        with fitz.open() as doc:
            p = doc.new_page()
            p.insert_text((25, 400), 'arXiv:1706.03762', fontsize=10, rotate=90)
            bbox = list(p.get_text('blocks')[0][:4])
            self.render(
                doc,
                [self.block('rotated', bbox, 'arXiv:1706.03762')],
                {'rotated': '预印本编号 1706.03762'},
            )
            lines = [
                line for b in doc[0].get_text('dict')['blocks'] for line in b.get('lines', [])
            ]
            self.assertEqual(tuple(lines[0]['dir']), (0, -1))


if __name__ == '__main__':
    unittest.main()
