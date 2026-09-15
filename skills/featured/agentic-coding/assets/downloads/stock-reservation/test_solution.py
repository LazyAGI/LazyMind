import copy
import importlib
import os
import unittest

import baseline

subject = importlib.import_module(os.environ.get("PACT_MODULE", "solution"))


def request(sku, quantity):
    return {"sku": sku, "quantity": quantity}


class ReservationContract(unittest.TestCase):
    def test_main_result(self):
        stock = {"USB-C": 5, "HDMI": 2}
        requests = [request("USB-C", 3), request("USB-C", 4),
                    request("HDMI", 2), request("SSD", 1)]
        self.assertEqual(subject.reserve_stock(stock, requests),
                         ({"USB-C": 3, "HDMI": 2}, {"USB-C": 4, "SSD": 1}))

    def test_inputs_unchanged(self):
        stock = {"USB-C": 5}
        requests = [request("USB-C", 3)]
        before = copy.deepcopy((stock, requests))
        subject.reserve_stock(stock, requests)
        self.assertEqual((stock, requests), before)

    def test_repeated_sku_greedy_order(self):
        self.assertEqual(subject.reserve_stock({"A": 5}, [
            request("A", 4), request("A", 2), request("A", 1)]),
            ({"A": 5}, {"A": 2}))

    def test_shortages_aggregate(self):
        self.assertEqual(subject.reserve_stock({"A": 1}, [
            request("A", 2), request("A", 3)]), ({}, {"A": 5}))

    def test_unknown_sku(self):
        self.assertEqual(subject.reserve_stock({}, [request("X", 1)]), ({}, {"X": 1}))

    def test_zero_quantity_keeps_baseline_result_without_inserting_stock(self):
        stock = {}
        self.assertEqual(subject.reserve_stock(stock, [request("X", 0)]),
                         ({"X": 0}, {}))
        self.assertEqual(stock, {})

    def test_empty_requests(self):
        self.assertEqual(subject.reserve_stock({"A": 5}, []), ({}, {}))

    def test_exact_depletion(self):
        self.assertEqual(subject.reserve_stock({"A": 2}, [request("A", 2)]),
                         ({"A": 2}, {}))

    def test_missing_key_raises_without_partial_stock_mutation(self):
        stock = {"A": 2}
        with self.assertRaises(KeyError):
            subject.reserve_stock(stock, [request("A", 1), {"sku": "A"}])
        self.assertEqual(stock, {"A": 2})

    def test_result_parity_matrix(self):
        for available in range(4):
            for first in range(5):
                for second in range(5):
                    stock = {"A": available}
                    requests = [request("A", first), request("A", second), request("X", 1)]
                    with self.subTest(available=available, first=first, second=second):
                        expected = baseline.reserve_stock(copy.deepcopy(stock), copy.deepcopy(requests))
                        self.assertEqual(subject.reserve_stock(stock, requests), expected)


if __name__ == "__main__":
    unittest.main()
