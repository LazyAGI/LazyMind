import unittest
from unittest.mock import patch
import fitz
import main
from translation_pdf import render_preserving_pages


class DiagnosticsTests(unittest.TestCase):
    def render(self, translated="译文"):
        with fitz.open() as doc:
            doc.new_page().insert_text((50, 70), "Source")
            result = render_preserving_pages(doc, [dict(id="a", page=1, bbox=[50, 55, 200, 80], text="Source")], {"a": translated}, main.TRANSLATION_FONT_PATH)
            self.assertTrue(doc.tobytes().startswith(b"%PDF"))
            return result

    def test_image_and_vector_checks_are_advisory(self):
        with patch("translation_pdf.redact_text_preserving_images", side_effect=ValueError("original images changed")), patch("translation_pdf.graphics_signature", side_effect=[[], ["changed"]]):
            result = self.render()
        self.assertFalse(result["layout_preserved"])
        self.assertTrue({"image_preservation", "vector_graphics_changed"}.issubset({d["code"] for d in result["diagnostics"]}))

    def test_text_checks_do_not_block_output(self):
        with patch("translation_pdf.paint"):
            result = self.render()
        self.assertTrue({"translation_extraction_mismatch", "translated_text_mismatch"}.issubset({d["code"] for d in result["diagnostics"]}))

    def test_protected_source_check_is_advisory(self):
        from translation_layout import source_spans
        def extra_span(page):
            return source_spans(page) + [dict(text="\u0308", bbox=(400, 400, 400, 410), protected=False)]
        with patch("translation_pdf.source_spans", side_effect=extra_span):
            result = self.render()
        self.assertIn("protected_source_text_missing", [d["code"] for d in result["diagnostics"]])

    def test_continuation_check_is_advisory(self):
        with patch("translation_continuation.paint"):
            result = self.render("连续译文。" * 5000)
        self.assertIn("continuation_text_mismatch", [d["code"] for d in result["diagnostics"]])

    def test_unsupported_character_does_not_block_output(self):
        result = self.render("译文" + chr(0x10FFFF))
        self.assertIn("unsupported_characters", [d["code"] for d in result["diagnostics"]])

    def test_invalid_layout_does_not_block_output(self):
        with fitz.open() as doc:
            doc.new_page().insert_text((50, 70), "Source")
            blocks = [dict(id="a", page=99, bbox=[0, 0, 1, 1]), dict(id="a", page=1, bbox=[0, 0, 1, 1]), dict(id="b", page=1, bbox=[-30, -30, -1, -1])]
            result = render_preserving_pages(doc, blocks, {"a": "译文", "b": "译文"}, main.TRANSLATION_FONT_PATH)
            self.assertIn("Source", doc[0].get_text())
            self.assertEqual(result["rendered_block_count"], 0)
            self.assertEqual(result["requested_block_count"], 3)
            self.assertEqual(len(result["diagnostics"]), 4)

if __name__ == "__main__":
    unittest.main()
