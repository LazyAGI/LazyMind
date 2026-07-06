import argparse
import logging
import signal
import sys
import time
from pathlib import Path

from milvus_lite.server import Server


LOG = logging.getLogger('lazymind.local_milvus_lite')


def _parse_args():
    parser = argparse.ArgumentParser(description='Run Milvus Lite as a LazyMind local host process.')
    parser.add_argument('--db-file', required=True, help='Milvus Lite database file path.')
    parser.add_argument('--address', required=True, help='Loopback host:port for the Milvus gRPC endpoint.')
    return parser.parse_args()


def main() -> int:
    logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(name)s: %(message)s')
    args = _parse_args()
    db_file = Path(args.db_file).expanduser().resolve()
    db_file.parent.mkdir(parents=True, exist_ok=True)

    server = Server(str(db_file), args.address)
    if not server.init():
        LOG.error('failed to initialize Milvus Lite at %s', db_file)
        return 1
    if not server.start():
        LOG.error('failed to start Milvus Lite at %s on %s', db_file, args.address)
        return 1

    stopping = False

    def _stop(signum, _frame):
        nonlocal stopping
        if stopping:
            return
        stopping = True
        LOG.info('stopping Milvus Lite after signal %s', signum)
        server.stop()

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)
    LOG.info('Milvus Lite started at %s using %s', args.address, db_file)

    try:
        while not stopping:
            proc = getattr(server, '_p', None)
            if proc is not None and proc.poll() is not None:
                LOG.error('Milvus Lite child exited with code %s', proc.returncode)
                return proc.returncode or 1
            time.sleep(0.5)
    finally:
        server.stop()
    return 0


if __name__ == '__main__':
    sys.exit(main())
