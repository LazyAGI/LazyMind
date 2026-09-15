#!/bin/sh
# Explicit interpreter avoids treating a packaged agent binary as Python.
# This stdlib-only client does not use the host's PYTHONPATH database hooks.
exec python3 -I "$(dirname "$0")/bid_client.py" "$@"
