package reward

import "testing"

func checks(over map[string]string) []Check {
	var out []Check
	for _, g := range Order {
		s := "PASS"
		if v, ok := over[g]; ok {
			s = v
		}
		out = append(out, Check{ID: g + "01", Status: s})
	}
	return out
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestWeightsSumTo100(t *testing.T) {
	sum := 0.0
	for _, g := range Order {
		sum += Weights[g]
	}
	if sum != 100 || len(Order) != len(Weights) || Weights["DRIVERS"] != 15 {
		t.Fatal(sum)
	}
}

func TestScores(t *testing.T) {
	W := Weights
	for _, c := range []struct {
		over     map[string]string
		score    float64
		stable   bool
		blockers []string
	}{
		{nil, 100, true, nil},
		{map[string]string{"GUARD": "FAIL", "MEMG": "FAIL"}, 100 - W["GUARD"] - W["MEMG"], true, nil},
		{map[string]string{"PKG": "FAIL"}, 100 - W["PKG"], true, nil},
		{map[string]string{"PKG": "FAIL", "SVC": "FAIL", "SCRUB": "FAIL"}, 100 - W["PKG"] - W["SVC"] - W["SCRUB"], false, nil},
		{map[string]string{"SVC": "WARN"}, 100 - W["SVC"]/2, true, nil},
		{map[string]string{"TRIM": "FAIL"}, 100 - W["TRIM"], false, []string{"TRIM"}},
		{map[string]string{"DRIVERS": "FAIL"}, 85, false, []string{"DRIVERS"}},
		{map[string]string{"DRIVERS": "WARN"}, 92.5, true, nil},
		{map[string]string{"TRIM": "WARN"}, 100 - W["TRIM"]/2, true, nil},
		{map[string]string{"HYPR": "SKIP"}, 100, true, nil},
		{map[string]string{"HYPR": "SKIP", "PKG": "FAIL"}, round2((100 - W["HYPR"] - W["PKG"]) * 100 / (100 - W["HYPR"])), true, nil},
	} {
		r := Score(checks(c.over))
		if r.Score != c.score || r.Stable != c.stable || len(r.Blockers) != len(c.blockers) {
			t.Errorf("%v: %+v", c.over, r)
		}
		for _, b := range c.blockers {
			if !has(r.Blockers, b) {
				t.Errorf("%v: missing blocker %s", c.over, b)
			}
		}
	}
}

func TestBlockingFailEvenIfScoreHigh(t *testing.T) {
	r := Score(append(checks(nil), Check{ID: "SMART02", Status: "FAIL"}))
	if r.Stable || !has(r.Blockers, "SMART") {
		t.Fatal(r)
	}
}

func TestGroupAverageSkipAndMissing(t *testing.T) {
	r := Score(append(checks(nil), Check{ID: "PKG02", Status: "WARN"}, Check{ID: "HYPR02", Status: "SKIP"}))
	if r.Groups["PKG"].Points != Weights["PKG"]*0.75 || r.Groups["HYPR"].Points != Weights["HYPR"] {
		t.Fatal(r.Groups)
	}
	var noSmart []Check
	for _, c := range checks(nil) {
		if c.ID != "SMART01" {
			noSmart = append(noSmart, c)
		}
	}
	if r := Score(noSmart); r.Score != 85 || !has(r.Blockers, "SMART") {
		t.Fatal(r)
	}
	for _, g := range Blocking {
		if r := Score(checks(map[string]string{g: "SKIP"})); r.Stable || !has(r.Blockers, g) {
			t.Fatal(g, r)
		}
	}
	if !Score(checks(map[string]string{"HYPR": "SKIP"})).Groups["HYPR"].Dropped {
		t.Fatal("not dropped")
	}
}

func TestLongestPrefixWins(t *testing.T) {
	if GroupOf("SMART01") != "SMART" || GroupOf("SPLASH01") != "SPLASH" || GroupOf("XYZ") != "" {
		t.Fatal("GroupOf")
	}
}
