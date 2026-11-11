import unittest
import fitz
from pdf_image_preservation import (
    image_signature, assert_images_preserved, redact_text_preserving_images,
)


class ImagePreservationTests(unittest.TestCase):
    def make_page(self, doc, clipped=False):
        page = doc.new_page(width=200, height=200)
        pixels = fitz.Pixmap(fitz.csRGB, fitz.IRect(0, 0, 10, 10), False)
        pixels.clear_with(100)
        page.insert_image(fitz.Rect(50, 50, 100, 100), pixmap=pixels)
        if clipped:
            xref = page.get_contents()[-1]
            doc.update_stream(xref, b'q 0 0 0 0 re W n\n' + doc.xref_stream(xref) + b'\nQ')
        page.insert_text((20, 150), 'Remove this text')
        return page

    def test_redaction_accepts_invisible_image_cleanup(self):
        with fitz.open() as doc:
            page = self.make_page(doc, clipped=True)
            self.assertEqual(len(image_signature(page)), 1)
            page.add_redact_annot(page.search_for('Remove this text')[0], fill=False)
            redact_text_preserving_images(page)
            self.assertEqual(image_signature(doc[0]), [])
            self.assertNotIn('Remove this text', page.get_text())

    def test_visible_image_is_retained(self):
        with fitz.open() as doc:
            page = self.make_page(doc)
            before = image_signature(page)
            page.add_redact_annot(page.search_for('Remove this text')[0], fill=False)
            redact_text_preserving_images(page)
            self.assertEqual(before, image_signature(doc[0]))

    def test_real_image_loss_is_rejected_despite_page_cache(self):
        with fitz.open() as doc:
            page = self.make_page(doc)
            before = image_signature(page)
            original = page.get_pixmap(matrix=fitz.Matrix(2, 2), annots=False)
            doc.update_stream(page.get_contents()[0], b'')
            with self.assertRaisesRegex(ValueError, 'original images changed on page 1'):
                assert_images_preserved(before, original, page)

    def test_image_movement_is_rejected(self):
        with fitz.open() as doc:
            page = self.make_page(doc)
            before = image_signature(page)
            original = page.get_pixmap(matrix=fitz.Matrix(2, 2), annots=False)
            xref = page.get_contents()[0]
            doc.update_stream(xref, b'q 1 0 0 1 20 0 cm\n' + doc.xref_stream(xref) + b'\nQ')
            with self.assertRaisesRegex(ValueError, 'original images changed on page 1'):
                assert_images_preserved(before, original, page)


if __name__ == '__main__':
    unittest.main()
