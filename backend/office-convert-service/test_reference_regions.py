import unittest
from types import SimpleNamespace

from main import _locate_reference_regions


class FakePage:
    def __init__(self, lines):
        self.rect = SimpleNamespace(width=612, height=792)
        self.lines = lines

    def get_text(self, mode, sort=False):
        assert mode == 'dict'
        return {'blocks': [{'type': 0, 'lines': [
            {'bbox': bbox, 'spans': [{'text': text}]}
            for text, bbox in self.lines
        ]}]}


class ReferenceRegionTests(unittest.TestCase):
    def test_single_column_keeps_right_aligned_continuation_with_its_entry(self):
        references = [
            'Alpha Author. 2020. Some long scholarly reference title. https://doi.org/10.1/alpha',
            'Beta Author. 2021. Another long scholarly reference title. https://doi.org/10.1/beta',
        ]
        document = [FakePage([
            ('[1] Alpha Author. 2020. Some long scholarly reference title.', (80, 100, 280, 110)),
            ('https://doi.org/10.1/alpha', (340, 112, 530, 122)),
            ('[2] Beta Author. 2021. Another long scholarly reference title.', (80, 126, 280, 136)),
            ('https://doi.org/10.1/beta', (340, 138, 530, 148)),
        ])]

        regions = _locate_reference_regions(document, references)

        self.assertEqual([region['bbox'][1] for region in regions[0]['regions']], [100, 112])
        self.assertEqual([region['bbox'][1] for region in regions[1]['regions']], [126, 138])

    def test_full_width_bibliography_does_not_split_lines_at_page_midpoint(self):
        # The source PDF uses lines nearly 450 points wide. A one-point width
        # difference puts adjacent lines on opposite sides of the page center.
        references = [
            'Alpha Author. 2020. A method with a long title. Journal.',
            'Beta Author. 2021. Another method with a long title. arXiv.',
        ]
        document = [FakePage([
            ('[44] Alpha Author. 2020. A method with a long title.', (80, 100, 532, 110)),
            ('Journal.', (98, 112, 320, 122)),
            ('[45] Beta Author. 2021. Another method with a long title.', (80, 126, 531, 136)),
            ('arXiv.', (98, 138, 300, 148)),
        ])]

        regions = _locate_reference_regions(document, references)

        self.assertEqual([entry['regions'][0]['bbox'][1] if entry['regions'] else None for entry in regions], [100, 126])
        self.assertEqual([entry['regions'][0]['bbox'][3] if entry['regions'] else None for entry in regions], [122, 148])

    def test_two_column_bibliography_keeps_left_column_before_right(self):
        references = [
            '[1] Alpha Author. 2020. Left column first.',
            '[2] Beta Author. 2021. Left column second.',
            '[3] Gamma Author. 2022. Right column first.',
            '[4] Delta Author. 2023. Right column second.',
        ]
        document = [FakePage([
            (references[0], (72, 100, 280, 110)),
            (references[2], (330, 100, 530, 110)),
            (references[1], (72, 130, 282, 140)),
            (references[3], (330, 130, 535, 140)),
        ])]

        regions = _locate_reference_regions(document, references)

        self.assertEqual([entry['regions'][0]['bbox'][0] for entry in regions], [72, 72, 330, 330])


if __name__ == '__main__':
    unittest.main()
