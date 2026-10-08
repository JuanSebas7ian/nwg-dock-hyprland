#!/usr/bin/env python3
"""Score the smoke test JSON from 0 to 100 (weights below).

Usage: reward.py [smoke.json]   (stdin if no file)
stdout: JSON; stderr: human summary. Exit 0 if stable, 1 if not, 3 on bad input.
PASS = full weight, WARN = half, FAIL = 0, SKIP = ignored. A group made only of
SKIPs drops out of the total and the score is rescaled to 100; a group with no
checks at all counts as 0. A blocking group (TRIM, SMART, DRIVERS) needs at least one
non-SKIP check, otherwise it is a blocker.
"""
import json
import sys

WEIGHTS = {
    "TRIM": 20, "SMART": 15, "PACCACHE": 7, "SCRUB": 7, "ORPHAN": 4,
    "PKG": 7, "BOOT": 4, "SVC": 8, "HYPR": 5, "DRIVERS": 15,
    "GUARD": 4, "MEMG": 4,  # the desktop guards (freeze-guard, memguard): not blocking
}
THRESHOLD = 90
BLOCKING = ("TRIM", "SMART", "DRIVERS")
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
    groups, total, active, blockers = {}, 0.0, 0, []
    for g, w in WEIGHTS.items():
        sts = per[g]
        scored = [s for s in sts if s != "SKIP"]
        dropped = bool(sts) and not scored
        got = 0.0
        if scored:
            got = w * sum(VALUE.get(s, 0.0) for s in scored) / len(scored)
        if not dropped:
            total += got
            active += w
        groups[g] = {"weight": w, "points": round(got, 2), "statuses": sts, "dropped": dropped}
        if g in BLOCKING and (not scored or any(VALUE.get(s, 0.0) == 0.0 for s in scored)):
            blockers.append(g)
    total = round(total * 100 / active, 2) if active else 0.0
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
