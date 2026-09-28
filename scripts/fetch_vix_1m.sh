#!/usr/bin/env bash
# Backfill India VIX (token 99926017) 1m candles into backend/data/historical.db.
# One weekday per histdata call, paced and retried because the Angel One
# historical API rate limit is bursty. Days already present are skipped,
# so re-running only retries missing/failed days.
#
# Usage: ./scripts/fetch_vix_1m.sh [FROM] [TO]   (dates YYYY-MM-DD, TO exclusive)
set -u
FROM=${1:-2026-03-02}
TO=${2:-$(date -d tomorrow +%F)}
TOKEN=99926017

cd "$(dirname "$0")/../backend"
set -a; source ../.env.prod; set +a

BIN=$(mktemp -d)/histdata
go build -o "$BIN" ./cmd/histdata || exit 1

have=$(sqlite3 data/historical.db "select distinct date(datetime(ts,'unixepoch','+330 minutes')) from historical_candles where token='$TOKEN'")
d=$FROM; ok=0; nod=0; fail=0
while [[ "$d" < "$TO" ]]; do
  dow=$(date -d "$d" +%u)
  if [[ $dow -le 5 ]] && ! grep -q "$d" <<<"$have"; then
    res=fail
    for try in 1 2 3 4; do
      out=$("$BIN" --exchange=NSE --token=$TOKEN --from=$d --to=$d 2>&1 | grep -v "password")
      if grep -qE "[0-9]+ candles stored" <<<"$(grep -v Total <<<"$out")"; then res=ok; break; fi
      if grep -q "no data" <<<"$out"; then res=nodata; break; fi
      sleep $((try*20))
    done
    echo "$d $res"
    case $res in ok) ok=$((ok+1));; nodata) nod=$((nod+1));; *) fail=$((fail+1));; esac
    sleep 8
  fi
  d=$(date -d "$d +1 day" +%F)
done
echo "DONE ok=$ok nodata=$nod failed=$fail"
# rows | distinct days | min close | max close
sqlite3 data/historical.db "select count(*), count(distinct date(datetime(ts,'unixepoch','+330 minutes'))), min(close), max(close) from historical_candles where token='$TOKEN'"
