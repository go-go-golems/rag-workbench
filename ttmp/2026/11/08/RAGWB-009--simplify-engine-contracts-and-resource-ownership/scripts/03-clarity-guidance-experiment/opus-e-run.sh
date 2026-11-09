#!/usr/bin/env bash
set -u
cd /home/manuel/workspaces/2026-11-02/rag-workbench/rag-workbench-resume
experiment="ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership/scripts/03-clarity-guidance-experiment"
claude --safe-mode --model claude-opus-5-5 --effort medium --session-id de128437-9267-4e26-9cd9-095d123a71b1 --print --output-format json --tools Read,Glob,Grep,Write --allowedTools Read Glob Grep Write < "$experiment/opus-e-prompt.md" > "$experiment/opus-e-result.json" 2> "$experiment/opus-e-stderr.log"
result=$?
printf '%s\n' "$result" > "$experiment/opus-e-exit-code.txt"
printf 'Claude writer exited: %s\n' "$result"
