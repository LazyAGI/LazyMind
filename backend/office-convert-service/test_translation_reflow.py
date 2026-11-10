import json
import tempfile
import unittest
from pathlib import Path
import fitz
import main


class TranslationReflowTests(unittest.TestCase):
    def test_short_first_page_title_is_not_discarded_as_author_metadata(self):
        with tempfile.TemporaryDirectory(dir="/var/lib/lazymind/uploads") as directory:
            root = Path(directory)
            with fitz.open() as source:
                page = source.new_page(width=595, height=842)
                page.insert_text((120, 200), "Attention Is All You Need", fontsize=14)
                source.save(root / "source.pdf")
            main.extract_pdf_translation_layout(
                main.PDFTranslationLayoutRequest(
                    source_path=str(root / "source.pdf"),
                    output_path=str(root / "layout.json"),
                )
            )
            blocks = json.loads((root / "layout.json").read_text())["blocks"]
            self.assertTrue(
                any("Attention Is All You Need" in block["text"] for block in blocks)
            )

    def test_extreme_translation_publishes_continuous_pdf(self):
        with tempfile.TemporaryDirectory(dir="/var/lib/lazymind/uploads") as directory:
            root = Path(directory)
            with fitz.open() as source:
                source.new_page(width=300, height=400)
                source.save(root / "source.pdf")
            (root / "layout.json").write_text(
                json.dumps(
                    {
                        "blocks": [
                            {
                                "id": "a",
                                "page": 1,
                                "bbox": [30, 50, 65, 65],
                                "text": "Source",
                                "styleLevel": "body",
                            },
                        ]
                    }
                )
            )
            (root / "translations.json").write_text(
                json.dumps({"a": "完整译文。" * 1000 + "最后一句。"})
            )
            result = main.render_pdf_translation(
                main.PDFTranslationRenderRequest(
                    source_path=str(root / "source.pdf"),
                    layout_path=str(root / "layout.json"),
                    translations_path=str(root / "translations.json"),
                    output_path=str(root / "output.pdf"),
                )
            )
            self.assertEqual(result["supplemental_block_count"], 0)
            with fitz.open(root / "output.pdf") as d:
                self.assertIn(
                    "最后一句。", "".join(p.get_text() for p in d).replace("\n", "")
                )
                self.assertGreater(len(d), 1)


if __name__ == "__main__":
    unittest.main()
