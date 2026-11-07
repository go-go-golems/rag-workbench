# Changelog

## 2026-11-04

- Initial workspace created

## 2026-11-04

Design: intern guide for decision models in rag.js; batch spike (32-question limit)

## 2026-11-04

Implemented phases 1-6: ctx.decide, classify/when, filter.decide/rerank.decide, enrich.decide, /_rt/decide + workbench + GUI, reports plans; measured goldens and off-topic rejection

### Related Files

- /home/manuel/workspaces/2026-11-02/rag-workbench/rag-workbench/pkg/ragjs/assets/rag.js — Decision components and when
- /home/manuel/workspaces/2026-11-02/rag-workbench/rag-workbench/pkg/ragjs/decide.go — Go bridge

## 2026-11-06

Verify when edits across raw/component/lambda/replace cases and add a tested decision-routing workbench preset.

## 2026-11-06

Add corpus-relative challenge evaluation and offline ranking/rejection baseline; live calibration requires approval for external report excerpts.

## 2026-11-06

Measure the harder challenge against the approved Pi decision provider at thresholds 0.3, 0.5 and 0.7; record ranking and rejection results.

## 2026-11-06

Synchronize shared workspace with Geppetto v0.14.0 and the resumed workbench branch; verify build and focused tests.
