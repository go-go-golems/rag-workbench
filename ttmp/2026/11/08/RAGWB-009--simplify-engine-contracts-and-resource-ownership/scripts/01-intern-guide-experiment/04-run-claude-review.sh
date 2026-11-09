#!/usr/bin/env bash
set -u
cd /home/manuel/workspaces/2026-11-02/rag-workbench/rag-workbench-resume
base=ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership
experiment="$base/scripts/01-intern-guide-experiment"
claude --safe-mode --model claude-opus-5-5 --print --tools Read,Glob,Grep --allowedTools Read Glob Grep < "$experiment/03-claude-newcomer-prompt.md" > "$experiment/claude-review-output.md" 2> "$experiment/claude-review-stderr.log"
result=$?
printf '%s\n' "$result" > "$experiment/claude-review-exit-code.txt"
printf 'Claude review exited: %s\n' "$result"
