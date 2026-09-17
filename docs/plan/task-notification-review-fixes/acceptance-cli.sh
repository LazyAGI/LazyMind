#!/bin/sh
# Acceptance-only adapter: never log stdin, stdout, tokens, or arbitrary arguments.
set -eu
: "${LAZYMIND_ACCEPTANCE_ROOT:?Set the acceptance directory first}"
: "${LAZYMIND_ACCEPTANCE_BIN:?Set the actual packaged CLI path first}"
case "${1-} ${2-} ${3-}" in
  'internal session renew'|'internal session set'|'internal session clear'|'internal session snapshot')
    printf '%s session %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$3" >> "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log"
    ;;
esac
# Optional, explicitly simulated connector outage. No production file is changed.
if [ "${1-} ${2-} ${3-}" = 'internal session renew' ] && [ -f "$LAZYMIND_ACCEPTANCE_ROOT/simulate-renewal-outage" ]; then
  printf '%s\n' '{"ok":false,"code":"DESKTOP_SESSION_RENEWAL_UNAVAILABLE"}'
  exit 0
fi
exec "$LAZYMIND_ACCEPTANCE_BIN" "$@"
