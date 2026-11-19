"""Revision-bound native visual edits, without a geometry/property allowlist."""
import base64
import hashlib
import os
import posixpath
import tempfile
from pathlib import Path
from zipfile import ZipFile

from lxml import etree
from pptx import Presentation


def _xml(raw):
    root = etree.fromstring(raw, etree.XMLParser(resolve_entities=False, no_network=True))
    if root.getroottree().docinfo.doctype:
        raise ValueError('OOXML must not contain a document type declaration')
    return root


def visual_source(source):
    """Give the model editable XML and an inventory of reusable binary assets."""
    with ZipFile(source) as archive:
        parts = {name: archive.read(name).decode('utf-8') for name in archive.namelist()
                 if name == '[Content_Types].xml'
                 or (name.startswith('ppt/') and name.endswith(('.xml', '.rels', '.svg')))}
        assets = [name for name in archive.namelist() if name.startswith(('ppt/media/', 'ppt/embeddings/'))]
    return {'parts': parts, 'assets': assets}


def visual_digest(source):
    """Compare package content, ignoring ZIP timestamps and compression."""
    digest = hashlib.sha256()
    with ZipFile(source) as archive:
        for name in sorted(archive.namelist()):
            digest.update(name.encode() + b'\0' + archive.read(name))
    return digest.hexdigest()


def apply_visual_edits(source, output, plan, *, source_sha256):
    source, output = Path(source), Path(output)
    if source.resolve() == output.resolve():
        raise ValueError('Output must not overwrite source')
    if hashlib.sha256(source.read_bytes()).hexdigest() != source_sha256:
        raise ValueError('Deck changed; inspect the current revision again')
    if not isinstance(plan, dict) or set(plan) != {'parts'} or not isinstance(plan['parts'], list):
        raise ValueError('Visual repair requires a parts list of {path, xml}')
    with ZipFile(source) as archive:
        infos = archive.infolist()
        data = {info.filename: archive.read(info) for info in infos}
    changed, seen = [], set()
    for item in plan['parts']:
        if not isinstance(item, dict) or set(item) not in ({'path', 'xml'}, {'path', 'data_base64'}):
            raise ValueError('Each visual repair part requires path and xml or data_base64')
        path = item['path']
        if (not isinstance(path, str) or path in seen or posixpath.normpath(path) != path
                or '\\' in path or '..' in path.split('/')
                or (not path.startswith('ppt/') and path != '[Content_Types].xml')):
            raise ValueError('Invalid or duplicate OOXML part path')
        if 'xml' in item:
            if not path.endswith(('.xml', '.rels', '.svg')) or not isinstance(item['xml'], str):
                raise ValueError('Part XML must be text in an XML part')
            raw = item['xml'].encode('utf-8')
            _xml(raw)
        else:
            if not path.startswith(('ppt/media/', 'ppt/embeddings/')):
                raise ValueError('Binary replacements must be media or embedded chart resources')
            try:
                raw = base64.b64decode(item['data_base64'], validate=True)
            except (TypeError, ValueError) as exc:
                raise ValueError('Invalid binary resource encoding') from exc
            if not raw:
                raise ValueError('Binary resource must not be empty')
        seen.add(path)
        if data.get(path) != raw:
            changed.append(path)
            data[path] = raw
    if not changed:
        raise ValueError('Visual repair made no change')
    # Relationship integrity is a file-validity check, not a visual editing restriction.
    for name, raw in data.items():
        if not name.endswith('.rels'):
            continue
        directory = posixpath.dirname(posixpath.dirname(name))
        for rel in _xml(raw):
            if rel.get('TargetMode') == 'External':
                continue
            target = rel.get('Target', '')
            target = target.lstrip('/') if target.startswith('/') else posixpath.normpath(
                posixpath.join(directory, target))
            if target not in data:
                raise ValueError('Visual repair introduced a missing package relationship')
    output.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=output.parent, suffix='.pptx')
    os.close(fd)
    try:
        with ZipFile(temporary, 'w') as archive:
            for info in infos:
                archive.writestr(info, data[info.filename])
            for name in data.keys() - {info.filename for info in infos}:
                archive.writestr(name, data[name])
        if len(Presentation(temporary).slides) != len(Presentation(source).slides):
            raise ValueError('Visual repair changed the requested page count')
        os.replace(temporary, output)
    finally:
        Path(temporary).unlink(missing_ok=True)
    return {'changed_parts': sorted(changed), 'original_unchanged': True}
