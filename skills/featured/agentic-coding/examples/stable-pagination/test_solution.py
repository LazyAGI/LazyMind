import copy
import importlib
import os
import unittest

subject = importlib.import_module(os.environ.get("PACT_MODULE", "solution"))


def tickets():
    return [
        {"id": "T-104", "priority": 2, "tags": ["billing"]},
        {"id": "T-101", "priority": 1, "tags": ["urgent"]},
        {"id": "T-103", "priority": 1, "tags": []},
        {"id": "T-102", "priority": 3, "tags": ["account"]},
    ]


def ids(rows):
    return [row["id"] for row in rows]


class OrderingGuardrails(unittest.TestCase):
    def test_ascending_and_stable_ties(self):
        self.assertEqual(ids(subject.sorted_tickets(tickets())),
                         ["T-101", "T-103", "T-104", "T-102"])

    def test_empty_sort(self):
        self.assertEqual(subject.sorted_tickets([]), [])


class PaginationAcceptance(unittest.TestCase):
    def test_sort_does_not_mutate(self):
        source = tickets()
        before = copy.deepcopy(source)
        result = subject.sorted_tickets(source)
        self.assertEqual(source, before)
        self.assertIsNot(result, source)

    def test_two_pages(self):
        source = tickets()
        self.assertEqual(ids(subject.paginate_tickets(source, 1, 2)), ["T-101", "T-103"])
        self.assertEqual(ids(subject.paginate_tickets(source, 2, 2)), ["T-104", "T-102"])

    def test_partial_last_page(self):
        self.assertEqual(ids(subject.paginate_tickets(tickets(), 2, 3)), ["T-102"])

    def test_empty_and_out_of_range(self):
        self.assertEqual(subject.paginate_tickets([], 1, 2), [])
        self.assertEqual(subject.paginate_tickets(tickets(), 8, 2), [])

    def test_page_does_not_mutate(self):
        source = tickets()
        before = copy.deepcopy(source)
        subject.paginate_tickets(source, 2, 2)
        self.assertEqual(source, before)

    def test_nonpositive_rejected(self):
        for page, size in [(0, 2), (-1, 2), (1, 0), (1, -2)]:
            with self.subTest(page=page, size=size), self.assertRaises(ValueError):
                subject.paginate_tickets(tickets(), page, size)

    def test_noninteger_and_bool_rejected(self):
        for value in [True, False, 1.0, "1", None]:
            for page, size in [(value, 2), (1, value)]:
                with self.subTest(page=page, size=size), self.assertRaises(TypeError):
                    subject.paginate_tickets(tickets(), page, size)

    def test_invalid_parameters_checked_before_data(self):
        with self.assertRaises(ValueError):
            subject.paginate_tickets([{}], 0, 2)


if __name__ == "__main__":
    unittest.main()
