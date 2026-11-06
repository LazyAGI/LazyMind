"""Source-span HTML structural edits. Never serializes or regenerates a page."""
from html import escape
import math
import re
import uuid

OPERATIONS = {
    'add_text', 'add_list', 'add_table', 'set_list', 'list_insert_item', 'list_set_item', 'list_delete_item',
    'table_set_cell', 'table_insert_row', 'table_delete_row', 'table_insert_column', 'table_delete_column',
    'adjust_style',
}


def _text(value):
    if not isinstance(value, str) or len(value) > 4000:
        raise ValueError('Text must be a string of at most 4000 characters')
    return escape(value)


def _values(values, count=None):
    if not isinstance(values, list) or not 1 <= len(values) <= 100:
        raise ValueError('values must contain 1–100 strings')
    if count is not None and len(values) != count:
        raise ValueError('values must match the table dimension exactly')
    return [_text(value) for value in values]


def _index(value, length, insert=False):
    if type(value) is not int or not 1 <= value <= length + int(insert):
        raise ValueError('index is 1-based; insert may use length + 1 to append')
    return value - 1


def _close(html, node):
    start = html.rfind('</', node['open_end'], node['end'])
    if start < node['open_end']:
        raise ValueError('Target needs an explicit closing tag')
    return start


def _fragment(html, node, inner):
    fragment = html[node['start']:node['open_end']] + inner + html[_close(html, node):node['end']]
    # Clones must not duplicate IDs. Preserve classes, cell styling and semantics.
    return re.sub(r'\s(?:id|data-el|data-group)\s*=\s*(?:"[^"]*"|\'[^\']*\')', '', fragment)


def apply(html, op, tree_type, resolve, set_styles):
    tree = tree_type(html)
    target = resolve(tree, str(op.get('el') or ''), op)[0]
    node = tree.nodes[target]
    name = op['op']
    replacements = []
    if name.startswith('add_'):
        if node['tag'] not in ('div', 'section', 'article', 'main', 'aside', 'li'):
            raise ValueError('Add content inside an inspected content container')
        identity = op.get('new_el') or f'edit-{uuid.uuid4().hex[:12]}'
        if not isinstance(identity, str) or not re.fullmatch(r'[a-zA-Z][\w-]{0,79}', identity):
            raise ValueError('new_el must be a simple unique element ID')
        if any(n['el'] == identity or n['id'] == identity for n in tree.nodes):
            raise ValueError('new_el already exists')
        attr = f' data-el="{identity}"'
        if name == 'add_text':
            fragment = f'<p{attr}>{_text(op.get("text"))}</p>'
        elif name == 'add_list':
            if type(op.get('ordered', False)) is not bool:
                raise ValueError('ordered must be boolean')
            tag = 'ol' if op.get('ordered') else 'ul'
            fragment = f'<{tag}{attr}>' + ''.join(f'<li>{v}</li>' for v in _values(op.get('values'))) + f'</{tag}>'
        else:
            rows = op.get('rows')
            if not isinstance(rows, list) or not 1 <= len(rows) <= 40:
                raise ValueError('rows must contain 1–40 rows')
            rows = [_values(row) for row in rows]
            if len(rows[0]) > 20 or any(len(row) != len(rows[0]) for row in rows):
                raise ValueError('Table must be rectangular with at most 20 columns')
            cells = ''.join('<tr>' + ''.join(
                f'<td style="border:1px solid #CBD5E1;padding:6px">{v}</td>' for v in row) + '</tr>' for row in rows)
            fragment = f'<table{attr} style="border-collapse:collapse;width:100%"><tbody>{cells}</tbody></table>'
        offset = _close(html, node)
        return html[:offset] + fragment + html[offset:]
    if tree.is_protected(target):
        raise ValueError('Cannot replace or resize the page shell')
    if name == 'adjust_style':
        changes = op.get('deltas')
        if not isinstance(changes, dict) or not changes:
            raise ValueError('deltas requires CSS properties and numeric increments')
        styles = dict(part.split(':', 1) for part in node['attrs'].get('style', '').split(';') if ':' in part)
        styles = {k.strip(): v.strip() for k, v in styles.items()}
        output = {}
        for key, value in changes.items():
            if key not in ('width', 'height', 'left', 'top', 'font-size', 'gap', 'padding', 'margin-left', 'margin-top'):
                raise ValueError('Unsupported relative style; use set_style for explicit values')
            match = re.fullmatch(r'(-?\d+(?:\.\d+)?)(px|pt|%|em|rem)(?:\s*!important)?', styles.get(key, ''))
            if not match or type(value) not in (int, float) or not math.isfinite(value):
                raise ValueError('Relative edits need a numeric inline base and finite delta; otherwise use set_style')
            result = float(match[1]) + value
            if key not in ('left', 'top', 'margin-left', 'margin-top') and result <= 0:
                raise ValueError('Resulting dimension must be positive')
            output[key] = f'{result:g}{match[2]}'
        return set_styles(html, node, output)
    if name == 'set_list' or name.startswith('list_'):
        if node['tag'] not in ('ul', 'ol'):
            raise ValueError('Target must be a native HTML list (ul/ol)')
        items = [tree.nodes[i] for i in node['children'] if tree.nodes[i]['tag'] == 'li']
        if name == 'set_list':
            values = _values(op.get('values'))
            body = ''.join(_fragment(html, items[min(i, len(items) - 1)], v) if items else f'<li>{v}</li>'
                           for i, v in enumerate(values))
            replacements.append((node['open_end'], _close(html, node), body))
            if 'ordered' in op:
                if type(op['ordered']) is not bool:
                    raise ValueError('ordered must be boolean')
                tag = 'ol' if op['ordered'] else 'ul'
                opening = html[node['start']:node['open_end']].replace('<' + node['tag'], '<' + tag, 1)
                replacements.extend([(node['start'], node['open_end'], opening),
                                     (_close(html, node), node['end'], f'</{tag}>')])
        else:
            index = _index(op.get('index'), len(items), name == 'list_insert_item')
            if name == 'list_insert_item':
                value = _text(op.get('text'))
                fragment = _fragment(html, items[min(index, len(items) - 1)], value) if items else f'<li>{value}</li>'
                offset = items[index]['start'] if index < len(items) else _close(html, node)
                replacements.append((offset, offset, fragment))
            elif name == 'list_set_item':
                item = items[index]
                replacements.append((item['open_end'], _close(html, item), _text(op.get('text'))))
            else:
                if len(items) == 1:
                    raise ValueError('Use delete_node to remove the final list item')
                replacements.append((items[index]['start'], items[index]['end'], ''))
    else:
        if node['tag'] != 'table':
            raise ValueError('Target must be a native HTML table')
        rows = [n for i, n in enumerate(tree.nodes) if n['tag'] == 'tr' and target in tree.ancestors(i)]
        cells = [[tree.nodes[i] for i in row['children'] if tree.nodes[i]['tag'] in ('td', 'th')] for row in rows]
        if (not cells or not cells[0] or any(len(row) != len(cells[0]) for row in cells)
                or any(c['attrs'].get(k, '1') != '1' for row in cells for c in row for k in ('colspan', 'rowspan'))):
            raise ValueError('Table must be rectangular without merged cells')
        if any(n['tag'] == 'table' and target in tree.ancestors(i) for i, n in enumerate(tree.nodes)):
            raise ValueError('Nested tables are not supported')
        if name == 'table_set_cell':
            cell = cells[_index(op.get('row'), len(rows))][_index(op.get('column'), len(cells[0]))]
            replacements.append((cell['open_end'], _close(html, cell), _text(op.get('text'))))
        elif name.endswith('_row'):
            insert = name == 'table_insert_row'
            index = _index(op.get('index'), len(rows), insert)
            if insert:
                if len(rows) >= 40:
                    raise ValueError('Table limit is 40 rows')
                values = _values(op.get('values'), len(cells[0]))
                template = min(index, len(rows) - 1)
                body = ''.join(_fragment(html, c, v) for c, v in zip(cells[template], values))
                offset = rows[index]['start'] if index < len(rows) else rows[-1]['end']
                replacements.append((offset, offset, _fragment(html, rows[template], body)))
            else:
                if len(rows) == 1:
                    raise ValueError('Use delete_node to remove the final table row')
                replacements.append((rows[index]['start'], rows[index]['end'], ''))
        else:
            insert = name == 'table_insert_column'
            index = _index(op.get('index'), len(cells[0]), insert)
            if insert:
                if len(cells[0]) >= 20:
                    raise ValueError('Table limit is 20 columns')
                values = _values(op.get('values'), len(rows))
            elif len(cells[0]) == 1:
                raise ValueError('Use delete_node to remove the final table column')
            for i, row in enumerate(cells):
                if insert:
                    offset = row[index]['start'] if index < len(row) else row[-1]['end']
                    fragment = _fragment(html, row[min(index, len(row) - 1)], values[i])
                    replacements.append((offset, offset, fragment))
                else:
                    replacements.append((row[index]['start'], row[index]['end'], ''))
    for start, end, text in sorted(replacements, reverse=True):
        html = html[:start] + text + html[end:]
    return html
