"""Bounded synthetic artifact handoffs, plan reuse, and workflow-owned checks."""

from __future__ import annotations

import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import uuid
from pathlib import Path

from cao_workflow import emit_output, get_inputs, step

INPUTS = {
    "repo_path": {"type": "path", "required": False, "default": "/Volumes/Craig/Code/workgraph"},
    "step_timeout_seconds": {"type": "int", "required": False, "default": 90},
    "plan_policy": {"type": "string", "required": False, "default": "reuse"},
}
PROFILES = {'architect': 'wg-architect-smoke', 'developer': 'wg-developer-smoke',
            'reviewer': 'wg-reviewer-smoke', 'gate': 'wg-gate-smoke'}
ARTIFACT_TOOLS = {
    'codex': ['exec', 'wait', 'exec_command', 'write_stdin', 'apply_patch'],
    'copilot_cli': ['fs_read', 'fs_list', 'fs_write'],
}
TASK = 'Implement is_adult(age) for integer ages: return True exactly when age is at least 18.'
PLAN_CONTRACT = {
    'objective': TASK, 'scope': ['synthetic is_adult'], 'non_goals': ['Workgraph changes'],
    'acceptance_criteria': ['AC-17:False', 'AC-18:True', 'AC-19:True'],
    'required_checks': ['age_17', 'age_18', 'age_19'],
    'invalidation_conditions': ['task or contract changes', 'explicit refresh', 'reviewer plan challenge'],
}
SCOPE = ('This is an isolated synthetic PROFILE SMOKE test. Use only supplied artifacts. '
         'Do not explore Workgraph or rig documents. Do not run tests, stage, commit, or delegate. '
         'Write only the requested output JSON file. Candidate and input files are read-only. '
         'Write a JSON object with every envelope field plus payload, using payload_fields; no Markdown. '
         'Terminal text is not consumed as a handoff. Exclude secrets, source excerpts and transcripts.\n')


class ArtifactError(ValueError):
    pass


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def read_json(path, unique_object):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd, 'rb') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > 20000:
                raise ArtifactError('artifact must be a bounded regular file without links')
            raw = stream.read(20001)
        if len(raw) > 20000:
            raise ArtifactError('artifact exceeds size limit')
        if not raw.strip():
            raise ArtifactError('artifact output file is empty')
        return json.loads(raw, object_pairs_hook=unique_object)
    except FileNotFoundError:
        raise ArtifactError('artifact output file is missing') from None
    except PermissionError:
        raise ArtifactError('artifact output file is unreadable due to permissions') from None
    except (OSError, ValueError, RecursionError) as exc:
        if isinstance(exc, ArtifactError):
            raise
        raise ArtifactError('missing or invalid JSON artifact') from None


def validate_plan(plan):
    if not isinstance(plan, dict) or set(plan) != set(PLAN_CONTRACT) | {'summary'}:
        raise ArtifactError('invalid plan fields')
    if any(plan[key] != value for key, value in PLAN_CONTRACT.items()):
        raise ArtifactError('plan does not match fixed smoke contract/checks')
    if not isinstance(plan['summary'], str) or not plan['summary'].strip() or len(plan['summary']) > 1000:
        raise ArtifactError('invalid bounded plan summary')
    return plan


def validate_candidate(payload, correction=False):
    if not isinstance(payload, dict) or set(payload) != {'expression', 'disposition', 'addressed_findings'}:
        raise ArtifactError('invalid developer handoff fields')
    if payload['expression'] not in ('age > 18', 'age >= 18'):
        raise ArtifactError('unsupported synthetic expression; no arbitrary code is executed')
    if payload['disposition'] != ('corrected' if correction else 'reproduced'):
        raise ArtifactError('unsupported developer disposition')
    ids = payload['addressed_findings']
    if not isinstance(ids, list) or len(ids) > 12 or any(not isinstance(item, str) or len(item) > 240 for item in ids):
        raise ArtifactError('invalid finding-to-change map')
    if len(ids) != len(set(ids)):
        raise ArtifactError('duplicate finding-to-change entries')
    return payload


def main():
    inputs = get_inputs()
    repo = Path(inputs.get('repo_path', '/Volumes/Craig/Code/workgraph')).resolve()
    timeout = int(inputs.get('step_timeout_seconds', 90))
    policy = inputs.get('plan_policy', 'reuse')
    if not 1 <= timeout <= 120 or policy not in ('reuse', 'refresh'):
        raise ValueError('invalid smoke timeout or plan policy')
    sys.dont_write_bytecode = True
    sys.path.insert(0, str(repo / '.cao/workflows'))
    try:
        import feature_engineering as rig
    finally:
        sys.path.pop(0)
    evidence = rig.RunEvidence(repo)
    try:
        configs = {seat: rig.load_profile_config(repo, name) for seat, name in PROFILES.items()}
        for seat, name in PROFILES.items():
            header = (repo / '.cao/profiles' / f'{name}.md').read_text().split('---', 2)[1]
            declarations = re.findall(r'^allowedTools: (.+)$', header, re.MULTILINE)
            tools = ARTIFACT_TOOLS.get(configs[seat]['provider'])
            if not tools or declarations != ['[' + ', '.join(tools) + ']']:
                raise ValueError('explicit artifact file permissions required')
            configs[seat]['allowed_tools'] = tools
        if any('astra' in config['model'].lower() for config in configs.values()):
            raise ValueError('Astra prohibited')
        if configs['reviewer']['model'] in {configs['developer']['model'], configs['gate']['model']}:
            raise ValueError('reviewer must use a different configured model')
    except (OSError, ValueError):
        evidence.fail('Smoke preflight requires explicit non-Astra profiles, artifact file permissions, and a distinct reviewer model')
    baseline = rig.repository_state(repo)
    checker = repo / '.cao/verification/smoke_checks.py'
    if not checker.is_file():
        evidence.fail('Fixed workflow-owned smoke check script is missing')
    contract_digest = digest({'schema_version': 1, 'contract': PLAN_CONTRACT,
                              'checker_sha256': hashlib.sha256(checker.read_bytes()).hexdigest()})
    git_root = Path(os.fsdecode(rig.git_bytes(repo, 'rev-parse', '--absolute-git-dir')).strip())
    cache_root = git_root / 'cao-plans/profile-smoke'
    cache_root.mkdir(parents=True, exist_ok=True, mode=0o700)
    cache = cache_root / 'plan.json'
    lock = cache_root / 'plan.lock'
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError:
        evidence.fail(f'Smoke plan is locked by another or interrupted run; reconcile {lock} before retry')
    work = repo / '.cao/smoke-work' / evidence.path.name
    owned = []
    pending = []
    completed = False
    brief_id = 'none'
    candidate_id = 'none'
    candidate_path = None
    plan = None
    try:
        if (repo / '.cao').is_symlink() or work.parent.is_symlink():
            evidence.fail('Smoke artifact directory must not be a symbolic link')
        work.mkdir(parents=True, mode=0o700)
        evidence.atomic_write(lock / 'owner.json', {'evidence_directory': str(evidence.path)})
        evidence.write('smoke-config', {'profiles': PROFILES, 'requested': configs,
                                       'actual_provider_model': None, 'timeout_seconds': timeout})

        def publish(name, document):
            path = work / name
            evidence.atomic_write(path, document)
            owned.append(path)
            return path

        def write_cache(document):
            temporary = cache_root / f'plan-{uuid.uuid4().hex}.json'
            evidence.atomic_write(temporary, document)
            os.replace(temporary, cache)

        def cache_snapshot():
            if not cache.exists() and not cache.is_symlink():
                return None
            try:
                return digest(read_json(cache, rig.unique_json_object))
            except ArtifactError:
                return 'invalid-cache'

        def manifest(excluded=None):
            result = {}
            try:
                for path in work.iterdir():
                    if path == excluded:
                        continue
                    info = path.lstat()
                    value = (hashlib.sha256(path.read_bytes()).hexdigest()
                             if stat.S_ISREG(info.st_mode) and info.st_size <= 20000 else 'nonregular-or-oversized')
                    result[path.name] = (info.st_mode, info.st_nlink, value)
            except OSError:
                evidence.fail('Cannot inspect smoke input artifacts')
            return result

        def call(seat, key, context):
            nonlocal candidate_path
            if rig.repository_state(repo) != baseline:
                evidence.fail(f'Repository changed before {key}')
            output_path = work / f'{key}-{uuid.uuid4().hex}.json'
            pending.append(output_path)
            envelope = {'schema_version': 1, 'run_token': evidence.path.name, 'step_id': key,
                        'brief_id': brief_id, 'candidate_id': candidate_id}
            request = {'output_path': str(output_path), 'envelope': envelope, **context}
            if candidate_path:
                request['candidate_path'] = str(candidate_path)
            before = manifest()
            cache_before = cache_snapshot()
            evidence.write('smoke-step-start', {'seat': seat, 'step_id': key, 'requested': configs[seat],
                                               'identity': envelope, 'repository': baseline})
            try:
                result = step(configs[seat]['provider'], PROFILES[seat], SCOPE + 'ARTIFACT_REQUEST: '
                              + json.dumps(request, sort_keys=True) + '\n', recovery='idempotent',
                              step_id=key, working_directory=str(repo), timeout=timeout)
            except Exception as exc:
                evidence.execution_failure = {'step_id': key, 'seat': seat, **rig.execution_diagnostics(exc),
                    'repository_before': baseline, 'repository_after': rig.repository_state(repo)}
                evidence.write('smoke-execution-error', evidence.execution_failure)
                if evidence.execution_failure.get('signal') == 'copilot_initialization_timeout':
                    evidence.fail(f'Copilot startup readiness timed out after 60 seconds at {key}; '
                                  'inspect CAO provider/status-monitor logs before another run')
                evidence.fail(f'Smoke agent {key} failed; reconcile side effects before another run')
            if rig.repository_state(repo) != baseline:
                evidence.fail(f'Smoke agent {key} modified repository state')
            if manifest(output_path) != before or cache_snapshot() != cache_before:
                evidence.fail(f'Smoke agent {key} modified input artifacts')
            if getattr(result, 'status', None) != 'completed' or getattr(result, 'replayed', False):
                evidence.fail(f'Smoke agent {key} did not produce a fresh completed step')
            try:
                document = read_json(output_path, rig.unique_json_object)
                if not isinstance(document, dict) or set(document) != set(envelope) | {'payload'}:
                    raise ArtifactError('invalid artifact envelope fields')
                if any(type(document[key]) is not type(value) or document[key] != value for key, value in envelope.items()):
                    raise ArtifactError('artifact identity does not match run/step/brief/candidate')
                return document['payload']
            except ArtifactError as exc:
                record = {'seat': seat, 'step_id': key,
                    'output_path': str(output_path), 'reason': str(exc), 'requested': configs[seat],
                    'repository': baseline}
                evidence.context['artifact_error'] = record
                evidence.write('smoke-artifact-error', record)
                raise
            finally:
                if output_path.is_file() or output_path.is_symlink():
                    output_path.unlink()

        plan_reused = False
        if policy == 'reuse':
            try:
                cached = read_json(cache, rig.unique_json_object)
                if set(cached) != {'schema_version', 'contract_digest', 'brief_id', 'state', 'plan'}:
                    raise ArtifactError('invalid cache fields')
                if type(cached['schema_version']) is not int or cached['schema_version'] != 1 or cached['contract_digest'] != contract_digest or cached['state'] != 'valid':
                    raise ArtifactError('invalidated plan')
                plan = validate_plan(cached['plan'])
                if cached['brief_id'] != digest(plan):
                    raise ArtifactError('plan digest mismatch')
                plan_reused = True
            except (ArtifactError, TypeError):
                plan = None
        if plan is None:
            write_cache({'state': 'invalidated'})
            try:
                plan = validate_plan(call('architect', 'smoke-architect', {
                    'action': 'Write an execution brief matching plan_contract exactly, plus a concise summary. No test commands.',
                    'plan_contract': PLAN_CONTRACT, 'payload_fields': [*PLAN_CONTRACT, 'summary']}))
            except ArtifactError as exc:
                evidence.fail(f'Invalid architect artifact: {exc}')
        brief_id = digest(plan)
        cached = {'schema_version': 1, 'contract_digest': contract_digest,
                  'brief_id': brief_id, 'state': 'valid', 'plan': plan}
        write_cache(cached)
        evidence.write('smoke-plan', {**cached, 'reused': plan_reused})
        brief_path = publish('brief.json', {'brief_id': brief_id, 'plan': plan})

        def materialize(payload, number):
            nonlocal candidate_id, candidate_path
            source = f"def is_adult(age):\n    return {payload['expression']}\n"
            candidate_id = digest({'brief_id': brief_id, 'repository': baseline, 'source': source})
            candidate_path = work / f'candidate-{number}.py'
            with candidate_path.open('x', encoding='utf-8') as stream:
                stream.write(source)
            candidate_path.chmod(0o600)
            owned.append(candidate_path)
            evidence.write('smoke-candidate', {'candidate_id': candidate_id, 'brief_id': brief_id,
                'repository': baseline, 'source_sha256': hashlib.sha256(source.encode()).hexdigest(), **payload})

        def verify(expression):
            if rig.repository_state(repo) != baseline:
                evidence.fail('Repository changed before deterministic smoke verification')
            try:
                result = subprocess.run([sys.executable, '-I', str(checker), expression], cwd=repo,
                    capture_output=True, text=True, timeout=10, check=False)
                data = json.loads(result.stdout, object_pairs_hook=rig.unique_json_object)
                if result.returncode not in (0, 1) or set(data) != {'checks', 'failures'} or data['checks'] != PLAN_CONTRACT['required_checks']:
                    raise ValueError('invalid check receipt')
                if bool(data['failures']) != bool(result.returncode):
                    raise ValueError('check result mismatch')
            except (OSError, subprocess.TimeoutExpired, ValueError, TypeError, RecursionError):
                evidence.fail('Deterministic smoke harness failed; no developer retry is safe')
            if rig.repository_state(repo) != baseline:
                evidence.fail('Repository changed during deterministic verification')
            receipt = {'candidate_id': candidate_id, 'brief_id': brief_id, 'repository': baseline,
                'passed': result.returncode == 0, 'exit_code': result.returncode, 'checks': data['checks'],
                'command': [sys.executable, '-I', str(checker), expression],
                'skipped': [], 'checker_sha256': hashlib.sha256(checker.read_bytes()).hexdigest(),
                'python_version': sys.version.split()[0]}
            evidence.verification = receipt
            evidence.write('smoke-check-log', {'candidate_id': candidate_id, **data})
            evidence.write('smoke-verification', receipt)
            if data['failures']:
                receipt = {**receipt, 'failures': data['failures']}
            return receipt

        def assess(seat, key, receipt, previous=None):
            marker = 'RELEASE_BLOCKED' if seat == 'gate' else 'FINDINGS'
            for attempt in range(2):
                step_id = key if attempt == 0 else key + '-format-1'
                context = {'action': 'Independently inspect current candidate and brief. Report defects or an explicit outcome. '
                    'Reassess previous findings yourself; never certify a developer claim. plan_valid=false challenges the brief.',
                    'brief_path': str(brief_path), 'verification': receipt,
                    'previous_findings': previous or [],
                    'developer_disposition': evidence.context.get('disposition'),
                    'payload_fields': {'outcome': f'{marker}: YES or {marker}: NO', 'plan_valid': 'boolean',
                        'findings': 'array; each finding has id, summary, evidence, requirement (nonempty strings <=240 characters); maximum 12'}}
                if attempt:
                    context['retry_reason'] = evidence.invalid_assessment['reason']
                if seat == 'gate':
                    context['review_receipt'] = evidence.assessment
                try:
                    payload = call(seat, step_id, context)
                    if not isinstance(payload, dict) or set(payload) != {'outcome', 'plan_valid', 'findings'}:
                        raise ArtifactError('invalid assessment fields')
                    if type(payload['plan_valid']) is not bool or not isinstance(payload['findings'], list):
                        raise ArtifactError('invalid assessment types')
                    text = '\n'.join('FINDING: ' + json.dumps(item) for item in payload['findings'])
                    text = (text + '\n' if text else '') + str(payload['outcome'])
                    parsed = rig.parse_assessment(text, marker)
                    if not payload['plan_valid'] and not parsed['blocked']:
                        raise ArtifactError('plan challenge requires a concrete finding')
                except (ArtifactError, rig.InvalidAssessment) as exc:
                    evidence.invalid_assessment = {'seat': seat, 'step_id': step_id, 'format_attempt': attempt,
                        'reason': str(exc), 'candidate_id': candidate_id}
                    evidence.write('smoke-invalid-assessment', evidence.invalid_assessment)
                    if attempt:
                        evidence.fail(f'Smoke assessment {step_id} invalid after one artifact retry')
                    continue
                evidence.invalid_assessment = None
                evidence.assessment = {'seat': seat, 'step_id': step_id, 'candidate_id': candidate_id,
                    'brief_id': brief_id, 'verification': evidence.verification, **parsed, 'plan_valid': payload['plan_valid']}
                evidence.write('smoke-assessment', evidence.assessment)
                if not payload['plan_valid']:
                    write_cache({**cached, 'state': 'invalidated'})
                    evidence.fail('Independent assessment challenged the plan; human clarification required')
                return parsed

        try:
            reproduction = validate_candidate(call('developer', 'smoke-developer', {
                'action': 'Reproduce the reported bug using expression age > 18. Do not fix it in this reproduction step.',
                'brief_path': str(brief_path), 'payload_fields': {'expression': 'age > 18', 'disposition': 'reproduced', 'addressed_findings': []}}))
        except ArtifactError as exc:
            evidence.fail(f'Invalid developer artifact: {exc}')
        if reproduction['expression'] != 'age > 18':
            evidence.fail('Smoke reproduction did not preserve the planted defect')
        materialize(reproduction, 0)
        receipt = verify(reproduction['expression'])
        if receipt['passed']:
            evidence.fail('Planted defect unexpectedly passed deterministic verification')
        findings = assess('reviewer', 'smoke-review-bug', receipt)
        if not findings['blocked'] or not any('candidate' in f['evidence'] and '18' in f['evidence'] for f in findings['findings']):
            evidence.fail('Smoke reviewer did not identify planted age-18 defect')
        evidence.context = {'previous_findings': findings['findings'], 'candidate_id': candidate_id}
        old_candidate = candidate_id
        evidence.review_corrections = 1
        try:
            correction = validate_candidate(call('developer', 'smoke-developer-fix', {
                'action': 'Correct the defect; propose a new expression and map every finding to this change. '
                    'Allowed expressions: age > 18 or age >= 18. Do not execute tests.',
                'brief_path': str(brief_path), 'verification': receipt, 'findings': findings['findings'],
                'payload_fields': {'expression': 'comparison', 'disposition': 'corrected', 'addressed_findings': 'array of finding IDs'}}), True)
        except ArtifactError as exc:
            evidence.fail(f'Invalid correction artifact: {exc}')
        if set(correction['addressed_findings']) != {f['id'] for f in findings['findings']}:
            evidence.fail('Correction does not account for every material finding')
        materialize(correction, 1)
        evidence.write('smoke-correction', {'before': old_candidate, 'after': candidate_id, **correction})
        evidence.context['disposition'] = correction
        if candidate_id == old_candidate:
            evidence.fail('No effective candidate change; findings remain unresolved')
        receipt = verify(correction['expression'])
        if not receipt['passed']:
            evidence.fail('Deterministic verification failed after the single correction')
        review = assess('reviewer', 'smoke-review-fixed', receipt, findings['findings'])
        if review['blocked']:
            evidence.fail('Unresolved reviewer findings after the single correction')
        gate = assess('gate', 'smoke-gate', receipt)
        if gate['blocked']:
            evidence.fail('Release gate blockers remain; smoke correction budget exhausted')
        summary = {'status': 'completed', 'workflow': 'profile_smoke', 'evidence_directory': str(evidence.path),
            'brief_id': brief_id, 'plan_path': str(cache), 'plan_reused': plan_reused,
            'candidate_id': candidate_id, 'corrections': 1, 'requested_assignments': configs,
            'scope': 'synthetic artifact interoperability; not product/release assurance'}
        evidence.write('smoke-completed', summary)
        completed = True
        emit_output(summary)
    finally:
        for path in pending:
            if path.is_file() or path.is_symlink():
                path.unlink()
        if completed:
            for path in owned:
                if path.is_file() or path.is_symlink():
                    path.unlink()
            work.rmdir()
        (lock / 'owner.json').unlink(missing_ok=True)
        lock.rmdir()


if __name__ == '__main__':
    main()
