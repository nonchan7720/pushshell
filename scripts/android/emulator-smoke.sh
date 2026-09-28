#!/usr/bin/env bash
# エミュレータ (または接続中の端末) に APK をインストールして起動し、スクリーンショットと
# logcat を保存するスモークテスト。GitHub Actions の android-emulator ワークフローと
# ローカル (mise run android:emulator 起動後) の両方で使う。
#
#   scripts/android/emulator-smoke.sh <apk> <out-dir> [package] [shots-seconds...]
#
# 終了コード: 0 = 最後の撮影時点でアプリのプロセスが生きている / 1 = 落ちた or 起動できない
set -euo pipefail

APK="${1:?apk path}"
OUT="${2:?output dir}"
PACKAGE="${3:-com.example.webappnotification}"
shift 3 2>/dev/null || shift $#
SHOT_TIMES=("$@")
if [ ${#SHOT_TIMES[@]} -eq 0 ]; then SHOT_TIMES=(20 45 90); fi

mkdir -p "$OUT"
log() { echo "[$(date +%T)] $*"; }

log "waiting for device"
adb wait-for-device
for _ in $(seq 1 60); do
  if [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ]; then break; fi
  sleep 5
done
adb shell settings put global window_animation_scale 0 >/dev/null 2>&1 || true
adb shell settings put global transition_animation_scale 0 >/dev/null 2>&1 || true
adb shell settings put global animator_duration_scale 0 >/dev/null 2>&1 || true

log "installing $APK"
adb install -r "$APK"

# Web アプリ / バックエンドがホスト側で動いている場合、エミュレータからは 10.0.2.2 で届くが、
# ゲストのネットワークが無い環境でも動くように adb reverse も張っておく。
for p in ${SMOKE_REVERSE_PORTS:-8080 8090}; do adb reverse "tcp:$p" "tcp:$p" >/dev/null 2>&1 || true; done

adb logcat -c || true
log "launching $PACKAGE"
adb shell am start -W -n "$PACKAGE/.MainActivity" | grep -E "Status|Error" || true

prev=0
alive=1
for t in "${SHOT_TIMES[@]}"; do
  sleep $((t - prev)); prev=$t
  adb exec-out screencap -p > "$OUT/screen-${t}s.png" || true
  pid="$(adb shell pidof "$PACKAGE" 2>/dev/null | tr -d '\r' || true)"
  log "screenshot at ${t}s ($(stat -c %s "$OUT/screen-${t}s.png" 2>/dev/null || echo 0) bytes), pid=${pid:-none}"
  if [ -z "$pid" ]; then alive=0; fi
done

adb logcat -d > "$OUT/logcat.txt" || true
grep -E "ReactNativeJS|\[App\]|\[notifications\]|Fatal signal|AndroidRuntime" "$OUT/logcat.txt" | tail -50 > "$OUT/logcat-app.txt" || true
adb shell dumpsys window 2>/dev/null | grep -E "mCurrentFocus|mFocusedApp" | head -2 > "$OUT/window.txt" || true

if [ "$alive" -ne 1 ]; then
  log "app process died; see $OUT/logcat-app.txt"
  exit 1
fi
log "done"
