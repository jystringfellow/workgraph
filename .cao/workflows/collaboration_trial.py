"""Fresh-run trial of three CLI providers, external checks, and one correction."""

import ast
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

from cao_workflow import emit_output, get_inputs, step

INPUTS = {
    'repo_path': {'type': 'path', 'required': False, 'default': '/Volumes/Craig/Code/workgraph'},
    'gate_model': {'type': 'string', 'required': True},
    'step_timeout_seconds': {'type': 'int', 'required': False, 'default': 120},
}
PLAN = 'Implement is_adult(age): integer ages 17, 18, 19 must return False, True, True. Change only candidate.py.\n'


def fingerprint(path):
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK), 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > 20000:
            raise ValueError('missing, linked, or oversized artifact')
        content = stream.read(20001)
    if len(content) > 20000:
        raise ValueError('oversized artifact')
    return hashlib.sha256(content).hexdigest()


def expression(path):
    fingerprint(path)
    module = ast.parse(path.read_text())
    if len(module.body) != 1 or not isinstance(module.body[0], ast.FunctionDef):
        raise ValueError('candidate must contain only is_adult')
    function = module.body[0]
    if (function.name != 'is_adult' or function.decorator_list or len(function.body) != 1
            or ast.dump(function.args) != ast.dump(ast.parse('def is_adult(age): pass').body[0].args)
            or function.returns is not None or not isinstance(function.body[0], ast.Return)):
        raise ValueError('candidate must contain a single integer comparison')
    value = function.body[0].value
    for source in ('age > 18', 'age >= 18'):
        if ast.dump(value) == ast.dump(ast.parse(source, mode='eval').body):
            return source
    raise ValueError('candidate must contain a supported integer comparison')


def main():
    inputs = get_inputs()
    repo = Path(inputs['repo_path']).resolve()
    sys.dont_write_bytecode = True
    sys.path.insert(0, str(repo / '.cao/workflows'))
    try:
        import feature_engineering as rig
    finally:
        sys.path.pop(0)
    evidence = rig.RunEvidence(repo)
    if os.environ.get('CAO_WORKFLOW_RESUME') == '1':
        evidence.fail('Collaboration trial supports fresh runs only; reconcile the prior run before starting another')
    gate_model = inputs['gate_model']
    timeout = int(inputs.get('step_timeout_seconds', 120))
    developer = rig.load_profile_config(repo, 'wg-trial-developer-smoke')
    if (not re.fullmatch(r'[A-Za-z0-9_.-]{1,100}', gate_model)
            or 'astra' in gate_model.lower() or 'astra' in developer['model'].lower()
            or gate_model == developer['model'] or not 1 <= timeout <= 180):
        evidence.fail('Select a non-Astra second Codex model distinct from the developer and a timeout of 1–180 seconds')
    seats = {
        'developer': ('codex', 'wg-trial-developer-smoke', developer['model']),
        'reviewer': ('copilot_cli', 'wg-trial-reviewer-smoke', 'claude-haiku-5.5'),
        'local': ('claude_code', 'wg-local-smoke', None),
        'gate': ('codex', 'wg-trial-gate-smoke', gate_model),
    }
    baseline = rig.repository_state(repo)
    work = Path(tempfile.mkdtemp(prefix='cao-collaboration-'))
    plan = work / 'plan.md'
    candidate = work / 'candidate.py'
    plan.write_text(PLAN)
    candidate.write_text('def is_adult(age):\n    return age > 18\n')
    plan_hash = fingerprint(plan)
    evidence.context = {'work_directory': str(work), 'seats': {
        seat: {'provider': provider, 'profile': profile, 'requested_model': model,
               'actual_model': None, 'model_source': 'native_configuration' if model is None else 'explicit'}
        for seat, (provider, profile, model) in seats.items()}, 'corrections': 0}
    evidence.write('trial', evidence.context)

    def call(seat, key, prompt):
        provider, profile, model = seats[seat]
        before = fingerprint(candidate)
        if rig.repository_state(repo) != baseline:
            evidence.fail(f'Repository changed before {key}')
        options = {'model': model} if model is not None else {}
        try:
            result = step(provider, profile, prompt, recovery='manual', step_id=key,
                          working_directory=str(work), timeout=timeout, **options)
        except Exception as exc:
            evidence.execution_failure = {'seat': seat, 'step_id': key, **rig.execution_diagnostics(exc)}
            evidence.fail(f'Agent execution failed at {key}; reconcile side effects before another run')
        try:
            plan_after, candidate_after = fingerprint(plan), fingerprint(candidate)
        except (OSError, ValueError):
            evidence.fail(f'{key} left a missing, linked, unreadable, or oversized input artifact')
        if plan_after != plan_hash or rig.repository_state(repo) != baseline:
            evidence.fail(f'{key} modified the plan or repository')
        if seat != 'developer' and candidate_after != before:
            evidence.fail(f'{key} modified the reviewed candidate')
        if result.status != 'completed' or result.replayed:
            evidence.fail(f'{key} did not execute a fresh completed step')
        evidence.write('step', {'seat': seat, 'step_id': key, 'candidate': candidate_after})
        return before

    def checks():
        try:
            value = expression(candidate)
        except (ValueError, SyntaxError, OSError):
            receipt = {'passed': False, 'failures': ['candidate must be a single supported is_adult comparison']}
        else:
            command = [sys.executable, '-I', str(repo / '.cao/verification/smoke_checks.py'), value]
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            if result.returncode not in (0, 1):
                evidence.fail('Deterministic checker failed to execute')
            receipt = json.loads(result.stdout)
            receipt = {'passed': result.returncode == 0, 'failures': receipt['failures']}
        receipt['candidate'] = fingerprint(candidate)
        evidence.verification = receipt
        evidence.write('verification', receipt)
        return receipt

    def fix(feedback):
        if evidence.context['corrections'] == 1:
            evidence.fail('Trial correction budget exhausted; unresolved failures/findings retained')
        before = call('developer', 'trial-correction',
            f'Read {plan}. Fix {candidate} using this feedback: {json.dumps(feedback)}. '
            'Write only candidate.py; do not run tests or create a handoff JSON. The runner runs tests.')
        evidence.context['corrections'] += 1
        if fingerprint(candidate) == before:
            evidence.fail('No effective correction; unresolved feedback retained')
        if not checks()['passed']:
            evidence.fail('Tests still fail after the single correction')

    def review(seat, round_number):
        output = work / f'{seat}-{round_number}.txt'
        marker = 'RELEASE_BLOCKED' if seat == 'gate' else 'FINDINGS'
        prompt = (f'Read {plan} and independently inspect {candidate}. Tests passed for this candidate. '
            f'Write your assessment to {output}; change no other files and do not run tests. '
            'For each concrete defect write one line: FINDING: JSON with id, summary, evidence, requirement '
            '(each a nonempty string of at most 240 characters). '
            f'Finish with one standalone {marker}: YES if findings exist, otherwise {marker}: NO. '
            'No other text in the assessment file. Terminal replies are informational.')
        call(seat, f'trial-{seat}-{round_number}', prompt)
        try:
            fingerprint(output)
            assessment = rig.parse_assessment(output.read_text(), marker)
        except (OSError, ValueError):
            if output.is_file() or output.is_symlink():
                output.unlink()
            evidence.fail(f'{seat} produced no valid assessment file; no formatting retries in this trial')
        output.unlink()
        record = {'seat': seat, 'candidate': fingerprint(candidate), **assessment}
        evidence.write('assessment', record)
        if seat != 'local':
            evidence.assessment = record
        return record

    call('developer', 'trial-developer', f'Read {plan}. Implement it in {candidate}. '
         'Write only candidate.py; do not run tests or create a handoff JSON. The runner runs tests.')
    receipt = checks()
    if not receipt['passed']:
        evidence.context['feedback'] = receipt['failures']
        fix(receipt['failures'])
    assessment = review('reviewer', 1)
    local = review('local', 1)
    if assessment['blocked']:
        evidence.context['feedback'] = {'reviewer': assessment['findings'], 'local_advisory': local['findings']}
        fix(evidence.context['feedback'])
        assessment = review('reviewer', 2)
        review('local', 2)
        if assessment['blocked']:
            evidence.fail('Reviewer findings remain after the single correction')
    if review('gate', 1)['blocked']:
        evidence.fail('Final Codex assessment blocked the candidate; findings retained')
    evidence.write('completed', {'candidate': fingerprint(candidate), 'verification': evidence.verification})
    emit_output({'status': 'completed', 'evidence': str(evidence.path), 'work_directory': str(work)})


if __name__ == '__main__':
    main()
