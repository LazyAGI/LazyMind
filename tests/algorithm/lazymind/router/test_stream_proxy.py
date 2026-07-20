from lazymind.router.core import stream_proxy


def test_stream_proxy_ignores_environment_proxy_settings():
    assert stream_proxy._client.trust_env is False
