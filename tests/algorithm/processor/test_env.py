import pytest

from lazymind.processor.service.env import env_bool, env_float, env_int, env_list


def test_env_int_uses_default_for_missing_or_blank(monkeypatch):
    monkeypatch.delenv('TEST_INT', raising=False)
    assert env_int('TEST_INT', 3) == 3

    monkeypatch.setenv('TEST_INT', '   ')
    assert env_int('TEST_INT', 3) == 3


def test_env_int_parses_value(monkeypatch):
    monkeypatch.setenv('TEST_INT', '42')

    assert env_int('TEST_INT', 3) == 42


def test_env_float_uses_default_for_missing_or_blank(monkeypatch):
    monkeypatch.delenv('TEST_FLOAT', raising=False)
    assert env_float('TEST_FLOAT', 1.5) == 1.5

    monkeypatch.setenv('TEST_FLOAT', '   ')
    assert env_float('TEST_FLOAT', 1.5) == 1.5


def test_env_float_parses_value(monkeypatch):
    monkeypatch.setenv('TEST_FLOAT', '2.75')

    assert env_float('TEST_FLOAT', 1.5) == pytest.approx(2.75)


@pytest.mark.parametrize('raw', ['1', 'true', 'TRUE', ' yes ', 'on'])
def test_env_bool_accepts_truthy_values(monkeypatch, raw):
    monkeypatch.setenv('TEST_BOOL', raw)

    assert env_bool('TEST_BOOL', False) is True


@pytest.mark.parametrize('raw', ['0', 'false', 'no', 'off', '', 'anything-else'])
def test_env_bool_treats_other_values_as_false(monkeypatch, raw):
    monkeypatch.setenv('TEST_BOOL', raw)

    assert env_bool('TEST_BOOL', True) is False


def test_env_bool_uses_default_only_when_missing(monkeypatch):
    monkeypatch.delenv('TEST_BOOL', raising=False)

    assert env_bool('TEST_BOOL', True) is True


def test_env_list_returns_none_for_missing_or_blank(monkeypatch):
    monkeypatch.delenv('TEST_LIST', raising=False)
    assert env_list('TEST_LIST') is None

    monkeypatch.setenv('TEST_LIST', '   ')
    assert env_list('TEST_LIST') is None


def test_env_list_returns_empty_list_when_all_items_are_blank(monkeypatch):
    monkeypatch.setenv('TEST_LIST', ' ,  , ')

    assert env_list('TEST_LIST') == []


def test_env_list_trims_and_skips_empty_items(monkeypatch):
    monkeypatch.setenv('TEST_LIST', 'alpha, beta,, gamma , ')

    assert env_list('TEST_LIST') == ['alpha', 'beta', 'gamma']
