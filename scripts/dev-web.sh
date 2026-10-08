#!/bin/sh
# 개발용: harness/go 소스가 바뀌면 웹 UI 를 다시 빌드하고 재시작한다.
# 빌드가 실패하면 기존 서버를 그대로 두고 오류만 출력한다.
#
#   scripts/dev-web.sh [웹 UI 주소]     # 기본 127.0.0.1:8787, Ctrl+C 로 종료
#
# 화면은 서버가 바뀐 것을 알아채고 "새 버전 적용됨" 배너를 띄운다 (자동 새로고침은 하지 않는다).
set -u

top=$(cd "$(dirname "$0")/.." && pwd)
src="$top/harness/go"
addr="${1:-127.0.0.1:8787}"
bin="${TMPDIR:-/tmp}/llmtest-dev-$$"
pid=""
opened=0

snapshot() {
  # 소스 파일 목록과 수정 시각으로 변경을 감지한다 (macOS·Linux stat 둘 다 지원).
  find "$src" -type f \( -name '*.go' -o -path '*/web/*' -o -name 'go.mod' \) -print 2>/dev/null | sort |
    while read -r f; do stat -f '%m %N' "$f" 2>/dev/null || stat -c '%Y %n' "$f"; done | cksum
}

stop() {
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null
    wait "$pid" 2>/dev/null
  fi
  pid=""
}

cleanup() { stop; rm -f "$bin" "$bin.new"; exit 0; }
trap cleanup INT TERM

start() {
  if ! (cd "$src" && go build -o "$bin.new" .); then
    echo "[dev-web] 빌드 실패 — 기존 서버 유지" >&2
    return
  fi
  stop
  mv "$bin.new" "$bin"
  open_flag="-no-open"
  [ "$opened" -eq 0 ] && open_flag="" && opened=1
  (cd "$src" && exec "$bin" -web -addr "$addr" $open_flag) &
  pid=$!
  echo "[dev-web] $(date '+%Y-%m-%d %H:%M:%S') 재시작 (pid $pid)"
}

last=""
while :; do
  cur=$(snapshot)
  if [ "$cur" != "$last" ]; then
    last="$cur"
    start
  fi
  sleep 1
done
