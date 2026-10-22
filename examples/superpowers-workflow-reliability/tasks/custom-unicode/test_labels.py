import unittest
from 标签 import normalize_labels
class Labels(unittest.TestCase):
    def test_normalize(self): self.assertEqual(normalize_labels([' A ', '', '  ', 'B']), ['a','b'])
    def test_dedupe(self): self.assertEqual(normalize_labels(['B','a',' B ','A','c']), ['b','a','c'])
    def test_validation(self):
        for values in [[1], ['ok', None], [False]]:
            with self.subTest(values=values), self.assertRaises(ValueError): normalize_labels(values)
    def test_empty(self): self.assertEqual(normalize_labels([]), [])
