#!/bin/sh
set -eu

export LC_ALL=C
secret_dir=${1:-/run/secrets/user-env}
key_file="$secret_dir/user-env.key"
umask 077
mkdir -p "$secret_dir"

fail() {
  echo "User environment key initialization failed: $1" >&2
  exit 1
}

validate_key() {
  key=$(printf '%s' "$key" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  [ "${#key}" -ge 32 ] && [ "${#key}" -le 4096 ] || fail 'key must contain 32 to 4096 bytes'
  case "$key" in
    *'
'*) fail 'key must be a single line' ;;
  esac
}

read_key() {
  [ -f "$1" ] && [ ! -L "$1" ] || fail 'key must be a regular file, not a symlink'
  size=$(wc -c < "$1")
  [ "$size" -gt 0 ] && [ "$size" -le 4097 ] || fail 'key file is empty or too large'
  key=$(cat "$1")
  validate_key
}

explicit=false
if [ -n "${LAZYMIND_USER_ENV_SECRET_KEY_INPUT_FILE:-}" ]; then
  read_key "$LAZYMIND_USER_ENV_SECRET_KEY_INPUT_FILE"
  explicit=true
elif [ -n "${LAZYMIND_USER_ENV_SECRET_KEY:-}" ]; then
  key=$LAZYMIND_USER_ENV_SECRET_KEY
  validate_key
  explicit=true
fi
requested_key=${key:-}

if [ -e "$key_file" ] || [ -L "$key_file" ]; then
  read_key "$key_file"
else
  if [ "$explicit" = false ]; then
    key=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
    validate_key
  fi
  temporary=$(mktemp "$secret_dir/.user-env-key.XXXXXX")
  trap 'rm -f "$temporary"' EXIT
  trap 'exit 1' HUP INT TERM
  printf '%s\n' "$key" > "$temporary"
  chmod 0600 "$temporary"
  # Atomic, no-clobber publication: concurrent starts must reuse the winner.
  ln "$temporary" "$key_file" 2>/dev/null || [ -f "$key_file" ] || fail 'cannot publish key'
  read_key "$key_file"
fi

if [ "$explicit" = true ] && [ "$key" != "$requested_key" ]; then
  fail 'configured key differs from the persisted key; restore the original configuration, do not rotate automatically'
fi
chmod 0600 "$key_file"
echo 'User environment encryption key is ready'
