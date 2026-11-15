"""GitHub HTTP requests using Windows trusted roots without its intermediate cache."""
import ssl
import sys
from urllib import request
from urllib.error import URLError


def urlopen(req, *, timeout, handlers=()):
    if sys.platform == 'win32':
        try:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            roots = bytearray()
            for cert, encoding, trust in ssl.enum_certificates('ROOT'):
                if encoding == 'x509_asn' and (
                    trust is True or ssl.Purpose.SERVER_AUTH.oid in trust
                ):
                    roots.extend(cert)
            if roots:
                context.load_verify_locations(cadata=roots)
            # Preserve OpenSSL's default paths and SSL_CERT_FILE/SSL_CERT_DIR.
            # load_default_certs() would also import Windows' CA store, whose
            # cached cross-signatures can hide a valid ROOT trust anchor.
            context.set_default_verify_paths()
        except OSError as exc:
            raise URLError(exc) from None
        handlers = (*handlers, request.HTTPSHandler(context=context))
    return request.build_opener(*handlers).open(req, timeout=timeout)
