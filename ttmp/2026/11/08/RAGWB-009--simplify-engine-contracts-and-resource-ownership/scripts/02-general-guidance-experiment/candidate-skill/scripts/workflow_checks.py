#!/usr/bin/env python3
"""Explicit workflow convention checks; not a free-form policy or review oracle."""
import argparse
import hashlib
import json
import re
from pathlib import Path

PHASES = {'start', 'implement', 'validate', 'checkpoint', 'resume', 'close'}
OWNED = ('diary', 'docmgr', 'remarkable-upload', 'ticket-research-docmgr-remarkable', 'transcript-doc-friction-analysis')

def digest(data):
    return hashlib.sha256(data).hexdigest()

def local(root, path):
    result = (root / path).resolve()
    if not result.is_relative_to(root.resolve()):
        raise ValueError(f'path escapes root: {path}')
    if not result.is_file():
        raise ValueError(f'missing file: {path}')
    return result

def skill_checks(root):
    """Check owned cores and their local Markdown reference graph only."""
    errors = []
    for name in OWNED:
        core = root / name / 'SKILL.md'
        text = core.read_text()
        fm = text.split('---', 2)[1] if text.startswith('---\n') else ''
        for key in ('name', 'description'):
            if not re.search(rf'^{key}:\s*\S', fm, re.M):
                errors.append(f'{name}: missing {key}')
        names = re.findall(r'^name:\s*(.+)$', fm, re.M)
        if names and (len(names[0]) > 64 or not re.fullmatch(r'[a-z0-9]+(?:-[a-z0-9]+)*', names[0])):
            errors.append(f'{name}: invalid name')
        seen, todo = set(), [core]
        while todo:
            path = todo.pop()
            if path in seen:
                continue
            seen.add(path)
            body = path.read_text()
            # Explicit local Markdown links; code examples and bare path mentions
            # are not claimed to be exhaustively parsed.
            for link in re.findall(r'\]\(([^)#]+)(?:#[^)]*)?\)', body):
                if '://' in link or not link.endswith('.md'):
                    continue
                target = (path.parent / link).resolve()
                if not target.is_relative_to(root.resolve()) or not target.is_file():
                    errors.append(f'{path}: broken local reference {link}')
                else:
                    todo.append(target)
    research = (root / 'ticket-research-docmgr-remarkable/SKILL.md').read_text()
    checklist = (root / 'ticket-research-docmgr-remarkable/references/deliverable-checklist.md').read_text()
    for body in (research, checklist):
        if re.search(r'remarquee (?:status|cloud account|cloud ls)', body):
            errors.append('orchestrator duplicates specialist upload commands')
    upload = (root / 'remarkable-upload/SKILL.md').read_text()
    if 'Never run `remarquee cloud ls`' in upload:
        errors.append('unconditional listing prohibition conflicts with user verification')
    if 'Explicit user or higher-priority requirements override these defaults.' not in upload:
        errors.append('missing explicit-requirement override')
    style = (root / 'ticket-research-docmgr-remarkable/references/writing-style.md').read_text()
    if style.count('## Decision Records') != 1:
        errors.append('duplicate/missing decision-record policy')
    friction = (root / 'transcript-doc-friction-analysis/SKILL.md').read_text()
    if 'Codex `success` is always 1' in friction or 'Deliverables are always *changes*' in friction:
        errors.append('stale adapter or implementation-authorization rule')
    return errors

def upload_actions(*, explicit_dry=False, explicit_listing=False, overwrite_risk=False, ambiguous=False, auth_failed=False):
    """Executable acceptance fixture for the declared policy, not an uploader."""
    actions = []
    if overwrite_risk:
        actions.append('inspect-existing-state')
    if explicit_dry:
        actions.append('dry-run')
    actions.append('upload')
    if auth_failed:
        actions.append('one-reauth-retry-or-block')
    if explicit_listing or ambiguous:
        actions.append('independent-listing')
    return actions

def diary_mode(*, detailed=False, substantive=False, failures=False):
    return 'investigation' if detailed or substantive or failures else 'milestone'

def validate_resume(value):
    if isinstance(value, list) and len(value) == 1 and 'resume' in value[0]:
        value = value[0]['resume']
    if not isinstance(value, dict) or value.get('schema_version') != 1:
        raise ValueError('unsupported resume schema')
    if not isinstance(value.get('ticket'), str) or not value['ticket'] or value.get('phase') not in PHASES:
        raise ValueError('invalid ticket/phase')
    for key in ('remaining', 'documents', 'conflicts'):
        if not isinstance(value.get(key), list):
            raise ValueError(f'invalid {key}')
    if not isinstance(value.get('revisions'), dict):
        raise ValueError('invalid revisions')
    return {'valid': True, 'conflicts': value['conflicts'], 'note': 'shape check only; inspect actual current files and request'}

_FENCE_RE = re.compile(rb'^[ \t]{0,3}(`{3,}|~{3,})([^\r\n]*)\r?$')
_IMAGE_RE = re.compile(rb'!\[[^\]]*\]\([^)]+\)')


def enumerate_figures(raw):
    """Return rendered Mermaid blocks and Markdown images outside code fences."""
    mermaid_count = 0
    visible = []
    fence = None
    for line in raw.splitlines(keepends=True):
        match = _FENCE_RE.match(line)
        if fence is not None:
            if (match is not None
                    and match.group(1)[:1] == fence[0]
                    and len(match.group(1)) >= fence[1]
                    and not match.group(2).strip()):
                fence = None
            continue
        if match is not None:
            marker, info = match.groups()
            if marker == b'```' and info.strip() == b'mermaid':
                mermaid_count += 1
            fence = (marker[:1], len(marker))
            continue
        visible.append(line)

    figures = [f'mermaid:{i + 1}' for i in range(mermaid_count)]
    figures += [f'image:{i + 1}' for i, _ in enumerate(_IMAGE_RE.finditer(b''.join(visible)))]
    return figures


def keys(root, manifest):
    if manifest.get('schema_version') != 1 or not manifest.get('renderer') or not manifest.get('policy'):
        raise ValueError('manifest requires schema_version, renderer and policy')
    shared = {'renderer': manifest['renderer'], 'policy': manifest['policy'], 'stylesheet': digest(local(root, manifest['stylesheet']).read_bytes())}
    result = {}
    if len(manifest['documents']) > 512:
        raise ValueError('document limit exceeded')
    for doc in manifest['documents']:
        name = doc['path']
        if name in result:
            raise ValueError(f'duplicate document: {name}')
        raw = local(root, name).read_bytes()
        figures = enumerate_figures(raw)
        assets = {p: digest(local(root, p).read_bytes()) for p in doc.get('assets', [])}
        # All external renderer/includes must be declared in assets or shared inputs.
        value = {'shared': shared, 'body': digest(raw), 'assets': assets, 'figures': figures}
        result[name] = {'key': digest(json.dumps(value, sort_keys=True).encode()), 'figures': figures}
    return result

def validation_plan(root, manifest, state):
    current = keys(root, manifest)
    pending = [name for name, item in current.items() if state.get(name, {}).get('key') != item['key']]
    return current, pending

def record_validation(root, manifest, state, reviews):
    current, pending = validation_plan(root, manifest, state)
    for name in pending:
        review = reviews.get(name, {})
        if review.get('checks_passed') is not True or not set(current[name]['figures']).issubset(review.get('reviewed_figures', [])):
            raise ValueError(f'{name}: missing check success or review of every figure')
    return {name: {**item, 'review': reviews[name] if name in pending else state[name]['review']} for name, item in current.items()}

def compare_sessions(before, after):
    """Compare explicitly curated comparable evidence, never infer causal time savings."""
    if before['task_class'] != after['task_class'] or before['requirements'] != after['requirements']:
        raise ValueError('sessions are not comparable')
    guardrails = ('covered_requirements', 'retained_failure_context', 'unsupported_success_claims')
    regression = any(before[k] != after[k] for k in guardrails)
    metrics = ('redundant_reloads', 'independent_state_records', 'unnecessary_mutation_diffs')
    return {'regression_or_review_needed': regression, 'deltas': {k: after[k]-before[k] for k in metrics}, 'claim': 'curated comparable observations only; no causal time/cost attribution'}

def write_state(path, value):
    encoded = json.dumps(value, indent=2)+'\n'
    if not path.exists() or path.read_text() != encoded:
        path.write_text(encoded)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['skills', 'resume', 'plan', 'record', 'compare'])
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument('--input', type=Path)
    parser.add_argument('--state', type=Path)
    parser.add_argument('--reviews', type=Path)
    args = parser.parse_args()
    load = lambda p: json.loads(p.read_text())
    if args.command == 'skills':
        result = {'errors': skill_checks(args.root)}
    elif args.command == 'resume':
        result = validate_resume(load(args.input))
    elif args.command == 'compare':
        result = compare_sessions(load(args.input), load(args.reviews))
    else:
        manifest = load(args.input)
        state = load(args.state) if args.state and args.state.exists() else {}
        if args.command == 'plan':
            current, pending = validation_plan(args.root, manifest, state)
            result = {'pending': pending, 'documents': current}
        else:
            result = record_validation(args.root, manifest, state, load(args.reviews) if args.reviews else {})
            if not args.state:
                raise ValueError('--state is required for record')
            write_state(args.state, result)
    print(json.dumps(result, indent=2))
    if result.get('errors'):
        raise SystemExit(1)

if __name__ == '__main__':
    main()
