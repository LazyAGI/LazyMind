from __future__ import annotations

import json
import threading
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer

from server import FakeOpenAIHandler


class FakeOpenAIServerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeOpenAIHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join(timeout=2)

    def _post(self, query: str, stream: bool = False):
        payload = {
            "model": "whatever-the-ui-configures",
            "messages": [{"role": "user", "content": query}],
            "stream": stream,
        }
        request = urllib.request.Request(
            f"{self.base}/v1/chat/completions",
            data=json.dumps(payload).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        return urllib.request.urlopen(request, timeout=3)

    def test_normal_and_length_terminal_reasons(self):
        normal = json.load(self._post("hello [fake:stop]"))
        limited = json.load(self._post("hello [fake:length]"))
        self.assertEqual(normal["choices"][0]["finish_reason"], "stop")
        self.assertEqual(limited["choices"][0]["finish_reason"], "length")

    def test_provider_error_statuses(self):
        for marker, status in [
            ("400", 400),
            ("sensitive_input", 400),
            ("401", 401),
            ("402", 402),
            ("422", 422),
            ("429", 429),
            ("429_quota", 429),
            ("429_unknown", 429),
            ("500", 500),
            ("503", 503),
        ]:
            with self.subTest(marker=marker):
                with self.assertRaises(urllib.error.HTTPError) as exc_info:
                    self._post(f"trigger [fake:{marker}]")
                self.assertEqual(exc_info.exception.code, status)
                body = json.load(exc_info.exception)
                self.assertIn("error", body)
                if status in {429, 503}:
                    self.assertEqual(exc_info.exception.headers["Retry-After"], "2")

    def test_openai_compatible_ambiguity_envelopes(self):
        expected_codes = {
            "402": "payment_required",
            "429": "rate_limit_exceeded",
            "429_quota": "credit_balance_exhausted",
            "429_unknown": "gateway_limit",
        }
        for marker, expected_code in expected_codes.items():
            with self.subTest(marker=marker):
                with self.assertRaises(urllib.error.HTTPError) as exc_info:
                    self._post(f"trigger [fake:{marker}]")
                body = json.load(exc_info.exception)
                self.assertEqual(body["error"]["code"], expected_code)

    def test_minimax_business_errors_use_http_200_base_resp(self):
        for marker, expected_code in [("minimax_1002", 1002), ("minimax_1008", 1008)]:
            with self.subTest(marker=marker):
                body = json.load(self._post(f"trigger [fake:{marker}]"))
                self.assertEqual(body["base_resp"]["status_code"], expected_code)

    def test_stream_has_terminal_frame_and_done(self):
        response = self._post("stream [fake:length]", stream=True)
        body = response.read().decode()
        events = [json.loads(line[6:]) for line in body.splitlines() if line.startswith("data: {")]
        self.assertEqual(events[-1]["choices"][0]["finish_reason"], "length")
        self.assertTrue(body.rstrip().endswith("data: [DONE]"))


if __name__ == "__main__":
    unittest.main()
