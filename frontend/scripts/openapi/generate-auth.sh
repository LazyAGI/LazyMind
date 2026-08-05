#!/bin/bash

set -e

if ! command -v java >/dev/null 2>&1 || ! java -version >/dev/null 2>&1; then
  if [ -n "${CI:-}" ] || [ "${OPENAPI_GENERATE_STRICT:-}" = "1" ]; then
    echo 'Error: java not found. Install JDK 17+ and ensure it is on PATH (e.g. JAVA_HOME/bin).' >&2
    exit 1
  fi

  STALE_APIS=$(node scripts/openapi/check-stale.mjs --json | node -e "const fs=require('fs');const items=JSON.parse(fs.readFileSync(0,'utf8'));process.stdout.write(items.filter(item=>item.stale || !item.exists).map(item=>item.name).join(','));" || true)

  echo '⚠️  Java runtime not found. Skipping OpenAPI generation and using checked-in API clients.' >&2
  if [ -n "${STALE_APIS}" ]; then
    if [ "${OPENAPI_ALLOW_STALE:-}" != "1" ]; then
      echo "   Error: generated client is stale for: ${STALE_APIS}." >&2
      echo '   Install JDK 17+ and rerun `npm run gen:auth`, or set OPENAPI_ALLOW_STALE=1 to bypass temporarily.' >&2
      exit 1
    fi
    echo "   Warning: spec changed but generated client was not refreshed for: ${STALE_APIS}." >&2
  fi
  echo '   To regenerate locally, install JDK 17+ and rerun `node scripts/openapi/generate-api.mjs`.' >&2
  exit 0
fi

node scripts/openapi/generate-api.mjs auth
node scripts/openapi/generate-api.mjs core
node scripts/openapi/generate-api.mjs scan
node scripts/openapi/generate-api.mjs channel-gateway
