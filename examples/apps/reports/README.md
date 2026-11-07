# Reports evaluation

The challenge set tests retrieval over the bundled reports with 12 answerable questions and 8 plausible questions whose requested facts are absent from this corpus. The labels describe this corpus; they do not claim the facts cannot be found elsewhere.

Run the offline example:

```sh
go run ./cmd/ragwb --decide fake --no-store verbs --dir examples/apps/reports reports challenge --names lexical,fused,graded,filtered --threshold 50
```

Replace `fake` with a configured decision provider to assess a real model. The decision plans send questions and report excerpts to that provider. Repeat with different integer `--threshold` percentages to compare operating points. Add `--details` to inspect individual cases.

`hit5`, `mrr`, and `ndcg10` score ranking on answerable cases. `false_rejects` counts answerable questions for which no passages survived. `rejected` and `false_accepts` count unanswerable questions with zero and nonzero returned passages, respectively. The challenge tests passage rejection, not the correctness of a generated answer. Provider errors and answer labels that match no corpus sections abort the run. The fake is a deterministic wiring check, not evidence of model quality or threshold calibration.
