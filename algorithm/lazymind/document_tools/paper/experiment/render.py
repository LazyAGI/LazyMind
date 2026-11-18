"""Render previews from canonical experiment tables, without model-transcribed cells."""
import re


def render_tables(tables):
    report = []
    for number, entry in enumerate(tables, 1):
        table = entry['table']
        # Extracted descriptions retain source/section separately from the opaque source identity.
        description = entry.get('description') or ''
        source, marker, location = description.partition('；来源 ')
        parts = [part.strip() for part in source.split('；') if part.strip()]
        title = parts[-1] if parts else f'数据表 {number}'
        title = re.sub(r'^\d+(?:\.\d+)*[.、\s]+', '', title)
        title = ' '.join(title.splitlines()).lstrip('# ').strip() or f'数据表 {number}'
        if marker:
            citation = parts[0] if parts else '用户材料'
            _, _, lines = location.partition('，')
            if lines:
                citation += '，' + lines
        else:
            citation = description
        headers = [c if not u or u in c else f'{c}（{u}）'
                   for c, u in zip(table['columns'], table.get('units', [None] * len(table['columns'])))]

        def cells(row):
            values = ['缺失' if cell is None else str(cell).replace('\\', '\\\\').replace('|', '\\|')
                      .replace('\r', ' ').replace('\n', ' ') for cell in row]
            return '| ' + ' | '.join(values) + ' |'

        lines = [cells(headers), cells(['---'] * len(headers)),
                 *(cells(row) for row in table['rows'][:20])]
        report += [f'## {title}', '\n'.join(lines)]
        note = f'来源：{citation}' if citation else ''
        if len(table['rows']) > 20:
            note += f' 共 {len(table["rows"])} 行，此处展示前 20 行；完整数据见结果文件。'
        if note:
            report.append(note.strip())
    return '\n\n'.join(report)
