import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
import reward  # noqa: E402

GROUPS = ["TRIM", "SMART", "PACCACHE", "SCRUB", "ORPHAN", "PKG", "BOOT", "SVC", "HYPR", "DRIVERS"]


def checks(**over):
    return {"checks": [{"id": g + "01", "status": over.get(g, "PASS"), "text": ""} for g in GROUPS]}


class RewardTest(unittest.TestCase):
    def test_weights_sum_to_100(self):
        self.assertEqual(sum(reward.WEIGHTS.values()), 100)
        self.assertEqual(reward.WEIGHTS["DRIVERS"], 15)
        self.assertEqual(reward.BLOCKING, ("TRIM", "SMART", "DRIVERS"))

    def test_all_pass_is_stable(self):
        r = reward.score(checks())
        self.assertEqual(r["score"], 100)
        self.assertTrue(r["stable"])

    def test_threshold_boundary(self):
        W = reward.WEIGHTS
        r = reward.score(checks(PKG="FAIL"))  # 100 - 8 = 92
        self.assertEqual(r["score"], 100 - W["PKG"])
        self.assertTrue(r["stable"])
        r = reward.score(checks(PKG="FAIL", SVC="FAIL"))  # 100 - 8 - 9 = 83
        self.assertEqual(r["score"], 100 - W["PKG"] - W["SVC"])
        self.assertFalse(r["stable"])

    def test_warn_is_half(self):
        r = reward.score(checks(SVC="WARN"))
        self.assertEqual(r["score"], 100 - reward.WEIGHTS["SVC"] / 2)

    def test_blocking_fail_trim(self):
        r = reward.score(checks(TRIM="FAIL"))
        self.assertEqual(r["score"], 100 - reward.WEIGHTS["TRIM"])
        self.assertFalse(r["stable"])
        self.assertIn("TRIM", r["blockers"])

    def test_blocking_fail_smart_even_if_score_high(self):
        d = checks()
        d["checks"].append({"id": "SMART02", "status": "FAIL", "text": ""})
        r = reward.score(d)
        self.assertFalse(r["stable"])
        self.assertIn("SMART", r["blockers"])

    def test_drivers_fail_blocks(self):
        r = reward.score(checks(DRIVERS="FAIL"))
        self.assertEqual(r["score"], 85)
        self.assertFalse(r["stable"])
        self.assertIn("DRIVERS", r["blockers"])

    def test_drivers_warn_is_not_blocking(self):
        r = reward.score(checks(DRIVERS="WARN"))
        self.assertEqual(r["score"], 92.5)
        self.assertTrue(r["stable"])
        self.assertEqual(r["blockers"], [])

    def test_trim_warn_is_not_blocking(self):
        r = reward.score(checks(TRIM="WARN"))
        self.assertEqual(r["score"], 100 - reward.WEIGHTS["TRIM"] / 2)
        self.assertEqual(r["blockers"], [])

    def test_group_average_and_skip(self):
        d = checks()
        d["checks"] += [{"id": "PKG02", "status": "WARN", "text": ""}, {"id": "HYPR02", "status": "SKIP", "text": ""}]
        r = reward.score(d)
        self.assertEqual(r["groups"]["PKG"]["points"], reward.WEIGHTS["PKG"] * 0.75)
        self.assertEqual(r["groups"]["HYPR"]["points"], reward.WEIGHTS["HYPR"])

    def test_missing_group_is_zero(self):
        d = {"checks": [c for c in checks()["checks"] if not c["id"].startswith("SMART")]}
        r = reward.score(d)
        self.assertEqual(r["score"], 100 - reward.WEIGHTS["SMART"])
        self.assertIn("SMART", r["blockers"])

    def test_all_skip_group_drops_and_rescales(self):
        W = reward.WEIGHTS
        r = reward.score(checks(HYPR="SKIP", PKG="FAIL"))
        self.assertTrue(r["groups"]["HYPR"]["dropped"])
        self.assertEqual(r["score"], round((100 - W["HYPR"] - W["PKG"]) * 100 / (100 - W["HYPR"]), 2))
        r = reward.score(checks(HYPR="SKIP"))
        self.assertEqual(r["score"], 100)
        self.assertTrue(r["stable"])

    def test_blocking_group_all_skip_is_blocker(self):
        for g in ("TRIM", "SMART", "DRIVERS"):
            r = reward.score(checks(**{g: "SKIP"}))
            self.assertIn(g, r["blockers"])
            self.assertFalse(r["stable"])

    def test_broken_json_cli(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as f:
            f.write("{not json")
        try:
            p = subprocess.run([sys.executable, os.path.join(os.path.dirname(HERE), "reward.py"), f.name],
                               capture_output=True, text=True, env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
        finally:
            os.unlink(f.name)
        self.assertEqual(p.returncode, 3)
        self.assertFalse(json.loads(p.stdout)["stable"])

    def test_wrong_shape_cli(self):
        p = subprocess.run([sys.executable, os.path.join(os.path.dirname(HERE), "reward.py")],
                           input='{"x": 1}', capture_output=True, text=True)
        self.assertEqual(p.returncode, 3)

    def test_exit_code_stable(self):
        p = subprocess.run([sys.executable, os.path.join(os.path.dirname(HERE), "reward.py")],
                           input=json.dumps(checks()), capture_output=True, text=True)
        self.assertEqual(p.returncode, 0)


if __name__ == "__main__":
    unittest.main()
