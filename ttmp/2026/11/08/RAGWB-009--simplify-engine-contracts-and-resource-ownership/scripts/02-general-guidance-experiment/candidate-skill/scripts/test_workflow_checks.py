import copy
import importlib.util
import os
from pathlib import Path
import shutil
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('workflow', Path(__file__).with_name('workflow_checks.py'))
w = importlib.util.module_from_spec(spec)
spec.loader.exec_module(w)

class PolicyTests(unittest.TestCase):
    def test_upload_composition(self):
        self.assertEqual(w.upload_actions(), ['upload'])
        self.assertEqual(w.upload_actions(explicit_dry=True, explicit_listing=True), ['dry-run', 'upload', 'independent-listing'])
        self.assertEqual(w.upload_actions(ambiguous=True), ['upload', 'independent-listing'])
        self.assertEqual(w.upload_actions(auth_failed=True), ['upload', 'one-reauth-retry-or-block'])
        self.assertEqual(w.upload_actions(overwrite_risk=True), ['inspect-existing-state', 'upload'])
    def test_diary_modes(self):
        self.assertEqual(w.diary_mode(), 'milestone')
        for flag in ('detailed', 'substantive', 'failures'):
            self.assertEqual(w.diary_mode(**{flag: True}), 'investigation')
    def test_owned_skill_graph(self):
        root = Path(__file__).resolve().parents[2]
        self.assertEqual(w.skill_checks(root), [])
        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp)
            for name in w.OWNED:
                shutil.copytree(root/name, target/name, ignore=shutil.ignore_patterns('__pycache__'))
            core = target/'diary/SKILL.md'
            core.write_text(core.read_text()+'\n[broken](references/absent.md)\n')
            self.assertTrue(any('broken local reference' in e for e in w.skill_checks(target)))
            checklist = target/'ticket-research-docmgr-remarkable/references/deliverable-checklist.md'
            checklist.write_text(checklist.read_text()+'\n- remarquee status\n')
            self.assertTrue(any('duplicates specialist' in e for e in w.skill_checks(target)))
    def test_resume_shape_and_staleness(self):
        view = {'schema_version': 1, 'ticket': 'TEST', 'phase': 'resume', 'remaining': [], 'documents': [], 'conflicts': ['changed'], 'revisions': {}}
        self.assertEqual(w.validate_resume([{'resume': view}])['conflicts'], ['changed'])
        view['phase'] = 'invented'
        with self.assertRaises(ValueError):
            w.validate_resume(view)
    def test_comparison_guardrails(self):
        before = {'task_class': 'fixture', 'requirements': ['a'], 'covered_requirements': ['a'], 'retained_failure_context': True, 'unsupported_success_claims': 0, 'redundant_reloads': 3, 'independent_state_records': 4, 'unnecessary_mutation_diffs': 2}
        after = {**before, 'redundant_reloads': 1}
        self.assertFalse(w.compare_sessions(before, after)['regression_or_review_needed'])
        after['covered_requirements'] = []
        self.assertTrue(w.compare_sessions(before, after)['regression_or_review_needed'])
        after['task_class'] = 'unrelated'
        with self.assertRaises(ValueError):
            w.compare_sessions(before, after)

class DependencyTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root/'style.css').write_text('body {}')
        (self.root/'a.md').write_text('```mermaid\ngraph TD\nA-->B\n```\n```mermaid\ngraph TD\nB-->C\n```\n')
        (self.root/'b.md').write_text('Plain text')
        (self.root/'asset.svg').write_text('<svg/>')
        self.manifest = {'schema_version': 1, 'renderer': 'fixture-v1', 'policy': 'all-figures-v1', 'stylesheet': 'style.css', 'documents': [{'path': 'a.md', 'assets': ['asset.svg']}, {'path': 'b.md'}]}
        self.reviews = {'a.md': {'checks_passed': True, 'reviewed_figures': ['mermaid:1', 'mermaid:2']}, 'b.md': {'checks_passed': True, 'reviewed_figures': []}}
    def test_figures_ignore_fenced_examples(self):
        raw = (
            b'````markdown\n'
            b'```mermaid\n'
            b'not a rendered diagram\n'
            b'```\n'
            b'![not a rendered image](example.png)\n'
            b'````\n'
            b'```mermaid\n'
            b'graph TD\n'
            b'A-->B\n'
            b'```\n'
            b'![rendered image](figure.png)\n'
        )
        self.assertEqual(w.enumerate_figures(raw), ['mermaid:1', 'image:1'])

    def test_unchanged_and_affected_assets(self):
        state = w.record_validation(self.root, self.manifest, {}, self.reviews)
        self.assertEqual(w.validation_plan(self.root, self.manifest, state)[1], [])
        (self.root/'asset.svg').write_text('<svg>changed</svg>')
        self.assertEqual(w.validation_plan(self.root, self.manifest, state)[1], ['a.md'])
    def test_shared_policy_renderer_stylesheet(self):
        state = w.record_validation(self.root, self.manifest, {}, self.reviews)
        for field in ('renderer', 'policy'):
            changed = {**self.manifest, field: 'v2'}
            self.assertEqual(w.validation_plan(self.root, changed, state)[1], ['a.md', 'b.md'])
        (self.root/'style.css').write_text('changed')
        self.assertEqual(w.validation_plan(self.root, self.manifest, state)[1], ['a.md', 'b.md'])
    def test_every_figure_and_failed_checks(self):
        reviews = copy.deepcopy(self.reviews)
        reviews['a.md']['reviewed_figures'] = ['mermaid:1']
        with self.assertRaises(ValueError):
            w.record_validation(self.root, self.manifest, {}, reviews)
        reviews = copy.deepcopy(self.reviews)
        reviews['a.md']['checks_passed'] = False
        with self.assertRaises(ValueError):
            w.record_validation(self.root, self.manifest, {}, reviews)
    def test_cache_write_preserves_noop_mtime(self):
        path = self.root/'state.json'
        w.write_state(path, {'a': 'verified'})
        os.utime(path, (1000, 1000))
        w.write_state(path, {'a': 'verified'})
        self.assertEqual(path.stat().st_mtime, 1000)
        w.write_state(path, {'a': 'changed'})
        self.assertNotEqual(path.stat().st_mtime, 1000)

    def test_missing_dependency_and_escape(self):
        (self.root/'asset.svg').unlink()
        with self.assertRaises(ValueError):
            w.keys(self.root, self.manifest)
        with self.assertRaises(ValueError):
            w.local(self.root, '../escape')

if __name__ == '__main__':
    unittest.main()
