from dataclasses import dataclass
from pathlib import Path
from typing import Literal, Optional


@dataclass(frozen=True)
class SensitiveMatch:
    word: str
    tier: Literal['red', 'gray']
    start: int
    end: int


class SensitiveFilter:
    def __init__(
        self,
        red_path: str | Path,
        gray_path: str | Path,
        whitelist_path: str | Path,
    ):
        try:
            import ahocorasick
        except ImportError as exc:
            raise RuntimeError(
                'SensitiveFilter requires pyahocorasick to be installed.'
            ) from exc
        try:
            import jieba
        except ImportError as exc:
            raise RuntimeError(
                'SensitiveFilter requires jieba to be installed.'
            ) from exc

        self._red_words = self._read_words(red_path, 'red')
        self._gray_words = self._read_words(gray_path, 'gray')
        self._whitelist_words = self._read_words(whitelist_path, 'whitelist')
        self._red_automaton = self._build_automaton(ahocorasick, self._red_words)
        self._whitelist_automaton = self._build_automaton(
            ahocorasick, self._whitelist_words
        )
        self._gray_word_set = frozenset(self._gray_words)
        self._gray_tokenizer = jieba.Tokenizer()
        for word in self._gray_words:
            self._gray_tokenizer.add_word(word)

    @staticmethod
    def _read_words(path: str | Path, tier: str) -> tuple[str, ...]:
        word_path = Path(path)
        if not word_path.is_file():
            raise RuntimeError(
                f'SensitiveFilter {tier} word file is missing or not a file: {word_path}'
            )
        try:
            words = tuple(
                line.strip()
                for line in word_path.read_text(encoding='utf-8').splitlines()
                if line.strip()
            )
        except OSError as exc:
            raise RuntimeError(
                f'SensitiveFilter failed to read {tier} word file: {word_path}'
            ) from exc
        if not words:
            raise RuntimeError(f'SensitiveFilter {tier} word file is empty: {word_path}')
        return words

    @staticmethod
    def _build_automaton(ahocorasick, words: tuple[str, ...]):
        automaton = ahocorasick.Automaton()
        for word in words:
            automaton.add_word(word, word)
        automaton.make_automaton()
        return automaton

    def evaluate(self, text: str) -> Optional[SensitiveMatch]:
        if not text:
            return None
        whitelist_spans = tuple(
            (end_index - len(word) + 1, end_index + 1)
            for end_index, word in self._whitelist_automaton.iter(text)
        )
        for end_index, word in self._red_automaton.iter(text):
            start = end_index - len(word) + 1
            end = end_index + 1
            if self._is_covered(start, end, whitelist_spans):
                continue
            return SensitiveMatch(word=word, tier='red', start=start, end=end)
        for word, start, end in self._gray_tokenizer.tokenize(text):
            if word not in self._gray_word_set:
                continue
            if word.isascii() and not self._has_ascii_word_boundaries(text, start, end):
                continue
            if self._is_covered(start, end, whitelist_spans):
                continue
            return SensitiveMatch(word=word, tier='gray', start=start, end=end)
        return None

    @staticmethod
    def _has_ascii_word_boundaries(text: str, start: int, end: int) -> bool:
        def is_ascii_word_character(character: str) -> bool:
            return character.isascii() and (
                character.isalnum() or character == '_'
            )

        return not (
            (start > 0 and is_ascii_word_character(text[start - 1]))
            or (end < len(text) and is_ascii_word_character(text[end]))
        )

    @staticmethod
    def _is_covered(
        start: int,
        end: int,
        whitelist_spans: tuple[tuple[int, int], ...],
    ) -> bool:
        return any(
            whitelist_start <= start and end <= whitelist_end
            for whitelist_start, whitelist_end in whitelist_spans
        )
