"""Prepare synthetic fixtures and check them outside CAO agent contexts."""

import ast
import hashlib
import json
import os
from pathlib import Path
import shlex
import stat
import sys
import tempfile


PLAN = ('Implement is_adult(age). For integer ages 17, 18, and 19, return False, True, '
        'and True respectively. Change only candidate.py. Use one function with one '
        'return statement comparing age to 18. Do not run tests, commit, or delegate.\n')
SCRIPT = Path(__file__).resolve()


def read(path):
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK), 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > 20000:
            raise ValueError('artifact must be a small, unlinked regular file')
        content = stream.read(20001)
    if len(content) > 20000:
        raise ValueError('artifact exceeds size limit')
    return content


def write(path, content):
    descriptor, pending = tempfile.mkstemp(prefix='.pending-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w') as stream:
            stream.write(content)
        os.replace(pending, path)
    finally:
        if os.path.exists(pending):
            os.unlink(pending)


def prepare(parent=None):
    work = Path(tempfile.mkdtemp(prefix='cao-supervisor-', dir=parent)).resolve()
    write(work / 'plan.md', PLAN)
    write(work / 'candidate.py', 'def is_adult(age):\n    return age > 18\n')
    check = shlex.join([sys.executable, '-I', str(SCRIPT), 'check', str(work)])
    finish = shlex.join([sys.executable, '-I', str(SCRIPT), 'finish', str(work)])
    write(work / 'start.md', f'''Run this tiny cross-model collaboration in this directory only.
The supplied plan.md is valid for this trial: skip the architect.
Use wg-codex-worker-smoke (Codex gpt-6-luna) and
wg-copilot-peer-smoke (Copilot claude-haiku-5.5). Do not substitute models.
These are requested assignments, not runtime attestations. Record "actual model unverified"
when runtime identity is unavailable. Do not block review on missing model attestation.
Do not ask workers to prove their model or use find_profiles or native cao shell
commands for identity verification. Such lookups are not required trial tools.

1. Read plan.md. Call CAO assign for the developer to read it and edit candidate.py.
   Ask it to report changes or clarification needs through send_message.
   End your turn to receive its reply; never poll or sleep waiting for messages.
2. After the reply, run the external checker with your native shell tool:
   {check}
   Read checks.json. Send only failures to the developer for triage if necessary.
3. If checks pass, assign the independent reviewer to inspect plan.md, candidate.py,
   and checks.json. Supply the candidate SHA-256 from the fresh workflow-owned receipt
   and explicitly confirm that it identifies the current candidate. Tell the reviewer
   to accept this hash evidence without computing a hash or requesting shell access.
   Missing or conflicting hash evidence remains a blocker. Ask for concrete
   defects with file/line evidence or an explicit approval of that exact candidate.
   The reviewer replies through send_message. End your turn for its reply.
4. Allow at most ONE developer correction total across failed tests and findings.
   Compare candidate hashes before/after correction. Stop on no effective change,
   unclear requirements, unavailable required tools, startup failure, or exhausted budget. Do not retry
   an execution with uncertain side effects or silently dismiss reviewer findings.
   Every correction needs fresh external checks and independent review of the current
   candidate, including closure of previous findings and possible new defects.
5. Only after checks pass and the reviewer explicitly approves the matching hash, run:
   {finish}
   Report ready for human review, with the receipt, seat identities, and this directory.

Use ordinary prose in messages: no JSON handoff or marker requirements.
Keep only a short curated handoff.md here: stage, candidate hash, check status,
correction count, unresolved findings/evidence, and approval provenance. Do not save
raw terminal output, full prompts, credentials, or unrelated repository material.
Do not edit plan.md or checker scripts, run tests inside the workers, invoke a Python
workflow, spawn built-in subagents, push, or create a PR. If a worker raises a plan
question, stop for human clarification. Stop after at most four worker assignments.
''')
    return work


def assess(work):
    if read(work / 'plan.md') != PLAN.encode():
        raise ValueError('supplied plan changed; human clarification required')
    content = read(work / 'candidate.py')
    failures = []
    try:
        tree = ast.dump(ast.parse(content))
        accepted = {ast.dump(ast.parse('def is_adult(age):\n    return ' + expression + '\n')): expression
                    for expression in ('age > 18', 'age >= 18')}
        expression = accepted.get(tree)
    except SyntaxError:
        expression = None
    if expression is None:
        failures.append({'test': 'candidate_shape', 'reason': 'use only the specified function and comparison'})
    else:
        for age, expected in ((17, False), (18, True), (19, True)):
            actual = age > 18 if expression == 'age > 18' else age >= 18
            if actual != expected:
                failures.append({'test': f'age_{age}', 'expected': expected, 'actual': actual})
    return {'candidate_sha256': hashlib.sha256(content).hexdigest(),
            'plan_sha256': hashlib.sha256(PLAN.encode()).hexdigest(),
            'checker_sha256': hashlib.sha256(SCRIPT.read_bytes()).hexdigest(),
            'passed': not failures, 'failures': failures}


def main(args):
    if args == ['prepare']:
        work = prepare()
        print('Fixtures: ' + str(work))
        print(shlex.join(['cao', 'launch', '--headless', '--async', '--agents', 'wg-coordinator-smoke',
                          '--provider', 'codex', '--working-directory', str(work),
                          'Read start.md and carry out the bounded collaboration trial.']))
        return 0
    if len(args) != 2 or args[0] not in ('check', 'finish'):
        raise ValueError('usage: supervisor_smoke.py prepare | check DIRECTORY | finish DIRECTORY')
    work = Path(args[1]).resolve()
    receipt = assess(work)
    if args[0] == 'finish':
        saved = json.loads(read(work / 'checks.json'))
        if saved != receipt or not receipt['passed']:
            print('STOP: missing, failed, or stale verification; run fresh checks.')
            return 1
        print('Checks current; eligible for human review ONLY with independent approval of '
              + receipt['candidate_sha256'] + '. Reviewer approval is not attested by this script.')
        return 0
    write(work / 'checks.json', json.dumps(receipt, indent=2, sort_keys=True) + '\n')
    if receipt['failures']:
        print(json.dumps({'failures': receipt['failures'], 'candidate_sha256': receipt['candidate_sha256']}))
        return 1
    print('PASS: candidate ' + receipt['candidate_sha256'] + '; receipt: ' + str(work / 'checks.json'))
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main(sys.argv[1:]))
    except (OSError, ValueError) as exc:
        print('STOP: ' + str(exc), file=sys.stderr)
        raise SystemExit(1)
