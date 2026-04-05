#!/bin/bash
# Simple health check script — can be used with cron or monitoring tools
# Usage: ./scripts/healthcheck.sh

URL="${HEALTHCHECK_URL:-https://invoiceparser-api.fly.dev/health}"

response=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 "$URL")

if [ "$response" = "200" ]; then
    echo "OK: Health check passed (HTTP $response)"
    exit 0
else
    echo "FAIL: Health check failed (HTTP $response)"
    exit 1
fi
