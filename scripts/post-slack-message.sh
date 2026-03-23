#!/usr/bin/env bash
set -euo pipefail

# Post a message to a Slack thread using the chat.postMessage API.
# Requires SLACK_BOT_TOKEN, SLACK_CHANNEL, and SLACK_THREAD_TS env vars.
#
# Usage:
#   post-slack-message.sh "plain text message"
#   post-slack-message.sh --payload '{"text":"fallback","blocks":[...]}'
#
# With --payload, channel and thread_ts are injected automatically.

if [ "$1" = "--payload" ]; then
  payload="$(echo "$2" | jq \
    --arg channel "$SLACK_CHANNEL" \
    --arg thread_ts "$SLACK_THREAD_TS" \
    '. + {channel: $channel, thread_ts: $thread_ts}')"
else
  text="${1:?message is required}"
  payload="$(jq -n \
    --arg channel "$SLACK_CHANNEL" \
    --arg thread_ts "$SLACK_THREAD_TS" \
    --arg text "$text" \
    '{channel: $channel, thread_ts: $thread_ts, text: $text}')"
fi

if ! response="$(curl -fsS https://slack.com/api/chat.postMessage \
  -H "Authorization: Bearer ${SLACK_BOT_TOKEN}" \
  -H 'Content-type: application/json; charset=utf-8' \
  --data "$payload")"; then
  echo "warning: slack notification failed" >&2
  exit 0
fi

if ! jq -e '.ok == true' >/dev/null <<<"$response"; then
  error="$(jq -r '.error // "unknown"' <<<"$response")"
  echo "warning: slack notification returned non-ok response: $error" >&2
  echo "warning: full response: $response" >&2
fi
