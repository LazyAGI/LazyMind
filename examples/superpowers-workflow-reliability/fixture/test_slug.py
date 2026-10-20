import unittest
from slug import slugify
class SlugTests(unittest.TestCase):
    def test_spaces(self): self.assertEqual(slugify("  Hello   World  "), "hello-world")
    def test_punctuation(self): self.assertEqual(slugify("Hello, World!"), "hello-world")
    def test_empty(self):
        with self.assertRaises(ValueError): slugify(" ! ")
if __name__ == "__main__": unittest.main()
