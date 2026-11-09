# Optional workflow checks

The standard-library helper is `scripts/workflow_checks.py` relative to this skill. It does not load skills into the model, run a renderer, upload documents or prove that a visual review occurred. Its policy fixtures and known-conflict checks are intentionally explicit, not a general natural-language theorem prover.

```bash
python3 scripts/workflow_checks.py skills
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_workflow_checks.py' -v
```

Run these from the skill directory. The skills check validates the five owned cores and follows explicit local Markdown links. It checks name/description presence, name syntax, broken links and specific contradictory upload/adapter/authorization rules. Bare path mentions and arbitrary semantic contradictions still need human review.

## Dependency-aware validation

A manifest declares actual document/asset files, renderer version, validation policy and stylesheet. All paths resolve within `--root`. For example:

```json
{
  "schema_version": 1,
  "renderer": "mermaid-11.12.2+review-script-revision",
  "policy": "all-figures-v1",
  "stylesheet": "review.css",
  "documents": [{"path": "chapter.md", "assets": ["figure.svg"]}]
}
```

Declare all includes and external inputs used by the renderer. The key incorporates file bytes, asset hashes, stylesheet bytes, declared renderer identity and policy. A changed shared input invalidates all dependents. Missing dependencies fail rather than count as a cache hit.

```bash
python3 scripts/workflow_checks.py plan --root /path/to/docs \
  --input manifest.json --state validated.json
```

Run the actual affected checks and inspect **every** affected figure. Then provide review attestations:

```json
{"chapter.md": {"checks_passed": true, "reviewed_figures": ["mermaid:1", "mermaid:2"]}}
```

The current recognizer enumerates Mermaid blocks and inline Markdown images (`image:1`, etc.) outside fenced code examples; it is not a complete Markdown/HTML parser. Review additional generated/HTML figures explicitly outside the helper. Never treat unsupported syntax as proof no diagrams exist.

```bash
python3 scripts/workflow_checks.py record --root /path/to/docs \
  --input manifest.json --state validated.json --reviews reviews.json
```

Only record after real checks. The helper rejects missing second-diagram review and failed check attestations, but cannot verify whether an attestation is truthful. At final delivery, audit the full contract and integration regardless of cache hits.

## Resume and evaluation

```bash
python3 scripts/workflow_checks.py resume --input resume-output.json
python3 scripts/workflow_checks.py compare --input before.json --reviews after.json
```

Resume accepts the service object or the CLI's one-row `resume` wrapper and preserves conflict warnings; it checks shape only. The files and current request remain authoritative.

Comparison inputs require task_class, requirements, covered_requirements, retained_failure_context, unsupported_success_claims, redundant_reloads, independent_state_records and unnecessary_mutation_diffs. Curate these from frozen transcripts/artifacts, preserving provenance and converter versions. Different tasks/contracts are rejected; changed coverage or failure context triggers review even when administrative counts fall.

The shipped tests are deterministic policy/dependency fixtures, not evidence that future agents use fewer tokens. Re-measure comparable real sessions after adoption. Report narrower observed changes and caveats; do not attribute a whole session's wall time to this workflow.
