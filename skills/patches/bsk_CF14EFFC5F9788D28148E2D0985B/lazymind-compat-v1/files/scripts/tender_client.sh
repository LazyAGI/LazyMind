#!/bin/sh
# Use an actual Python interpreter even when the host agent is a frozen binary.
exec python3 "$(dirname "$0")/tender_client.py" "$@"
