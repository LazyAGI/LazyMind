import unittest
import fitz
import main
from translation_pdf import render_preserving_pages, visible
from translation_layout import expanded_boxes


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
            p.insert_text((50, 100), "Short source", fontsize=11)
            source = fitz.Rect(p.get_text("blocks")[0][:4])
            translated = (
                "这一段译文比原文更长，需要利用段落右侧和下方的空白，全文留在这一页。"
                * 5
            )
            result = self.render(
                doc, [self.block("a", source, "Short source")], {"a": translated}
            )
            self.assertEqual(len(doc), 1)
            self.assertEqual(doc[0].get_links(), [])
            self.assertEqual(result["supplemental_block_count"], 0)
            self.assertGreater(result["expanded_block_count"], 0)
            self.assertIn(visible(translated), visible(doc[0].get_text()))
            self.assertGreaterEqual(result["minimum_font_size"], 8)

    def test_neighbouring_column_images_formula_and_table_stay_unchanged(self):
        with fitz.open() as doc:
            p = doc.new_page(width=595, height=842)
            p.insert_text((50, 100), "Left paragraph", fontsize=11)
            p.insert_text((320, 100), "Right paragraph", fontsize=11)
            p.insert_text((50, 200), "E = mc2", fontsize=12)
            p.draw_rect(fitz.Rect(50, 250, 250, 320), color=(0, 0, 0))
            p.draw_line((50, 285), (250, 285), color=(0, 0, 0))
            p.insert_text((60, 275), "Table cell 42.5", fontsize=10)
            p.insert_image(
                fitz.Rect(320, 250, 450, 340), filename="/app/assets/lazymind-logo.png"
            )
            regions = [
                p.search_for("E = mc2")[0],
                fitz.Rect(50, 250, 250, 320),
                fitz.Rect(320, 250, 450, 340),
            ]
            pixels = [p.get_pixmap(clip=r).samples for r in regions]
            blocks = [
                self.block("left", [50, 85, 250, 135], "Left paragraph"),
                self.block("right", [320, 85, 520, 135], "Right paragraph"),
            ]
            self.render(
                doc, blocks, {"left": "左栏译文。" * 30, "right": "右栏译文。" * 30}
            )
            for r, before in zip(regions, pixels):
                self.assertEqual(before, doc[0].get_pixmap(clip=r).samples)
            for label, box in [
                ("左栏译文", fitz.Rect(45, 80, 310, 180)),
                ("右栏译文", fitz.Rect(315, 80, 580, 180)),
            ]:
                self.assertIn(label, doc[0].get_text(clip=box))
            self.assertEqual(len(doc), 1)

    def test_table_cells_are_not_flattened_into_prose(self):
        with fitz.open() as doc:
            p = doc.new_page()
            for y in [100, 125, 150]:
                p.draw_line((50, y), (300, y))
            p.insert_text((60, 120), "Model 42.5")
            box = fitz.Rect(45, 95, 305, 155)
            before = p.get_pixmap(clip=box).samples
            result = self.render(
                doc,
                [self.block("cell", [60, 105, 200, 123], "Model 42.5")],
                {"cell": "模型 42.5"},
            )
            self.assertEqual(before, doc[0].get_pixmap(clip=box).samples)
            self.assertEqual(result["preserved_visual_block_ids"], ["cell"])
            self.assertEqual(len(doc), 1)

    def test_impossible_translation_fails_before_mutating_source(self):
        with fitz.open() as doc:
            p = doc.new_page(width=150, height=150)
            p.insert_text((30, 70), "Source")
            before = p.get_pixmap().samples
            with self.assertRaisesRegex(ValueError, "无法在可读字号下排入原页"):
                self.render(
                    doc,
                    [self.block("huge", [30, 55, 100, 75], "Source")],
                    {"huge": "完整译文。" * 5000},
                )
            self.assertEqual(len(doc), 1)
            self.assertEqual(before, doc[0].get_pixmap().samples)

    def test_missing_translation_fails(self):
        with fitz.open() as doc:
            doc.new_page()
            with self.assertRaisesRegex(ValueError, "missing translation"):
                self.render(
                    doc, [self.block("missing", [50, 50, 250, 100], "Source")], {}
                )

    def test_expansion_does_not_cross_other_column(self):
        source = fitz.Rect(50, 50, 200, 70)
        other = fitz.Rect(300, 50, 500, 500)
        boxes = expanded_boxes(source, fitz.Rect(0, 0, 595, 842), [other])
        self.assertTrue(all(not (b & other).get_area() for b in boxes))
        self.assertTrue(any(b.width > source.width for b in boxes))

    def test_rotated_chart_label_remains_rotated(self):
        with fitz.open() as doc:
            p = doc.new_page()
            p.insert_text((25, 400), "arXiv:1706.03762", fontsize=10, rotate=90)
            bbox = list(p.get_text("blocks")[0][:4])
            self.render(
                doc,
                [self.block("rotated", bbox, "arXiv:1706.03762")],
                {"rotated": "预印本编号 1706.03762"},
            )
            lines = [
                l for b in doc[0].get_text("dict")["blocks"] for l in b.get("lines", [])
            ]
            self.assertEqual(tuple(lines[0]["dir"]), (0, -1))


if __name__ == "__main__":
    unittest.main()
