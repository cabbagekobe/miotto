# hooks 共通ライブラリ（各 hook から source して使う）。
#
# extract_tool_input_field <field>:
#   stdin の hook JSON から .tool_input.<field> を取り出して stdout に返す。
#   jq → python3 の順で試し、どちらも無ければ空を返す（呼び出し側が安全側に倒す）。
extract_tool_input_field() {
  local field="$1" input
  input="$(cat)"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$input" | jq -r ".tool_input.${field} // empty" 2>/dev/null && return 0
  fi
  if command -v python3 >/dev/null 2>&1; then
    printf '%s' "$input" | python3 -c 'import sys,json
try:
    print(json.load(sys.stdin).get("tool_input",{}).get(sys.argv[1],""))
except Exception:
    pass' "$field" 2>/dev/null && return 0
  fi
  return 0
}
