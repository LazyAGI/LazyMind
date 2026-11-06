"""Call-local model views; canonical material identities stay owned by Python."""
import copy
import re


class MaterialBindings:
    def __init__(self, papers):
        self.materials = {f'M{index}': copy.deepcopy(paper) for index, paper in enumerate(papers, 1)}
        self.aliases = {paper['id']: key for key, paper in self.materials.items()}
        if len(self.aliases) != len(self.materials):
            raise ValueError('Duplicate material identities in model input')

    def model_view(self, value):
        """Replace registered identities only in the temporary prompt, never the source objects."""
        if isinstance(value, dict):
            return {key: self.model_view(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.model_view(item) for item in value]
        if isinstance(value, str) and self.aliases:
            if value in self.aliases:
                return self.aliases[value]
            pattern = '|'.join(re.escape(key) for key in sorted(self.aliases, key=len, reverse=True))
            return re.sub(r'(?:(?<=#paper-ref-)|(?<![\w-]))(?:' + pattern + r')(?![\w-])',
                          lambda match: self.aliases[match[0]], value)
        return value

    def restore_citations(self, text):
        """Resolve display markers from this invocation, not file paths parsed from prose."""
        def restore(match):
            result = []
            for group in re.split(r'\s*[,，;；]\s*', match['keys']):
                bounds = re.split(r'\s*[-–—]\s*', group)
                start, end = int(bounds[0][1:]), int(bounds[-1][1:])
                if len(bounds) > 2 or start > end or end > len(self.materials):
                    raise ValueError(f'Model cited an unavailable source marker: {group}')
                for index in range(start, end + 1):
                    result.append('[' + (match['prefix'] or '') + self.materials[f'M{index}']['id'] + ']')
            return ''.join(result)

        return re.sub(r'\[(?P<prefix>文献:)?(?P<keys>M[1-9][0-9]*(?:\s*[,，;；–—-]\s*M[1-9][0-9]*)*)\]',
                      restore, text)
