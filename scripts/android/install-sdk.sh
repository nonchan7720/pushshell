#!/usr/bin/env bash
# Android SDK を $ANDROID_HOME に冪等にインストールする (mise run android:sdk)。
#
# - cmdline-tools は Google のリポジトリ XML から最新の安定版を解決して取得する
# - React Native 0.86 / Expo SDK 57 が要求する platform / build-tools / NDK / cmake を入れる
# - ANDROID_EMULATOR=1 のときは emulator と system image も入れる (mise run android:emulator:install)
#
# 必要な環境変数 (.mise.toml の [env] で設定済み): ANDROID_HOME, JAVA_HOME (mise の java)
set -euo pipefail

: "${ANDROID_HOME:?ANDROID_HOME is not set (run via mise)}"
: "${ANDROID_PLATFORM:=android-36}"
: "${ANDROID_BUILD_TOOLS:=36.0.0}"
: "${ANDROID_NDK_VERSION:=27.1.12297006}"
: "${ANDROID_CMAKE_VERSION:=3.22.1}"
: "${ANDROID_SYSTEM_IMAGE:=system-images;${ANDROID_PLATFORM};google_apis;x86_64}"
REPO_XML="https://dl.google.com/android/repository/repository2-3.xml"

os_tag() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo macosx ;;
    *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
}

# 最新の安定版 cmdline-tools の zip URL を解決する
resolve_cmdline_tools_url() {
  curl -fsSL "$REPO_XML" | python3 -c '
import re, sys
xml = sys.stdin.read()
os_tag = sys.argv[1]
best = None
for m in re.finditer(r"<remotePackage path=\"cmdline-tools;([0-9.]+)\">(.*?)</remotePackage>", xml, re.S):
    ver, body = m.group(1), m.group(2)
    key = tuple(int(x) for x in ver.split("."))
    for a in re.finditer(r"<archive>(.*?)</archive>", body, re.S):
        if f"<host-os>{os_tag}</host-os>" in a.group(1):
            url = re.search(r"<url>(.*?)</url>", a.group(1)).group(1)
            if best is None or key > best[0]:
                best = (key, url)
if not best:
    sys.exit("cmdline-tools archive not found")
print("https://dl.google.com/android/repository/" + best[1])
' "$1"
}

mkdir -p "$ANDROID_HOME"
SDKMANAGER="$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"

if [ ! -x "$SDKMANAGER" ]; then
  url="$(resolve_cmdline_tools_url "$(os_tag)")"
  echo "==> downloading cmdline-tools: $url"
  tmp="$(mktemp -d)"
  curl -fsSL "$url" -o "$tmp/cmdline-tools.zip"
  unzip -q "$tmp/cmdline-tools.zip" -d "$tmp"
  mkdir -p "$ANDROID_HOME/cmdline-tools"
  rm -rf "$ANDROID_HOME/cmdline-tools/latest"
  mv "$tmp/cmdline-tools" "$ANDROID_HOME/cmdline-tools/latest"
  rm -rf "$tmp"
fi

packages=(
  "platform-tools"
  "platforms;${ANDROID_PLATFORM}"
  "build-tools;${ANDROID_BUILD_TOOLS}"
  "ndk;${ANDROID_NDK_VERSION}"
  "cmake;${ANDROID_CMAKE_VERSION}"
)
if [ "${ANDROID_EMULATOR:-0}" = "1" ]; then
  packages+=("emulator" "${ANDROID_SYSTEM_IMAGE}")
fi

# ライセンス承認。新しい cmdline-tools では --licenses は不要 (警告が出るだけ) なので失敗は無視する。
# `yes` は相手が先に終了すると SIGPIPE で終わるため、この区間だけ pipefail を外す。
echo "==> accepting licenses"
set +o pipefail
yes 2>/dev/null | "$SDKMANAGER" --sdk_root="$ANDROID_HOME" --licenses > /dev/null 2>&1 || true
echo "==> installing: ${packages[*]}"
yes 2>/dev/null | "$SDKMANAGER" --sdk_root="$ANDROID_HOME" "${packages[@]}"
status=${PIPESTATUS[1]}
set -o pipefail
if [ "$status" -ne 0 ]; then
  echo "sdkmanager failed with exit code $status" >&2
  exit "$status"
fi

echo "==> installed packages"
"$SDKMANAGER" --sdk_root="$ANDROID_HOME" --list_installed
