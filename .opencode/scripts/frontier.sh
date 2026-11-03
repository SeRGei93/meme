#!/usr/bin/env bash
set -euo pipefail

repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)

echo "Доступные тикеты (все blocked-by закрыты):"

issues=$(gh issue list \
  --repo "$repo" \
  --state open \
  --label ready-for-agent \
  --limit 100 \
  --json number,title \
  --jq '.[] | "\(.number)\t\(.title)"' | sort -n)

found=0
while IFS=$'\t' read -r number title; do
  [[ -z "${number:-}" ]] && continue

  open_blockers=$(gh api "repos/$repo/issues/$number/dependencies/blocked_by" \
    --jq '[.[] | select(.state == "open")] | length')

  if [[ "$open_blockers" -eq 0 ]]; then
    printf '#%s\t%s\n' "$number" "$title"
    found=1
  fi
done <<< "$issues"

if [[ "$found" -eq 0 ]]; then
  echo "Нет доступных тикетов: все ready-for-agent закрыты или заблокированы."
fi
