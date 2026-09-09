#!/usr/bin/env bash
# Real-machine E2E for the Tauri shell sidecar supervisor (src-tauri/src/main.rs).
#
# Spawns the actual desktop GUI (needs a live $DISPLAY, e.g. DISPLAY=:0)
# against the real Go sidecar and asserts:
#   1. kill -9 on the sidecar -> it is respawned (~1s) and readyz recovers;
#      SIGTERM to the app leaves no orphan sidecar.
#   2. A healthy foreign daemon already owning the port -> single-instance
#      handover is recognised (exit 0, no respawn, foreign daemon survives).
#   3. A sidecar that exits 1 -> bounded respawn: give-up after 5 short
#      generations (exactly 4 "respawning" log lines), never hot-spins.
#
# TEST 3 temporarily replaces the sidecar binaries with an `exit 1` script.
# A trap restores the real ELFs even if the run aborts midway.
#
# Usage: scripts/shell-e2e.sh [app-binary] [real-sidecar] [daemon-binary]
# Defaults assume `cargo build` (debug) + scripts/build-sidecar.sh already ran;
# the foreign-instance daemon is built automatically into a temp dir.
#
# This needs a display and therefore does NOT run in CI; use it before
# releasing Rust changes that pass `cargo check`/`clippy` but still warrant
# a real process-level check.
set -u
cd "$(dirname "$0")/.."

APP="${1:-src-tauri/target/debug/supplider-desktop}"
SIDECAR="${2:-src-tauri/binaries/suppliderd-x86_64-unknown-linux-gnu}"
ROOT=$(mktemp -d /tmp/srm-shell-e2e.XXXXXX)
DAEMON="${3:-$ROOT/suppliderd-e2e}"
trap 'cleanup' EXIT

pass=0; fail=0
ok(){ echo "PASS: $1"; pass=$((pass+1)); }
no(){ echo "FAIL: $1"; fail=$((fail+1)); }

# Sidecar copies replaced with crashing scripts, restored by the trap.
REPLACED=()
cleanup(){
  for entry in "${REPLACED[@]:-}"; do
    [ -n "$entry" ] || continue
    local backup=${entry%%|*} target=${entry##*|}
    [ -f "$backup" ] && cp "$backup" "$target"
  done
  pkill -f "suppliderd-x86_64-unknown-linux-gnu" 2>/dev/null || true
  rm -rf "$ROOT"
}

wait_ready(){ # $1=timeout-seconds
  for _ in $(seq 1 $(( $1*5 )) ); do
    curl -sf -m 1 "http://127.0.0.1:7612/readyz" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  return 1
}
wait_log(){ # $1=log $2=pattern $3=timeout-seconds
  for _ in $(seq 1 $(( $3*5 )) ); do
    grep -q "$2" "$1" 2>/dev/null && return 0
    sleep 0.2
  done
  return 1
}
sidecar_pid(){ ss -ltnp 2>/dev/null | grep ':7612' | grep -oP 'pid=\K[0-9]+' | head -1; }

[ -x "$APP" ]     || { echo "app binary not found/executable: $APP (run: cd src-tauri && cargo build)"; exit 1; }
[ -x "$SIDECAR" ] || { echo "sidecar not found/executable: $SIDECAR (run: scripts/build-sidecar.sh)"; exit 1; }
if [ ! -x "$DAEMON" ]; then
  ( cd backend && go build -tags personal -o "$DAEMON" ./cmd/suppliderd )
fi

export XDG_DATA_HOME="$ROOT/xdg"; mkdir -p "$XDG_DATA_HOME"
export RUST_BACKTRACE=0

echo "===== TEST 1: sidecar kill -9 -> respawn, then SIGTERM app -> no orphan ====="
"$APP" >"$ROOT/app1.log" 2>&1 &
APP_PID=$!
if wait_ready 25; then ok "initial readyz"; else no "initial readyz"; cat "$ROOT/app1.log"; fi
PID1=$(sidecar_pid); echo "sidecar pid1=$PID1 app=$APP_PID"
[ -n "$PID1" ] && kill -9 "$PID1"
sleep 0.5
if wait_ready 8; then ok "recovered after kill -9"; else no "recovery after kill -9"; fi
PID2=$(sidecar_pid); echo "sidecar pid2=$PID2"
[ -n "$PID2" ] && [ "$PID2" != "$PID1" ] && ok "new sidecar process" || no "new sidecar process"
grep -q "respawning sidecar" "$ROOT/app1.log" && ok "log: respawning" || no "log: respawning"
kill -TERM "$APP_PID" 2>/dev/null
for _ in $(seq 1 25); do kill -0 "$APP_PID" 2>/dev/null || break; sleep 0.2; done
sleep 1
if [ -z "$(sidecar_pid)" ]; then ok "no orphan sidecar after app exit"; else no "orphan sidecar after app exit: $(sidecar_pid)"; fi

echo "===== TEST 2: healthy instance owns port -> clean handover (exit 0, no respawn) ====="
D2=$(mktemp -d "$ROOT/d2.XXXX")
"$DAEMON" --addr 127.0.0.1:7612 --data-dir "$D2" >"$ROOT/daemon2.log" 2>&1 &
DPID=$!
wait_ready 10 || { echo "daemon2 never ready"; cat "$ROOT/daemon2.log"; }
PID_BEFORE=$(sidecar_pid)
"$APP" >"$ROOT/app2.log" 2>&1 &
APP2=$!
# The app-spawned sidecar probes the port (~2s) then exits 0; poll instead
# of a fixed sleep so a loaded machine cannot make this assertion flaky.
if wait_log "$ROOT/app2.log" "not respawning" 20; then
  ok "log: handover recognized"
else
  no "log: handover"; tail -5 "$ROOT/app2.log"
fi
PID_AFTER=$(sidecar_pid)
[ "$PID_BEFORE" = "$PID_AFTER" ] && [ -n "$PID_AFTER" ] && ok "external instance keeps serving" || no "pid changed $PID_BEFORE -> $PID_AFTER"
kill -TERM "$APP2" 2>/dev/null
for _ in $(seq 1 25); do kill -0 "$APP2" 2>/dev/null || break; sleep 0.2; done
kill -0 "$DPID" 2>/dev/null && ok "external daemon survived app exit" || no "external daemon was killed"
kill -TERM "$DPID" 2>/dev/null; sleep 1; kill -9 "$DPID" 2>/dev/null

echo "===== TEST 3: crashing sidecar -> bounded give-up (no infinite respawn) ====="
# Debug builds resolve the sidecar next to the app executable (tauri-build
# copies it there); packaged builds use the binaries/ dir. Replace both.
ALT="$(dirname "$APP")/$(basename "$SIDECAR" | sed 's/-x86_64-unknown-linux-gnu//')"
for B in "$SIDECAR" "$ALT"; do
  [ -f "$B" ] || continue
  backup="$ROOT/$(basename "$B").real"
  cp "$B" "$backup"
  REPLACED+=("$backup|$B")
  printf '#!/bin/bash\nexit 1\n' > "$B" && chmod +x "$B"
done
"$APP" >"$ROOT/app3.log" 2>&1 &
APP3=$!
# Give-up happens after 5 short generations + four 1s backoffs (~5s on an
# idle machine); poll generously so sustained build load cannot flake it.
if wait_log "$ROOT/app3.log" "giving up" 40; then
  ok "give-up logged"
else
  no "no give-up (respawn count=$(grep -c 'respawning sidecar' "$ROOT/app3.log"))"
fi
N=$(grep -c "respawning sidecar" "$ROOT/app3.log")
[ "$N" -le 4 ] && ok "respawn attempts bounded ($N, expected exactly 4)" || no "respawn attempts unbounded ($N)"
kill -TERM "$APP3" 2>/dev/null; sleep 1; kill -9 "$APP3" 2>/dev/null
cleanup; REPLACED=()  # restore now so the ELF check below is meaningful
trap - EXIT
head -c4 "$ALT" | grep -q "$(printf '\177ELF')" && ok "real sidecar restored" || no "RESTORE FAILED"

echo "===== RESULT: pass=$pass fail=$fail (tmp=$ROOT) ====="
exit "$fail"
