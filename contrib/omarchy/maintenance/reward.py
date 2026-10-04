#!/usr/bin/env python3
"""Score the smoke test JSON from 0 to 100 (weights below).

Usage: reward.py [smoke.json]   (stdin if no file)
stdout: JSON; stderr: human summary. Exit 0 if stable, 1 if not, 3 on bad input.
PASS = full weight, WARN = half, FAIL = 0, SKIP = ignored. A group made only of
SKIPs counts as full; a group with no checks at all counts as 0.
"""
import json
import sys

WEIGHTS = {
    "TRIM": 25, "SMART": 20, "PACCACHE": 10, "SCRUB": 10, "ORPHAN": 5,
    "PKG": 10, "BOOT": 5, "SVC": 10, "HYPR": 5,
}
THRESHOLD = 90
BLOCKING = ("TRIM", "SMART")
VALUE = {"PASS": 1.0, "WARN": 0.5, "FAIL": 0.0}


def group_of(check_id):
    for g in sorted(WEIGHTS, key=len, reverse=True):
        if check_id.startswith(g):
            return g
    return None


def score(data):
    checks = data["checks"]
    per = {g: [] for g in WEIGHTS}
    for c in checks:
        g = group_of(str(c["id"]))
        if g is None:
            continue
        per[g].append(str(c["status"]).upper())
    groups, total, blockers = {}, 0.0, []
    for g, w in WEIGHTS.items():
        sts = per[g]
        scored = [s for s in sts if s != "SKIP"]
        if not sts:
            frac = 0.0
        elif not scored:
            frac = 1.0
        else:
            frac = sum(VALUE.get(s, 0.0) for s in scored) / len(scored)
        got = w * frac
        total += got
        groups[g] = {"weight": w, "points": round(got, 2), "statuses": sts}
        if g in BLOCKING and (not sts or any(VALUE.get(s, 0.0) == 0.0 and s != "SKIP" for s in sts)):
            blockers.append(g)
    total = round(total, 2)
    return {"score": total, "threshold": THRESHOLD, "blockers": blockers,
            "stable": total >= THRESHOLD and not blockers, "groups": groups}


def main(argv):
    try:
        raw = open(argv[1]).read() if len(argv) > 1 else sys.stdin.read()
        result = score(json.loads(raw))
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as e:
        print(json.dumps({"error": str(e), "stable": False, "score": 0}))
        print(f"reward: entrada invalida: {e}", file=sys.stderr)
        return 3
    print(json.dumps(result, indent=1))
    print(f"reward: {result['score']}/100 (umbral {THRESHOLD}); "
          f"bloqueantes: {', '.join(result['blockers']) or 'ninguno'}; "
          f"{'ESTABLE' if result['stable'] else 'NO estable'}", file=sys.stderr)
    return 0 if result["stable"] else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
