#!/usr/bin/env bash
# PostToolUse(Edit|Write) hook: format ONLY the file that was just edited.
#
# Contract:
#   - stdin: JSON with .tool_input.file_path
#   - always exits 0 (formatting must never block an edit)
#
# Behavior: pick a formatter from the extension, but run it only when both the
# tool is installed AND the project actually opted into it (a config file exists
# somewhere above the edited file). Otherwise it is a silent no-op. Single file =
# fast. This continuously undoes format drift as code is written.
set -uo pipefail

. "$(dirname "$0")/lib.sh" || exit 0

file="$(extract_tool_input_field file_path)"
[ -z "$file" ] && exit 0
[ -f "$file" ] || exit 0
case "$file" in
  *node_modules/*|*/.git/*|*/dist/*|*/build/*|*/.venv/*) exit 0 ;;
esac

# find_up <start-dir> <name...>: is any of <name> present at or above start-dir?
find_up() {
  local dir="$1"; shift
  while [ -n "$dir" ] && [ "$dir" != "/" ]; do
    for n in "$@"; do
      [ -e "$dir/$n" ] && return 0
    done
    dir="$(dirname "$dir")"
  done
  return 1
}

dir="$(cd "$(dirname "$file")" 2>/dev/null && pwd)" || exit 0
ext="${file##*.}"

run() { "$@" >/dev/null 2>&1 || true; }

case "$ext" in
  ts|tsx|js|jsx|mjs|cjs|json|jsonc|css|scss|md|mdx|html|yaml|yml)
    if command -v biome >/dev/null 2>&1 && find_up "$dir" biome.json biome.jsonc; then
      run biome format --write --files-ignore-unknown=true "$file"
    elif find_up "$dir" .prettierrc .prettierrc.json .prettierrc.js prettier.config.js .prettierrc.yaml .prettierrc.yml; then
      if command -v prettier >/dev/null 2>&1; then run prettier --write "$file"
      elif command -v npx >/dev/null 2>&1; then run npx --no-install prettier --write "$file"; fi
    fi
    ;;
  py)
    if command -v ruff >/dev/null 2>&1 && find_up "$dir" pyproject.toml ruff.toml .ruff.toml; then
      run ruff format "$file"
      run ruff check --fix-only "$file"
    fi
    ;;
  swift)
    if command -v swiftformat >/dev/null 2>&1 && find_up "$dir" .swiftformat Package.swift; then
      run swiftformat "$file"
    fi
    ;;
  go)
    if command -v gofmt >/dev/null 2>&1 && find_up "$dir" go.mod; then
      run gofmt -w "$file"
    fi
    ;;
esac

exit 0
