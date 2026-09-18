#!/bin/sh
set -eu

# Only the initializer writes this volume. Consumers mount it read-only, just
# like a Docker secret; 0444 permits the nonroot scan service to read the token.
secret_dir=${1:-/run/secrets/internal-service}
token_file="$secret_dir/token"
mkdir -p "$secret_dir"
chmod 0755 "$secret_dir"
umask 077

fail() {
  echo "Internal service token initialization failed: $1" >&2
  exit 1
}

validate_token() {
  [ "${#token}" -ge 16 ] && [ "${#token}" -le 4096 ] || fail 'token must contain 16 to 4096 characters'
  case "$token" in
    *'
'*) fail 'token must be a single line' ;;
  esac
}

read_token_file() {
  [ -f "$1" ] && [ ! -L "$1" ] || fail 'configured token must be a regular file'
  size=$(wc -c < "$1")
  [ "$size" -gt 0 ] && [ "$size" -le 4096 ] || fail 'token file is empty or too large'
  token=$(cat "$1")
}

explicit=false
if [ -n "${LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN:-}" ]; then
  token=$LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN
  explicit=true
elif [ -n "${LAZYMIND_INTERNAL_SERVICE_TOKEN_INPUT_FILE:-}" ]; then
  read_token_file "$LAZYMIND_INTERNAL_SERVICE_TOKEN_INPUT_FILE"
  explicit=true
elif [ -e "$token_file" ] || [ -L "$token_file" ]; then
  read_token_file "$token_file"
else
  token=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
fi

token=$(printf '%s' "$token" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
validate_token

if [ "$explicit" = true ] || [ ! -e "$token_file" ]; then
  temporary=$(mktemp "$secret_dir/.token.XXXXXX")
  trap 'rm -f "$temporary"' EXIT HUP INT TERM
  printf '%s\n' "$token" > "$temporary"
  chmod 0444 "$temporary"
  if [ "$explicit" = true ]; then
    mv -f "$temporary" "$token_file"
  else
    # A concurrent initializer must reuse the winner's key, never overwrite it.
    ln "$temporary" "$token_file" 2>/dev/null || [ -f "$token_file" ] || fail 'cannot publish token'
  fi
fi
read_token_file "$token_file"
validate_token
chmod 0444 "$token_file"
echo 'Internal service token is ready'
