"""Exercise real resume files, image layout and content-preserving pagination."""
import io
import json
from pathlib import Path
import re
import tempfile
import unittest
import zipfile

import fitz
from PIL import Image

from lazymind.document_tools.resume.renderer import TEMPLATES, render_resume_files


def example_resume():
    return {
        'basics': {'name': '陈样例', 'title': '软件测试工程师',
                   'email': 'candidate@example.invalid', 'location': '北京',
                   'links': [{'label': '作品', 'url': 'https://example.invalid/resume'}]},
        'summary': '参与接口自动化、性能与知识库质量评测。',
        'sections': [
            {'title': '教育背景', 'items': [{'title': '星城理工大学', 'subtitle': '软件工程本科',
                                         'date': '2023.09–2027.06', 'bullets': ['GPA 3.62/4.0']}]},
            {'title': '专业技能', 'items': [{'title': 'Python / pytest', 'bullets': ['接口测试与SQL验证']} ]},
            {'title': '项目经历', 'items': [{'title': '自动化测试平台', 'date': '2026.01–2026.02',
                                         'bullets': ['维护380条接口用例，发现55个有效缺陷。',
                                                     '末尾完整性 END_SENTINEL_123']} ]},
        ],
    }


class ResumeRendererTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='resume-render-test-')
        self.root = Path(self.temporary.name)

    def tearDown(self):
        self.temporary.cleanup()

    def files(self, result):
        return {file['format']: Path(file['path']) for file in result['files']}

    def assert_content(self, paths):
        with fitz.open(paths['pdf']) as pdf:
            text = ''.join(page.get_text() for page in pdf)
        self.assertIn('陈样例', text)
        self.assertIn('380', text)
        self.assertIn('END_SENTINEL_123', text)
        with zipfile.ZipFile(paths['docx']) as word:
            xml = word.read('word/document.xml').decode()
        self.assertIn('陈样例', xml)
        self.assertIn('END_SENTINEL_123', xml)

    def test_all_six_templates_create_usable_files_and_distinct_layouts(self):
        name_layouts = []
        for template in TEMPLATES:
            with self.subTest(template=template):
                result = render_resume_files(example_resume(), str(self.root / template), template)
                paths = self.files(result)
                self.assertEqual(set(paths), {'pdf', 'docx', 'html'})
                self.assertEqual(result['page_count'], 1)
                self.assert_content(paths)
                with fitz.open(paths['pdf']) as pdf:
                    self.assertEqual(sum(len(page.get_images()) for page in pdf), 0)
                    spans = [span for block in pdf[0].get_text('dict')['blocks'] if 'lines' in block
                             for line in block['lines'] for span in line['spans'] if '陈样例' in span['text']]
                    name_layouts.append((round(spans[0]['bbox'][0]), round(spans[0]['bbox'][1]),
                                         round(spans[0]['size'])))
                self.assertNotIn('class="portrait"', paths['html'].read_text())
        self.assertGreater(len(set(name_layouts)), 2)

    def test_long_resume_keeps_every_entry_and_dates_across_pages(self):
        for template in ('business', 'sidebar', 'editorial'):
            with self.subTest(template=template):
                resume = example_resume()
                resume['sections'][-1]['items'] = [
                    {'title': f'项目经历 ENTRY_{index:03d}', 'date': f'2025.{index % 12 + 1:02d}–2026.01',
                     'bullets': [f'UNIQUE_{index:03d} 使用Python进行接口、数据与异常输入测试。' * 3]}
                    for index in range(35)
                ]
                result = render_resume_files(resume, str(self.root / template), template)
                self.assertGreater(result['page_count'], 1)
                with fitz.open(self.files(result)['pdf']) as pdf:
                    text = ''.join(page.get_text() for page in pdf)
                    for index in range(35):
                        self.assertIn(f'ENTRY_{index:03d}', text)
                        self.assertIn(f'UNIQUE_{index:03d}', text)
                    self.assertIn('2025.01–2026.01', text)
                    self.assertTrue(all(str(index + 1) + ' / ' in page.get_text()
                                        for index, page in enumerate(pdf)))

    def test_uploaded_landscape_and_portrait_photos_are_embedded_without_stretching(self):
        for size in ((800, 300), (300, 800)):
            with self.subTest(size=size):
                photo = self.root / f'photo-{size[0]}.png'
                Image.new('RGB', size, '#bd3d34').save(photo)
                for template in TEMPLATES:
                    result = render_resume_files(example_resume(), str(self.root / f'{template}-{size[0]}'),
                                                 template, str(photo))
                    paths = self.files(result)
                    self.assertTrue(result['has_photo'])
                    self.assertIn('data:image/png;base64,', paths['html'].read_text())
                    with fitz.open(paths['pdf']) as pdf:
                        self.assertGreater(sum(len(page.get_images()) for page in pdf), 0)
                        for page in pdf:
                            for image in page.get_image_info():
                                photo_rect = fitz.Rect(image['bbox'])
                                self.assertGreaterEqual(photo_rect.x0, 40)
                                self.assertLessEqual(photo_rect.x1, page.rect.width - 40)
                                for block in page.get_text('dict')['blocks']:
                                    for line in block.get('lines', []):
                                        for span in line['spans']:
                                            self.assertFalse(photo_rect.intersects(fitz.Rect(span['bbox'])))
                    with zipfile.ZipFile(paths['docx']) as word:
                        images = [name for name in word.namelist() if name.startswith('word/media/')]
                        self.assertEqual(len(images), 1)
                        normalized = Image.open(io.BytesIO(word.read(images[0])))
                        self.assertEqual(normalized.size, (540, 720))
                        self.assertEqual(normalized.getpixel((0, 0)), (255, 255, 255))

    def test_sidebar_continues_long_skills_and_main_history_without_losing_either_column(self):
        resume = example_resume()
        resume['sections'][1]['items'][0]['bullets'] = [
            f'SIDESKILL_{index:03d} 使用Python实现接口、数据校验与日志分析，保留全部技能事实。'
            for index in range(90)
        ]
        resume['sections'][-1]['items'] = [
            {'title': f'超长公司名称与岗位职责边界准确保留的测试开发实习项目 ENTRY_{index:03d}',
             'date': '2025年09月 – 2026年10月',
             'bullets': [f'MAIN_{index:03d} 使用Python实现接口回归，验证权限与业务数据的一致性。' * 5]}
            for index in range(12)
        ]
        result = render_resume_files(resume, str(self.root), 'sidebar')
        with fitz.open(self.files(result)['pdf']) as pdf:
            text = ''.join(page.get_text() for page in pdf)
            self.assertGreater(len(pdf), 1)
            for index in range(90):
                self.assertIn(f'SIDESKILL_{index:03d}', text)
            for index in range(12):
                self.assertIn(f'MAIN_{index:03d}', text)

    def test_section_and_first_project_move_together_without_a_title_only_page(self):
        # Captured fictional input from the real chat path: both the project
        # section title and first project originally ended near page one's foot.
        # Independent forced breaks incorrectly created a third, title-only page.
        fixture = Path(__file__).parents[1] / 'fixtures' / 'resume' / 'sidebar-pagination.json'
        resume = json.loads(fixture.read_text())
        photo = self.root / 'portrait.png'
        Image.new('RGB', (150, 200), '#cddfd7').save(photo)
        result = render_resume_files(resume, str(self.root / 'actual'), 'sidebar', str(photo))
        self.assertEqual(result['page_count'], 2)
        with fitz.open(self.files(result)['pdf']) as pdf:
            pages = [page.get_text() for page in pdf]
            self.assertTrue(all(len(text) > 200 for text in pages))
            self.assertIn('项目经历', pages[1])
            self.assertIn(resume['sections'][2]['items'][0]['title'], pages[1])
            self.assertGreater(sum(len(page.get_images()) for page in pdf), 0)
        text = re.sub(r'\s+', '', ''.join(pages)).replace('ﬁ', 'fi').replace('ﬂ', 'fl')
        for section in resume['sections']:
            for item in section['items']:
                for value in [item.get('title', ''), item.get('subtitle', ''), item.get('date', ''),
                              *item.get('bullets', [])]:
                    self.assertIn(re.sub(r'\s+', '', value), text)

    def test_identical_title_and_subtitle_appear_once_in_all_three_formats(self):
        resume = example_resume()
        resume['sections'][1]['items'][0].update(title='测试工具与框架', subtitle='测试工具与框架')
        paths = self.files(render_resume_files(resume, str(self.root)))
        with fitz.open(paths['pdf']) as pdf:
            self.assertEqual(''.join(page.get_text() for page in pdf).count('测试工具与框架'), 1)
        with zipfile.ZipFile(paths['docx']) as word:
            self.assertEqual(word.read('word/document.xml').decode().count('测试工具与框架'), 1)
        self.assertEqual(paths['html'].read_text().count('测试工具与框架'), 1)

    def test_cover_photo_fills_frame_and_exif_orientation_is_applied(self):
        photo = self.root / 'exif-photo.jpg'
        image = Image.new('RGB', (800, 300), '#bd3d34')
        exif = Image.Exif()
        exif[274] = 6
        image.save(photo, exif=exif)
        for fit in ('contain', 'cover'):
            result = render_resume_files(example_resume(), str(self.root / fit), photo_path=str(photo), photo_fit=fit)
            with zipfile.ZipFile(self.files(result)['docx']) as word:
                name = next(name for name in word.namelist() if name.startswith('word/media/'))
                normalized = Image.open(io.BytesIO(word.read(name)))
                self.assertEqual(normalized.size, (540, 720))
                self.assertLess(normalized.getpixel((270, 20))[0], 240)
                self.assertEqual(normalized.getpixel((0, 0)) == (255, 255, 255), fit == 'contain')

    def test_user_text_is_escaped_and_external_resources_cannot_be_injected(self):
        resume = example_resume()
        resume['summary'] = '<script>alert(1)</script><img src="https://private.invalid/pixel">'
        result = render_resume_files(resume, str(self.root))
        rendered = self.files(result)['html'].read_text()
        self.assertNotIn('<script>', rendered)
        self.assertNotIn('<img src="https://', rendered)
        self.assertIn('&lt;script&gt;', rendered)
        self.assertIn('Content-Security-Policy', rendered)

    def test_invalid_template_path_and_image_fail_instead_of_creating_fake_files(self):
        for arguments in ({'template': 'unknown'}, {'filename': '../resume'}, {'photo_fit': 'stretch'}):
            with self.subTest(arguments=arguments), self.assertRaises(ValueError):
                render_resume_files(example_resume(), str(self.root), **arguments)
        photo = self.root / 'not-a-photo.png'
        photo.write_text('<svg><script>alert(1)</script></svg>')
        with self.assertRaises(ValueError):
            render_resume_files(example_resume(), str(self.root), photo_path=str(photo))


if __name__ == '__main__':
    unittest.main()
