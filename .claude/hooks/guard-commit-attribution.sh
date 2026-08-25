#!/usr/bin/env bash
# PreToolUse(Bash) hook: block `git commit` that carries AI-authorship attribution.
#
# Contract:
#   - stdin: JSON with .tool_input.command (the bash command about to run)
#   - block: exit 2, message on stderr (fed back to Claude)
#   - allow: exit 0, no output (transparent — normal permission flow continues)
#
# Scope/limits: only inspects the command string, so it catches `-m "..."` and
# heredoc messages. It cannot see messages supplied via `-F <file>` or the editor;
# those are left to the pre-commit hook / review. Failing open (exit 0) is chosen
# over blocking every commit when JSON can't be parsed.
set -euo pipefail

. "$(dirname "$0")/lib.sh" || exit 0

cmd="$(extract_tool_input_field command)"
[ -z "$cmd" ] && exit 0

# Only guard git commits.
printf '%s' "$cmd" | grep -Eq 'git[[:space:]]+([^|&;]*[[:space:]])?commit' || exit 0

# AI-attribution signals. Curated to avoid false positives on ordinary messages.
if printf '%s' "$cmd" | grep -iEq \
  'co-authored-by:[^\n]*(claude|anthropic)|noreply@anthropic\.com|generated (with|by)[^\n]*claude|🤖[^\n]*generated'; then
  echo "BLOCKED: このコミットメッセージに AI 著作権表記が含まれています（CLAUDE.md の絶対ルール違反）。" >&2
  echo "Co-Authored-By: Claude / Generated with Claude / noreply@anthropic.com などの表記を削除してから再実行してください。" >&2
  exit 2
fi

exit 0
