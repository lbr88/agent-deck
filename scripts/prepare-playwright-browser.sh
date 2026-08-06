#!/usr/bin/env bash

set -euo pipefail

prepare_playwright_browser() {
  if [ -n "${PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH:-}" ]; then
    echo "[precommit-ci] Using explicitly configured Chromium at $PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH."
    return 0
  fi

  # Playwright has no Ubuntu 26.04 platform entry yet. Its documented host
  # override lets the compatible 24.04 browser build install and be located at
  # runtime. Keep an explicit caller-provided override authoritative.
  if [ -z "${PLAYWRIGHT_HOST_PLATFORM_OVERRIDE:-}" ] && [ -r /etc/os-release ]; then
    os_id=""
    os_version=""
    while IFS='=' read -r key value; do
      case "$key" in
        ID) os_id="${value%\"}"; os_id="${os_id#\"}" ;;
        VERSION_ID) os_version="${value%\"}"; os_version="${os_version#\"}" ;;
      esac
    done < /etc/os-release

    os_major="${os_version%%.*}"
    case "$(uname -m)" in
      x86_64) playwright_arch="x64" ;;
      aarch64|arm64) playwright_arch="arm64" ;;
      *) playwright_arch="" ;;
    esac
    if [ "$os_id" = "ubuntu" ] && [ -n "$playwright_arch" ] &&
      [ "${os_major:-0}" -ge 25 ] 2>/dev/null; then
      export PLAYWRIGHT_HOST_PLATFORM_OVERRIDE="ubuntu24.04-$playwright_arch"
      echo "[precommit-ci] Using Playwright platform fallback $PLAYWRIGHT_HOST_PLATFORM_OVERRIDE."
    fi
  fi

  echo "[precommit-ci] Installing Playwright's matching Chromium build."
  ./node_modules/.bin/playwright install chromium
}

playwright_chromium_path() {
  local chromium_path="${PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH:-}"
  if [ -z "$chromium_path" ]; then
    chromium_path="$(node --input-type=module -e \
      "import { chromium } from '@playwright/test'; process.stdout.write(chromium.executablePath())")"
  fi
  if [ ! -x "$chromium_path" ]; then
    echo "ERROR: Playwright Chromium is not executable at $chromium_path." >&2
    return 1
  fi
  printf '%s' "$chromium_path"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  prepare_playwright_browser
fi
