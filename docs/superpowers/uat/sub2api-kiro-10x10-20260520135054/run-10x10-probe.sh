#!/usr/bin/env bash
set -euo pipefail

mode="${1:?mode required: stream or nonstream}"
out_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rounds=10
concurrency=10
model="claude-opus-4-7"

pass=$(awk '/^database:/{f=1} f && /^[[:space:]]*password:/{print $2; exit}' /www/sub2api/deploy/data/config.yaml)
api_key=$(docker exec -e PGPASSWORD="$pass" sub2api psql -h postgres -U sub2api -d sub2api -Atc "select key from api_keys where id=2")

mode_dir="$out_dir/$mode"
mkdir -p "$mode_dir"
: > "$mode_dir/results.jsonl"
: > "$mode_dir/failures.txt"

run_one() {
  local round="$1"
  local slot="$2"
  local req_id="uat-${mode}-r${round}-s${slot}-$(date +%s%N)"
  local body_file="$mode_dir/${req_id}.body"
  local header_file="$mode_dir/${req_id}.headers"
  local stream_flag=false
  if [[ "$mode" == "stream" ]]; then
    stream_flag=true
  fi
  local expected="UAT_${mode}_R${round}_S${slot}_END"
  local payload
  payload=$(jq -nc \
    --arg model "$model" \
    --arg expected "$expected" \
    --argjson stream "$stream_flag" \
    '{model:$model,max_tokens:80,stream:$stream,messages:[{role:"user",content:("Return one compact JSON object only. The object must have exactly one field named uat with this exact string value: "+$expected)}]}')

  local start end status text ok
  start=$(date +%s%3N)
  status=$(curl -sS --max-time 120 -D "$header_file" -o "$body_file" -w '%{http_code}' \
    'http://127.0.0.1:18080/v1/messages' \
    -H 'Content-Type: application/json' \
    -H 'anthropic-version: 2023-06-01' \
    -H "Authorization: Bearer $api_key" \
    -H "x-claude-code-session-id: $req_id" \
    -H "x-client-request-id: $req_id" \
    --data "$payload" || true)
  end=$(date +%s%3N)

  if [[ "$mode" == "stream" ]]; then
    text=$(grep '^data: ' "$body_file" | sed 's/^data: //' | jq -r 'select(.type=="content_block_delta") | .delta.text // empty' 2>/dev/null | tr -d '\n' || true)
  else
    text=$(jq -r '[.content[]? | select(.type=="text") | .text] | join("")' "$body_file" 2>/dev/null || true)
  fi

  local parsed_text
  parsed_text=$(printf '%s' "$text" | jq -r '.uat // empty' 2>/dev/null || true)
  if [[ -z "$parsed_text" ]]; then
    parsed_text=$(printf '%s' "$text" | grep -o '{[^}]*}' | head -1 | jq -r '.uat // empty' 2>/dev/null || true)
  fi

  ok=false
  if [[ "$status" == "200" && "$parsed_text" == "$expected" ]]; then
    ok=true
  fi

  jq -nc \
    --arg mode "$mode" \
    --arg req_id "$req_id" \
    --arg expected "$expected" \
    --arg text "$text" \
    --arg parsed_text "$parsed_text" \
    --arg status "$status" \
    --arg body_file "$body_file" \
    --arg header_file "$header_file" \
    --argjson round "$round" \
    --argjson slot "$slot" \
    --argjson duration_ms "$((end-start))" \
    --argjson ok "$ok" \
    '{mode:$mode,round:$round,slot:$slot,request_id:$req_id,status:($status|tonumber? // 0),duration_ms:$duration_ms,expected:$expected,text:$text,parsed_text:$parsed_text,ok:$ok,body_file:$body_file,header_file:$header_file}' >> "$mode_dir/results.jsonl"

  if [[ "$ok" != "true" ]]; then
    printf '%s status=%s expected=%s parsed=%s text=%s body=%s\n' "$req_id" "$status" "$expected" "$parsed_text" "$text" "$body_file" >> "$mode_dir/failures.txt"
  fi
}

for round in $(seq 1 "$rounds"); do
  for slot in $(seq 1 "$concurrency"); do
    run_one "$round" "$slot" &
  done
  wait
  jq -s --arg mode "$mode" --argjson round "$round" '
    {mode:$mode,round:$round,total:length,ok:map(select(.ok))|length,failed:map(select(.ok|not))|length,max_duration_ms:(map(.duration_ms)|max),status_counts:(group_by(.status)|map({status:.[0].status,count:length}))}
  ' "$mode_dir/results.jsonl" > "$mode_dir/round-${round}-summary.json"
  cat "$mode_dir/round-${round}-summary.json"
done

jq -s --arg mode "$mode" '
  {mode:$mode,total:length,ok:map(select(.ok))|length,failed:map(select(.ok|not))|length,status_counts:(group_by(.status)|map({status:.[0].status,count:length})),wrong_text:map(select(.status==200 and .ok|not))|length,max_duration_ms:(map(.duration_ms)|max),avg_duration_ms:((map(.duration_ms)|add)/length)}
' "$mode_dir/results.jsonl" > "$mode_dir/summary.json"
cat "$mode_dir/summary.json"
