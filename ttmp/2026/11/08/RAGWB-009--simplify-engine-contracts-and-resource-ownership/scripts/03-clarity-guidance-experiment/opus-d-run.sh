#!/usr/bin/env bash
set -u
cd /home/manuel/workspaces/2026-11-02/rag-workbench/rag-workbench-resume
experiment="ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership/scripts/03-clarity-guidance-experiment"
claude --safe-mode --model claude-opus-5-5 --effort medium --session-id 02eb8173-39ab-4287-b7e3-3339e99dc026 --print --output-format json --tools Read,Glob,Grep,Write --allowedTools Read Glob Grep Write < "$experiment/opus-d-prompt.md" > "$experiment/opus-d-result.json" 2> "$experiment/opus-d-stderr.log"
result=$?
printf '%s\n' "$result" > "$experiment/opus-d-exit-code.txt"
printf 'Claude writer exited: %s\n' "$result"
