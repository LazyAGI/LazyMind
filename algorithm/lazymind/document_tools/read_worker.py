"""One read-only operation. Keep startup independent of Chat and its background jobs."""

from contextlib import redirect_stdout
import json
import sys


def main():
    output = sys.stdout
    with redirect_stdout(sys.stderr):
        # Linux is the server deployment target. Cap address space before parsing untrusted documents.
        # macOS/Windows retain transfer/time/concurrency limits; OS memory enforcement is platform-specific.
        if sys.platform.startswith('linux'):
            import resource
            resource.setrlimit(resource.RLIMIT_AS, (2 * 1024**3, 2 * 1024**3))
        try:
            from lazymind.document_tools.reading import DocumentReadRequest, DocumentReadError, read_document
            from lazymind.document_tools.discovery import (
                DocumentDiscoveryRequest, browse_documents, search_documents,
            )
            operations = {
                'read': (DocumentReadRequest, read_document),
                'browse': (DocumentDiscoveryRequest, browse_documents),
                'search': (DocumentDiscoveryRequest, search_documents),
            }
            model, function = operations[sys.argv[1]]
            body = sys.stdin.buffer.read(65537)
            if len(body) > 65536:
                raise ValueError('request too large')
            request = model.model_validate_json(body)
            try:
                result = {'result': function(request).model_dump(mode='json')}
            except DocumentReadError as exc:
                result = {'error': {'code': exc.code, 'message': str(exc)}}
            encoded = json.dumps(result)
            if len(encoded.encode()) > 2 * 1024 * 1024:
                raise MemoryError('response too large')
        except MemoryError:
            encoded = json.dumps({'error': {
                'code': 'RESOURCE_LIMIT_EXCEEDED', 'message': 'document memory limit exceeded'}})
        except Exception:
            encoded = json.dumps({'error': {'code': 'PROVIDER_UNAVAILABLE', 'message': 'document worker failed'}})
    output.write(encoded)
    output.flush()


if __name__ == '__main__':
    main()
