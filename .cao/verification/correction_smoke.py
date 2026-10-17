"""Prepare and verify a bounded reviewer-led correction trial."""

import ast
import hashlib
import importlib.util
import json
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile


SCRIPT = Path(__file__).resolve()
spec = importlib.util.spec_from_file_location('smoke_files', SCRIPT.with_name('supervisor_smoke.py'))
files = importlib.util.module_from_spec(spec)
spec.loader.exec_module(files)
PLAN = ('Implement is_adult(age). Accept only exact built-in int values. Return False '
        'below 18 and True from 18 onward. Every other input type must raise TypeError. '
        'Keep one small function: an input guard raising TypeError, then return age >= 18. '
        'Change only candidate.py. Do not import modules, run tests, commit, or delegate.\n')
SEED = ('def is_adult(age):\n    if not isinstance(age, int):\n'
        '        raise TypeError("age must be an integer")\n    return age >= 18\n')


def shape(content):
    tree = ast.parse(content)
    for node in ast.walk(tree):
        if isinstance(node, ast.FunctionDef):
            node.returns = None
        if isinstance(node, ast.arg):
            node.annotation = None
        if (isinstance(node, ast.Raise) and node.cause is None
                and isinstance(node.exc, ast.Call) and isinstance(node.exc.func, ast.Name)
                and node.exc.func.id == 'TypeError' and not node.exc.keywords
                and (not node.exc.args or (len(node.exc.args) == 1
                     and isinstance(node.exc.args[0], ast.Constant)
                     and isinstance(node.exc.args[0].value, str)))):
            node.exc.args = [ast.Constant(value='invalid age')]
    return ast.dump(tree)


def prepare(parent=None):
    work = Path(tempfile.mkdtemp(prefix='cao-correction-', dir=parent)).resolve()
    files.write(work / 'plan.md', PLAN)
    files.write(work / 'candidate.py', SEED)
    command = [sys.executable, '-I', str(SCRIPT)]
    basic = shlex.join(command + ['check', str(work)])
    regression = shlex.join(command + ['check', str(work), '--regression'])
    finish = shlex.join(command + ['finish', str(work)])
    files.write(work / 'start.md', f'''Run one bounded reviewer-led correction trial here only.
The supplied plan is valid: skip the architect. The candidate is already provided;
do not assign initial implementation or change it before the first review.
Use wg-codex-worker-smoke (Codex gpt-6-luna) for correction and
wg-copilot-peer-smoke (Copilot claude-haiku-5.5) for independent review.
These are requested assignments. Record actual model unverified when necessary;
do not require model attestation, switch models, or use built-in subagents.

1. Run {basic}
   This is deliberately limited baseline coverage, not a full correctness claim.
   Save its candidate hash and check results in a compact baseline.json here.
2. Assign the independent reviewer to inspect plan.md, candidate.py, and checks.json.
   Supply the checked candidate hash and explicitly confirm the current candidate matches it.
   The workflow owns hashing: no reviewer shell access or hash computation is needed.
   Ask for concrete defects with file/line and plan evidence, or approval of that hash.
   Do not disclose a specific defect to the first reviewer or supply a proposed fix.
   End your turn for its send_message callback; do not sleep or poll.
3. If the reviewer reports no defect, stop: the intended correction path was not exercised.
   If it reports a concrete defect, save a short curated finding and assign ONE developer correction.
   Give the developer the finding and plan. It may edit only candidate.py and must reply
   through send_message. It must not run tests, edit checks/plan, commit, or delegate.
   End your turn for the callback. A plan dispute or uncertainty requires human clarification.
4. Compare the candidate hash to baseline.json; stop on unchanged state or inability to fix.
   Run fresh workflow-owned regression checks: {regression}
   These add coverage beyond the baseline. If any check fails, stop: no further correction.
5. Assign a fresh independent reviewer to inspect the corrected candidate, plan, and fresh receipt.
   Supply the new candidate hash, prior finding, and a concise developer disposition.
   Ask it to independently verify closure and investigate regressions, not agree with the developer.
   Require explicit approval identifying this new hash via send_message. No shell hashing.
   End your turn. If new findings remain, stop; do not start another correction loop.
6. Only after explicit independent approval of the new hash, run {finish}
   Record Stage: ready for human review in handoff.md only if finish succeeds.

Stop on startup/tool failure or execution uncertainty; no retries or fallbacks.
At most three worker assignments and one correction total. Do not run tests in workers.
Use normal prose, with no marker or JSON output contract for agent messages.
Keep handoff.md concise: stage, baseline/final hashes, check scope/result, finding,
developer disposition, correction/assignment counts, reviewer terminal IDs and approval provenance.
Do not save transcripts, complete prompts, credentials, unrelated files, or source diffs.
Do not edit plan.md or checker scripts, create a PR, or land anything.
''')
    files.write(work / '.gitignore', 'checks.json\nbaseline.json\nhandoff.md\n.pending-*\n')
    git = ['git', '-c', 'user.name=CAO Smoke Fixture', '-c', 'user.email=cao-smoke@localhost',
           '-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false']
    subprocess.run(git + ['init', '-q'], cwd=work, check=True, capture_output=True)
    subprocess.run(git + ['add', 'plan.md', 'candidate.py', 'start.md', '.gitignore'],
                   cwd=work, check=True, capture_output=True)
    subprocess.run(git + ['commit', '-q', '-m', 'Initialize synthetic correction fixture'],
                   cwd=work, check=True, capture_output=True)
    return work


def assess(work, regression=False):
    if files.read(work / 'plan.md') != PLAN.encode():
        raise ValueError('supplied plan changed; human clarification required')
    content = files.read(work / 'candidate.py')
    variants = {'not isinstance(age, int)': 'instance', 'type(age) is not int': 'exact',
                'type(age) != int': 'exact', 'not type(age) is int': 'exact',
                'isinstance(age, bool) or not isinstance(age, int)': 'non_boolean',
                'not isinstance(age, int) or isinstance(age, bool)': 'non_boolean'}
    accepted = {shape(SEED.replace('not isinstance(age, int)', guard).encode()): kind
                for guard, kind in variants.items()}
    try:
        kind = accepted.get(shape(content))
    except SyntaxError:
        kind = None
    failures = []
    if kind is None:
        failures.append({'test': 'candidate_shape', 'reason':
                         'use one input guard raising TypeError, then return age >= 18'})
    else:
        cases = [('age_17', 17, False), ('age_18', 18, True), ('age_19', 19, True),
                 ('negative_age', -1, False), ('float', 18.0, 'TypeError'),
                 ('string', '18', 'TypeError'), ('none', None, 'TypeError')]
        if regression:
            integer_subclass = type('TrialInteger', (int,), {})
            cases += [('bool_true', True, 'TypeError'), ('bool_false', False, 'TypeError'),
                      ('integer_subclass', integer_subclass(18), 'TypeError')]
        for name, value, expected in cases:
            invalid = type(value) is not int if kind == 'exact' else not isinstance(value, int)
            if kind == 'non_boolean' and isinstance(value, bool):
                invalid = True
            actual = 'TypeError' if invalid else value >= 18
            if actual != expected:
                failures.append({'test': name, 'expected': expected, 'actual': actual})
    return {'candidate_sha256': hashlib.sha256(content).hexdigest(),
            'plan_sha256': hashlib.sha256(PLAN.encode()).hexdigest(),
            'checker_sha256': hashlib.sha256(SCRIPT.read_bytes()).hexdigest(),
            'scope': 'regression' if regression else 'baseline',
            'passed': not failures, 'failures': failures}


def main(args):
    if args == ['prepare']:
        print(prepare())
        return 0
    if (len(args) not in (2, 3) or args[0] not in ('check', 'finish')
            or (len(args) == 3 and (args[0] != 'check' or args[2] != '--regression'))):
        raise ValueError('usage: correction_smoke.py prepare | check DIRECTORY [--regression] | finish DIRECTORY')
    work = Path(args[1]).resolve()
    receipt = assess(work, regression=args[0] == 'finish' or len(args) == 3)
    if args[0] == 'finish':
        if json.loads(files.read(work / 'checks.json')) != receipt or not receipt['passed']:
            print('STOP: missing, stale, failed, or baseline-only checks; fresh regression checks required.')
            return 1
        print('Regression checks current; independent approval must identify ' + receipt['candidate_sha256'])
        return 0
    files.write(work / 'checks.json', json.dumps(receipt, indent=2, sort_keys=True) + '\n')
    if receipt['failures']:
        print(json.dumps({'failures': receipt['failures'], 'candidate_sha256': receipt['candidate_sha256']}))
        return 1
    print('PASS: ' + receipt['scope'] + '; candidate ' + receipt['candidate_sha256'])
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main(sys.argv[1:]))
    except (OSError, ValueError) as exc:
        print('STOP: ' + str(exc), file=sys.stderr)
        raise SystemExit(1)
