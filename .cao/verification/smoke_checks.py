"""Fixed boundary checks for the synthetic artifact smoke workflow."""

import json
import sys


EXPRESSIONS = {'age > 18', 'age >= 18'}


def check(expression):
    if expression not in EXPRESSIONS:
        raise ValueError('unsupported synthetic expression')
    failures = []
    for age, expected in ((17, False), (18, True), (19, True)):
        actual = age > 18 if expression == 'age > 18' else age >= 18
        if actual != expected:
            failures.append({'test': f'age_{age}', 'expected': expected, 'actual': actual})
    return {'checks': ['age_17', 'age_18', 'age_19'], 'failures': failures}


if __name__ == '__main__':
    try:
        result = check(sys.argv[1])
    except (ValueError, IndexError):
        print(json.dumps({'error': 'unsupported check input'}))
        raise SystemExit(2)
    print(json.dumps(result, sort_keys=True))
    raise SystemExit(1 if result['failures'] else 0)
