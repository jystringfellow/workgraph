"""Prepare and supervise a bounded CAO pod with external verification."""

import argparse
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
import time
import uuid

from urllib.parse import urlencode
from urllib.request import Request, urlopen


REPO = Path(__file__).resolve().parents[1]
HOME = Path(os.environ.get('CAO_HOME_DIR', Path.home() / '.aws/cli-agent-orchestrator')).resolve()
API_HOST = os.environ.get('CAO_API_HOST', '127.0.0.1')
API_PORT = os.environ.get('CAO_API_PORT', '9889')
BASE = f'http://{API_HOST}:{API_PORT}'
RIG = Path(__file__).resolve().parent
COPILOT_AGENTS = Path.home() / '.copilot/agents'


def state():
    def git(*args):
        return subprocess.check_output(['git', '-C', str(REPO), *args], stderr=subprocess.PIPE)
    head = git('rev-parse', 'HEAD').decode().strip()
    parts = [git('ls-files', '--stage', '-z'),
             git('diff', '--binary', '--no-ext-diff', '--no-textconv')]
    def append_path(path):
        raw = os.fsencode(path.relative_to(REPO))
        parts.extend([raw, str(path.lstat().st_mode).encode()])
        if path.is_symlink():
            parts.append(os.fsencode(os.readlink(path)))
        elif path.is_dir():
            children = subprocess.check_output(['git', '-C', str(path), 'ls-files',
                                                '--cached', '--others', '--exclude-standard', '-z'])
            for child in sorted(set(filter(None, children.split(b'\0')))):
                nested = path / os.fsdecode(child)
                if nested.exists() or nested.is_symlink():
                    append_path(nested)
        else:
            parts.append(path.read_bytes())

    for raw in sorted(filter(None, git('ls-files', '--others', '--exclude-standard', '-z').split(b'\0'))):
        append_path(REPO / os.fsdecode(raw))
    digest = hashlib.sha256()
    for part in parts:
        digest.update(len(part).to_bytes(8, 'big'))
        digest.update(part)
    return {'head': head, 'fingerprint': digest.hexdigest()}


def save(path, value):
    pending = path.with_suffix(path.suffix + '.pending')
    pending.write_text(json.dumps(value, indent=2) + '\n')
    pending.replace(path)


def clean_env():
    env = os.environ.copy()
    for key in ('WORKGRAPH_AI_SESSION_ID', 'WORKGRAPH_AI_HOME', 'WORKGRAPH_AI_DATABASE', 'PYTHONPATH'):
        env.pop(key, None)
    env.update(CAO_HOME_DIR=str(HOME), CAO_API_HOST=API_HOST, CAO_API_PORT=API_PORT)
    return env


def prepare(brief_path, plan_paths):
    import yaml

    brief = brief_path.read_text()
    if not brief.strip() or len(brief.encode()) > 20000:
        raise ValueError('Execution brief must contain 1–20000 bytes')
    paths = {str((REPO / p).resolve().relative_to(REPO)) for p in ['AGENTS.md', *plan_paths]}
    plan = {p: hashlib.sha256((REPO / p).read_bytes()).hexdigest() for p in sorted(paths)}
    run_id = uuid.uuid4().hex[:10]
    root = REPO / '.cao/pod-runs' / ('pod-' + run_id)
    root.mkdir(parents=True, mode=0o700)
    verifier = (RIG / 'verification/verify.sh').read_bytes()
    (root / 'verify.sh').write_bytes(verifier)
    seats = {}
    for seat in ('lead', 'developer', 'reviewer', 'gate'):
        text = (RIG / 'profiles' / ('wg-pod-' + seat + '.md')).read_text()
        _, header, body = text.split('---', 2)
        cfg = yaml.safe_load(header)
        cfg['name'] += '-' + run_id
        if cfg['provider'] == 'codex':
            cfg['mcpServers']['cao-mcp-server']['env'] = {
                'CAO_HOME_DIR': str(HOME), 'CAO_API_HOST': API_HOST, 'CAO_API_PORT': API_PORT}
        (root / (cfg['name'] + '.md')).write_text('---\n' + yaml.safe_dump(cfg, sort_keys=False) + '---' + body)
        seats[seat] = {k: cfg[k] for k in ('name', 'provider', 'model')}
    save(root / 'plan-state.json', {'sources': plan, 'status': 'existing plan reused'})
    save(root / 'seats.json', seats)
    save(root / 'manifest.json', {'run_id': run_id, 'repo': str(REPO),
                                'verifier_sha256': hashlib.sha256(verifier).hexdigest(),
                                'review_corrections': 3, 'gate_corrections': 2,
                                'max_worker_assignments': 14, 'max_check_requests': 12})
    (root / 'packet.md').write_text(f'''# Execution packet

Repository: {REPO}
Run directory: {root}

Read .cao/pods/feature-engineering.md and follow its supervisor-driven execution
protocol. The human-authored brief below defines the task; preserve pre-existing
edits. The plan sources are bound in plan-state.json; reuse them without an
architect call. Missing requirements or changed plans require human clarification.

Use ONLY the exact run-specific profiles from seats.json. Assign seats lazily
and sequentially through CAO; do not use built-in subagents or switch models.
Requested configuration is not runtime model attestation. Record uncertainty.

The host owns all tests. checks.json is its current receipt. Start with independent
review if implementation already exists and full checks passed. For a new
implementation, assign the developer fact-only work first. No agent runs tests.

Request checks by writing verify.request in THIS directory, then END YOUR TURN.
Use increasing request numbers, for example `1 red TestExactFactName` then `2 full`.
The host wakes you with a receipt. Confirm an intended assertion failed before
allowing implementation; a setup/build failure is not a valid regression fact.
Do not edit receipts, profiles, plan-state.json, seats.json, or host configuration.

At most three review corrections plus their final review, two gate corrections,
14 worker assignments, and 12 host check requests. Gate fixes always return through
full verification and fresh independent review before gate reassessment. These
budgets never reset. No retries or fallback on startup or uncertain execution.

Record concise findings, dispositions, budgets, terminal provenance and candidate
identity in handoff.md. On unsupported no-op corrections, exhausted budgets or
ambiguity, write Stage: stopped with final unresolved findings. Ordinary prose
verdicts are required, not JSON or special reviewer markers. Explicit approvals
must identify the supplied HEAD and fingerprint. Do not ask reviewers to hash.

Write Stage: ready for human review ONLY with current full checks, independent
review and explicit gate approval of the SAME candidate. The operator inspects
actual verdicts; provenance checks alone do not establish their meaning.
No commits, push, PR, release, automatic evidence deletion, or Jev authority.
No transcripts, secrets, environment dumps, full source contents or diffs in
findings. The task brief is human-authored input, not a captured provider prompt.

## Human task brief

{brief}
''')
    print(root)
    return root


def api(method, path, params=None):
    url = BASE + path + (('?' + urlencode(params)) if params else '')
    with urlopen(Request(url, method=method), timeout=120) as response:
        return json.load(response)


def check_plan(plan):
    for path, expected in plan.items():
        resolved = (REPO / path).resolve()
        resolved.relative_to(REPO)
        if hashlib.sha256(resolved.read_bytes()).hexdigest() != expected:
            raise RuntimeError('Plan sources changed; human clarification required')


def parse_request(text, completed, maximum):
    match = re.fullmatch(r'([1-9]\d*) (full|red)(?: (Test[A-Za-z0-9_]+))?', text)
    if not match:
        raise ValueError('Invalid host check request; no developer cycle consumed')
    number, mode, fact = int(match[1]), match[2], match[3]
    if number != completed + 1 or number > maximum or (mode == 'red') != bool(fact):
        raise ValueError('Check request sequence/budget/selection invalid')
    return number, mode, fact


def review_evidence(receipt, current, callbacks, actors, seats):
    if not receipt['passed'] or receipt['mode'] != 'full' or current != receipt['candidate']:
        raise RuntimeError('Ready claim has stale or non-full verification evidence')
    required = {}
    for seat in ('reviewer', 'gate'):
        latest = next((callback for callback in sorted(callbacks, reverse=True)
                       if actors.get(callback[1]) == seats[seat]['name']), None)
        if latest is None:
            raise RuntimeError('Ready claim lacks candidate-bound ' + seat + ' callback')
        rowid, sender, status, body = latest
        if (rowid <= receipt['inbox_rowid'] or status != 'delivered'
                or current['fingerprint'] not in body or current['head'] not in body):
            raise RuntimeError('Ready claim lacks fresh candidate-bound ' + seat + ' callback')
        required[seat] = sender
    return required


def verify(root, number, mode='full', fact=None):
    before = state()
    paths = subprocess.check_output(['git', '-C', str(REPO), 'diff', '--name-only', '-z', 'HEAD'])
    paths += subprocess.check_output(['git', '-C', str(REPO), 'ls-files', '--others', '--exclude-standard', '-z'])
    changed_paths = sorted(set(os.fsdecode(p) for p in paths.split(b'\0') if p))
    env = clean_env()
    verifier = (root / 'verify.sh').read_bytes()
    expected = json.loads((root / 'manifest.json').read_text())['verifier_sha256']
    if hashlib.sha256(verifier).hexdigest() != expected:
        raise RuntimeError('Verifier snapshot changed; prepare a fresh run')
    command = ['bash', '-c', verifier.decode()] if mode == 'full' else [
        'go', 'test', './facts', '-count=1', '-run', '^' + fact + '$']
    print(f'Host verification {number}: {mode}' + ((' ' + fact) if fact else ''), flush=True)
    child = subprocess.Popen(command, cwd=REPO, env=env, stdout=subprocess.PIPE,
                             stderr=subprocess.STDOUT, text=True, start_new_session=True, umask=0o022)
    started = time.monotonic()
    timed_out = False
    while True:
        try:
            output, _ = child.communicate(timeout=25)
            break
        except subprocess.TimeoutExpired:
            print(f'Host verification {number}: still running', flush=True)
            if time.monotonic() - started > 900:
                import signal
                timed_out = True
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    output, _ = child.communicate(timeout=15)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    output, _ = child.communicate(timeout=15)
                break
    after = state()
    failed_tests = sorted(set(re.findall(r'--- FAIL: ([A-Za-z0-9_/]+)', output)))
    locations = sorted(set(re.findall(r'\b([A-Za-z0-9_/-]+\.go:\d+):', output)))[:30]
    receipt = {'request': number, 'mode': mode, 'fact': fact, 'candidate': before,
               'passed': child.returncode == 0 and before == after and not timed_out,
               'exit_code': child.returncode, 'changed_during_checks': before != after,
               'timed_out': timed_out, 'changed_paths': changed_paths,
               'failed_tests': failed_tests, 'failure_locations': locations,
               'expected_fact_failed': (mode == 'red' and fact in failed_tests
                                        and child.returncode != 0 and before == after and not timed_out),
               'go_vet': ('pass' if 'go_vet: pass' in output else 'fail') if mode == 'full' else None,
               'go_test': ('pass' if 'go_test: pass' in output else 'fail') if mode == 'full'
                          else ('pass' if child.returncode == 0 else 'fail')}
    save(root / f'{number:02d}-checks.json', receipt)
    save(root / 'checks.json', receipt)
    print(json.dumps(receipt), flush=True)
    return receipt


def run(root):
    root = root.resolve()
    root.relative_to(REPO / '.cao/pod-runs')
    with (root / 'started.json').open('x') as claim:
        claim.write(json.dumps({'pid': os.getpid()}))
    env = clean_env()
    result = {'status': 'running', 'run_directory': str(root), 'session': None}
    session = lead_id = None
    actors = {}
    registrations = []
    last_full = red_receipt = None
    command = [sys.executable, '-m', 'cli_agent_orchestrator.cli.main']

    def host_verify(number, mode='full', fact=None):
        nonlocal last_full, red_receipt
        current = state() if number and mode == 'full' else None
        if current is not None and current != last_full['candidate']:
            if (not red_receipt or not red_receipt['expected_fact_failed']
                    or red_receipt['candidate'] == last_full['candidate']
                    or red_receipt['candidate']['head'] != current['head']):
                raise RuntimeError('Changed candidate requires a passing red receipt before full checks')
        receipt = verify(root, number, mode, fact)
        if mode == 'full' and receipt['passed']:
            with closing(sqlite3.connect('file:' + str(HOME / 'db/cli-agent-orchestrator.db') + '?mode=ro', uri=True)) as db:
                receipt['inbox_rowid'] = db.execute('select coalesce(max(rowid), 0) from inbox').fetchone()[0]
            save(root / f'{number:02d}-checks.json', receipt)
            save(root / 'checks.json', receipt)
        if mode == 'full':
            last_full, red_receipt = receipt, None
        else:
            red_receipt = receipt
        return receipt

    try:
        manifest = json.loads((root / 'manifest.json').read_text())
        seats = json.loads((root / 'seats.json').read_text())
        plan = json.loads((root / 'plan-state.json').read_text())['sources']
        if api('GET', '/health').get('status') != 'ok':
            raise RuntimeError('CAO server unhealthy')
        if manifest['repo'] != str(REPO):
            raise RuntimeError('Run belongs to a different repository')
        check_plan(plan)
        (HOME / 'agent-store').mkdir(parents=True, exist_ok=True)
        for seat, cfg in seats.items():
            target = HOME / 'agent-store' / (cfg['name'] + '.md')
            with target.open('x') as f:
                registrations.append(target)
                f.write((root / target.name).read_text())
            checked = subprocess.run(command + ['profile', 'validate', str(target)], env=env,
                                     capture_output=True, text=True, timeout=30)
            if checked.returncode:
                raise RuntimeError('Invalid profile for seat ' + seat)
            if cfg['provider'] == 'copilot_cli':
                native = COPILOT_AGENTS / (cfg['name'] + '.agent.md')
                if native.exists() or native.is_symlink():
                    raise RuntimeError('Copilot agent already exists: ' + cfg['name'])
                registrations.append(native)
                installed = subprocess.run(command + ['install', str(target), '--provider', 'copilot_cli'],
                                           env=env, capture_output=True, text=True, timeout=60)
                if installed.returncode or f"Agent '{cfg['name']}' installed successfully" not in installed.stdout:
                    raise RuntimeError('Copilot profile registration not confirmed')
        baseline = host_verify(0)
        if not baseline['passed']:
            raise RuntimeError('Full baseline verification failed; no paid seats launched')
        launched = subprocess.run(command + ['launch', '--headless', '--async', '--auto-approve',
                                 '--agents', seats['lead']['name'], '--provider', 'codex',
                                 '--working-directory', str(REPO),
                                 f'Read {root / "packet.md"} and supervise this bounded engineering task.'],
                                  env=env, capture_output=True, text=True, timeout=180)
        match = re.search(r'Session created:\s+([A-Za-z0-9_-]+)', launched.stdout)
        if match:
            session = match.group(1)
            result['session'] = session
        if launched.returncode or not session:
            raise RuntimeError('Lead startup/delivery failed; reconcile terminal before another attempt')
        print('Pod session: ' + session, flush=True)
        deadline = time.monotonic() + 2400
        previous = None
        notice = 0.0
        seen_requests = set()
        ready_since = None
        receipt = baseline
        while time.monotonic() < deadline:
            handoff_path = root / 'handoff.md'
            handoff = handoff_path.read_text() if handoff_path.exists() else ''
            stage = next((s.strip().replace('**', '') for s in handoff.splitlines()
                          if re.match(r'(?i)^stage:', s.strip().replace('**', ''))), 'Awaiting lead handoff')
            if stage != previous or time.monotonic() - notice > 30:
                print(stage, flush=True)
                notice, previous = time.monotonic(), stage
            with sqlite3.connect('file:' + str(HOME / 'db/cli-agent-orchestrator.db') + '?mode=ro', uri=True) as db:
                rows = db.execute('select id,agent_profile,provider,model,model_honored from terminals where tmux_session=?', (session,)).fetchall()
                actors.update((identifier, profile) for identifier, profile, provider, model, honored in rows)
                runtime_path = root / 'runtime-seats.json'
                runtime = json.loads(runtime_path.read_text()) if runtime_path.exists() else {}
                for identifier, profile, provider, model, honored in rows:
                    runtime[identifier] = {'profile': profile, 'provider': provider,
                                           'configured_model': model, 'model_honored_metadata': honored,
                                           'actual_model': 'unverified'}
                save(runtime_path, runtime)
                lead_id = next((key for key, profile in actors.items() if profile == seats['lead']['name']), lead_id)
                callbacks = db.execute('select rowid,sender_id,status,message from inbox where receiver_id=? ORDER BY rowid', (lead_id,)).fetchall() if lead_id else []
            save(root / 'actors.json', actors)
            if len(actors) > manifest['max_worker_assignments'] + 1:
                raise RuntimeError('Overall pod assignment bound exceeded')
            if subprocess.check_output(['git', '-C', str(REPO), 'rev-parse', 'HEAD'], text=True).strip() != baseline['candidate']['head']:
                raise RuntimeError('Git HEAD changed during a no-commit run; human reconciliation required')
            if re.search(r'(?i)^stage:\s*(?:stopped|blocked|failed)\b', stage):
                raise RuntimeError('Lead stopped; unresolved findings are in handoff.md')
            check_plan(plan)
            request = root / 'verify.request'
            text = request.read_text().strip() if request.exists() else ''
            if text and text not in seen_requests:
                number, mode, fact = parse_request(text, len(seen_requests), manifest['max_check_requests'])
                seen_requests.add(text)
                ready_deadline = min(deadline, time.monotonic() + 90)
                while time.monotonic() < ready_deadline:
                    with closing(sqlite3.connect('file:' + str(HOME / 'db/cli-agent-orchestrator.db') + '?mode=ro', uri=True)) as db:
                        terminals = db.execute('select id from terminals where tmux_session=?', (session,)).fetchall()
                    if lead_id and terminals and all(
                            api('GET', '/terminals/' + identifier).get('status') in ('idle', 'completed')
                            for identifier, in terminals):
                        break
                    time.sleep(2)
                else:
                    raise RuntimeError('Pod terminals did not yield for host verification; no wakeup retry')
                receipt = host_verify(number, mode, fact)
                cue = (f'Host verification request {number} completed. Read {root / "checks.json"}. '
                       f'Mode={mode}; passed={receipt["passed"]}; expected_fact_failed={receipt["expected_fact_failed"]}. '
                       f'Candidate fingerprint={receipt["candidate"]["fingerprint"]}. Continue packet within remaining budgets.')
                delivery = api('POST', f'/terminals/{lead_id}/input', params={'message': cue})
                if delivery.get('success') is not True:
                    raise RuntimeError('CAO did not confirm host receipt delivery; inspect the retained session')
                ready_since = None
            if re.search(r'(?i)^stage:\s*ready for human review\b', stage):
                current = state()
                required = review_evidence(receipt, current, callbacks, actors, seats)
                if api('GET', '/terminals/' + lead_id).get('status') not in ('idle', 'completed'):
                    ready_since = None
                else:
                    ready_since = ready_since or time.monotonic()
                if ready_since and time.monotonic() - ready_since > 8:
                    for seat in ('reviewer', 'gate'):
                        print(seat + ' verdict: ' + next(body for rowid,sender,status,body in reversed(callbacks)
                              if sender == required[seat] and current['fingerprint'] in body)[:2200], flush=True)
                    result.update(status='ready_for_human_review', candidate=current,
                                  verification_receipt=str(root / 'checks.json'), verdict_terminals=required,
                                  verdicts_require_human_inspection=True)
                    break
            time.sleep(2)
        else:
            raise TimeoutError('Overall pod time bound exhausted')
    except Exception as exc:
        result.update(status='stopped', failure=str(exc))
        print('STOP: ' + str(exc), flush=True)
        if session and ('bound exceeded' in str(exc) or isinstance(exc, TimeoutError)):
            try:
                result['bound_cleanup'] = api('DELETE', '/sessions/' + session)
            except Exception as cleanup_error:
                result['bound_cleanup_error'] = type(cleanup_error).__name__
    finally:
        for path in reversed(registrations):
            try:
                path.unlink(missing_ok=True)
            except OSError as exc:
                result.setdefault('registration_cleanup_errors', []).append(
                    {'path': str(path), 'error': type(exc).__name__})
                result['status'] = 'stopped'
        save(root / 'result.json', result)
        print('Run evidence: ' + str(root), flush=True)
        if session:
            print('Session retained: ' + session, flush=True)
    return 0 if result['status'] == 'ready_for_human_review' else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    preparation = commands.add_parser('prepare', help='Prepare artifacts without launching agents')
    preparation.add_argument('--brief', type=Path, required=True)
    preparation.add_argument('--plan', action='append', required=True,
                             help='Repository-relative plan/spec path; repeat for multiple sources')
    execution = commands.add_parser('run', help='Launch the pod; uses paid model quota')
    execution.add_argument('directory', type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    if args.command == 'prepare':
        prepare(args.brief, args.plan)
        return 0
    return run(args.directory)


if __name__ == '__main__':
    raise SystemExit(main())
