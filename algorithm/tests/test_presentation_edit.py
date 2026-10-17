from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import unittest
import zipfile


MODULE_PATH = (
    Path(__file__).resolve().parents[1]
    / 'lazymind/chat/engine/tools/file_resources/presentation_edit.py'
)
SPEC = importlib.util.spec_from_file_location('presentation_edit_test_module', MODULE_PATH)
PRESENTATION_EDIT = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(PRESENTATION_EDIT)


def write_test_pptx(path: Path) -> None:
    with zipfile.ZipFile(path, 'w') as archive:
        archive.writestr('[Content_Types].xml', '<Types/>')
        archive.writestr(
            'ppt/presentation.xml',
            '<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>',
        )
        archive.writestr(
            'ppt/slides/slide1.xml',
            '''<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
                 xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
                 <a:rPr><a:latin typeface="Old Font"/><a:solidFill><a:srgbClr val="112233"/></a:solidFill></a:rPr>
               </p:sld>''',
        )
        archive.writestr(
            'ppt/theme/theme1.xml',
            '''<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
                 <a:themeElements><a:fontScheme name="Test"><a:majorFont><a:latin typeface="Old Font"/></a:majorFont></a:fontScheme></a:themeElements>
               </a:theme>''',
        )


def write_scoped_test_pptx(path: Path) -> None:
    slide = '''<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
      xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree>
      <p:sp><p:nvSpPr><p:cNvPr id="2" name="Title 1"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr>
        <p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr><a:latin typeface="Old Font"/></a:rPr><a:t>Title</a:t></a:r></a:p></p:txBody></p:sp>
      <p:sp><p:nvSpPr><p:cNvPr id="3" name="Content 2"/><p:cNvSpPr/><p:nvPr><p:ph type="body"/></p:nvPr></p:nvSpPr>
        <p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr><a:latin typeface="Old Font"/></a:rPr><a:t>Body</a:t></a:r></a:p></p:txBody></p:sp>
      </p:spTree></p:cSld></p:sld>'''
    with zipfile.ZipFile(path, 'w') as archive:
        archive.writestr('[Content_Types].xml', '<Types/>')
        archive.writestr('ppt/presentation.xml', '''<p:presentation
          xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
          xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
          <p:sldIdLst><p:sldId id="256" r:id="rId2"/><p:sldId id="257" r:id="rId1"/></p:sldIdLst>
        </p:presentation>''')
        archive.writestr('ppt/_rels/presentation.xml.rels', '''<Relationships
          xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
          <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/>
          <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide2.xml"/>
        </Relationships>''')
        archive.writestr('ppt/slides/slide1.xml', slide)
        archive.writestr('ppt/slides/slide2.xml', slide)
        archive.writestr('ppt/theme/theme1.xml', '<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:latin typeface="Old Font"/></a:theme>')


class PresentationEditTests(unittest.TestCase):
    def test_inspect_reports_fonts_colors_and_availability(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / 'source.pptx'
            write_test_pptx(source)
            result = PRESENTATION_EDIT.inspect_pptx(source, {'Old Font'})

        self.assertEqual(result['slide_count'], 1)
        self.assertEqual(result['fonts'][0]['family'], 'Old Font')
        self.assertTrue(result['fonts'][0]['available'])
        self.assertEqual(result['colors'][0]['rgb'], '#112233')

    def test_rewrite_replaces_fonts_and_exact_colors_without_touching_other_parts(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / 'source.pptx'
            output = Path(temp) / 'edited.pptx'
            write_test_pptx(source)
            stats = PRESENTATION_EDIT.rewrite_pptx(
                source, output, source_font='Old Font', target_font='New Font',
                color_map={'#112233': '#AABBCC'},
            )
            inspected = PRESENTATION_EDIT.inspect_pptx(output, {'New Font'})

        self.assertEqual(stats['font_replacements'], 2)
        self.assertEqual(stats['color_replacements'], 1)
        self.assertEqual(inspected['fonts'][0]['family'], 'New Font')
        self.assertEqual(inspected['colors'][0]['rgb'], '#AABBCC')

    def test_no_match_does_not_create_a_version(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / 'source.pptx'
            output = Path(temp) / 'edited.pptx'
            write_test_pptx(source)
            with self.assertRaisesRegex(ValueError, 'no new version'):
                PRESENTATION_EDIT.rewrite_pptx(
                    source, output, source_font='Missing', target_font='New Font',
                )
            self.assertFalse(output.exists())

    def test_rewrite_can_limit_font_to_titles_on_selected_pages(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / 'source.pptx'
            output = Path(temp) / 'edited.pptx'
            write_scoped_test_pptx(source)
            stats = PRESENTATION_EDIT.rewrite_pptx(
                source, output, target_font='New Font', pages=[1], text_scope='titles',
            )
            with zipfile.ZipFile(output) as archive:
                slide1 = archive.read('ppt/slides/slide1.xml').decode()
                slide2 = archive.read('ppt/slides/slide2.xml').decode()
                theme = archive.read('ppt/theme/theme1.xml').decode()

        self.assertEqual(stats['pages'], [1])
        self.assertEqual(stats['text_scope'], 'titles')
        self.assertEqual(stats['changed_parts'], ['ppt/slides/slide2.xml'])
        self.assertNotIn('New Font', slide1)
        self.assertIn('New Font', slide2)
        self.assertIn('Old Font', slide2)
        self.assertNotIn('New Font', theme)


if __name__ == '__main__':
    unittest.main()
