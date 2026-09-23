"""Main Chat-only export markers; no tools or artifacts are created here."""
import json
import re

_OPEN = re.compile(r'^:::export\{title=("(?:[^"\\]|\\.)*") filename=("(?:[^"\\]|\\.)*")\}$')
_FENCE = re.compile(r'^ {0,3}(`{3,}|~{3,})(.*)$')


def _utf16(text):
    return len(text.encode('utf-16-le')) // 2


class ChatExportStream:
    def __init__(self):
        self.raw = []
        self.pending = ''
        self.emitted = 0
        self.offset = 0
        self.fence = None
        self.depth = 0
        self.candidate = None
        self.blocks = []

    def _line(self, line):
        start = self.offset
        self.offset += len(line)
        marker = line.rstrip('\r\n')
        fence = _FENCE.match(marker)
        if self.fence:
            if fence and fence[1][0] == self.fence[0] and len(fence[1]) >= len(self.fence) and not fence[2].strip():
                self.fence = None
            return line
        if fence:
            self.fence = fence[1]
            return line
        if marker.startswith(':::export'):
            match = _OPEN.fullmatch(marker)
            if self.depth:
                self.depth += 1
                self.candidate = None
                return line
            self.depth = 1
            if match:
                try:
                    title, filename = (json.loads(value) for value in match.groups())
                except (ValueError, TypeError):
                    return line
                if title.strip() and filename.strip():
                    self.candidate = (start, self.offset, title, filename)
                    return ''
            self.candidate = None
            return line
        if marker == ':::' and self.depth:
            self.depth -= 1
            if self.depth == 0 and self.candidate:
                opening, body_start, title, filename = self.candidate
                self.blocks.append((opening, body_start, start, self.offset, title, filename))
                self.candidate = None
                return ''
        return line

    def feed(self, text):
        self.raw.append(text)
        self.pending += text
        output = []
        while '\n' in self.pending:
            line, rest = self.pending.split('\n', 1)
            rendered = self._line(line + '\n')
            output.append(rendered[self.emitted:])
            self.pending = rest
            self.emitted = 0
        # Only potential marker/fence lines need buffering. Ordinary prose streams immediately.
        prefix = self.pending.lstrip(' ')
        if self.fence or (prefix and prefix[0] not in ':`~'):
            output.append(self.pending[self.emitted:])
            self.emitted = len(self.pending)
        return ''.join(output)

    def finish(self):
        if self.pending:
            self._line(self.pending)
        raw = ''.join(self.raw)
        parts, exports, cursor, length = [], [], 0, 0
        for opening, start, end, closing, title, filename in self.blocks:
            before, body = raw[cursor:opening], raw[start:end]
            parts.extend((before, body))
            length += _utf16(before)
            exports.append(dict(index=len(exports), title=title, filename=filename,
                                content_type='text/markdown', start=length, end=length + _utf16(body)))
            length += _utf16(body)
            cursor = closing
        parts.append(raw[cursor:])
        content = ''.join(parts)
        # Existing Chat history/rendering trims the assistant body.
        leading = _utf16(content) - _utf16(content.lstrip())
        content = content.strip()
        size = _utf16(content)
        for item in exports:
            item['start'] = max(0, item['start'] - leading)
            item['end'] = min(size, item['end'] - leading)
        exports = [item for item in exports if item['end'] > item['start']]
        for index, item in enumerate(exports):
            item['index'] = index
        return dict(content=content, exports=exports)
