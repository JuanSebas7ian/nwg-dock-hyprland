package agents

import (
	"testing"
	"time"
)

func TestStatsWeekTodayAndModels(t *testing.T) {
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.Local)
	s := NewStats(now)
	s.Add("a", now, "m1", 100, 20, 5, 0)
	s.Add("a", now.AddDate(0, 0, -2), "m1", 10, 0, 0, 0)
	s.Add("b", now.AddDate(0, 0, -30), "m2", 1, 1, 0, 0) // outside the week, still in totals
	s.Add("c", now, "m3", 0, 0, 0, 0)                    // nothing: ignored
	f := s.Fields()
	days := f["recentDays"].([]map[string]any)
	if len(days) != 7 || days[6]["messageCount"] != 125 || days[4]["messageCount"] != 10 {
		t.Fatalf("%v", days)
	}
	if f["totalPrompts"] != 3 || f["totalSessions"] != 2 || f["todayPrompts"] != 1 || f["todayTotalTokens"] != 125 || f["activeDays"] != 3 {
		t.Fatalf("%v", f)
	}
	if s.models["m1"].InputTokens != 110 || s.models["m3"] != nil {
		t.Fatalf("%+v", s.models)
	}
	r := UsageRecord("x", "X", now, nil, "tier", "", "help", s)
	if r["id"] != "x" || r["limits"] == nil || r["modelUsage"] == nil {
		t.Fatalf("%v", r)
	}
}

func TestOpencodeMessages(t *testing.T) {
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.Local)
	s := NewStats(now)
	ms := now.UnixMilli()
	local := map[string]bool{"ollama": true}
	AddOpencodeMessage(s, "s1", `{"role":"assistant","providerID":"ollama","modelID":"qwen:7b","time":{"created":`+itoa(ms)+`},"tokens":{"input":100,"output":10,"reasoning":5,"cache":{"read":50,"write":2}}}`, local)
	AddOpencodeMessage(s, "s1", `{"role":"assistant","providerID":"anthropic","modelID":"claude","time":{"created":`+itoa(ms)+`},"tokens":{"input":1}}`, local)
	AddOpencodeMessage(s, "s1", `{"role":"user","time":{"created":1}}`, local)
	AddOpencodeMessage(s, "s1", `not json`, local)
	q := s.models["qwen:7b · local"]
	if q == nil || q.OutputTokens != 15 || q.CacheReadInputTokens != 50 || s.models["claude · anthropic"] == nil || s.prompts != 2 {
		t.Fatalf("%+v", s.models)
	}
}

// pb helpers to build test protobufs
func pbv(n uint64) []byte {
	var b []byte
	for n >= 0x80 {
		b = append(b, byte(n)|0x80)
		n >>= 7
	}
	return append(b, byte(n))
}
func pbVar(num int, v uint64) []byte { return append(pbv(uint64(num<<3)), pbv(v)...) }
func pbMsg(num int, b []byte) []byte {
	return append(append(pbv(uint64(num<<3|2)), pbv(uint64(len(b)))...), b...)
}
func cat(parts ...[]byte) (out []byte) {
	for _, p := range parts {
		out = append(out, p...)
	}
	return
}

func TestParseGeneration(t *testing.T) {
	usage := cat(pbVar(2, 1200), pbVar(3, 300), pbVar(4, 7), pbVar(5, 900))
	timing := pbMsg(4, pbVar(1, 1791700000))
	meta := cat(pbMsg(4, usage), pbMsg(19, []byte("gemini-3-pro")), pbMsg(9, timing))
	g, ok := ParseGeneration(pbMsg(1, meta))
	if !ok || g.Model != "gemini-3-pro" || g.In != 1200 || g.Out != 300 || g.CacheRead != 900 || g.CacheWrite != 7 || g.When.Unix() != 1791700000 {
		t.Fatalf("%+v %v", g, ok)
	}
	if _, ok := ParseGeneration(pbMsg(1, pbMsg(4, usage))); ok {
		t.Fatal("no timestamp should be skipped")
	}
	if _, ok := ParseGeneration([]byte{0xff, 0xff}); ok {
		t.Fatal("garbage accepted")
	}
}

func TestQuotaFromStatusAndNotReset(t *testing.T) {
	st := map[string]any{"userStatus": map[string]any{
		"planStatus": map[string]any{"planInfo": map[string]any{"planName": "Pro"}},
		"cascadeModelConfigData": map[string]any{"clientModelConfigs": []any{
			map[string]any{"label": "Gemini Flash", "quotaInfo": map[string]any{"remainingFraction": 0.9, "resetTime": "2026-10-12T00:00:00Z"}},
			map[string]any{"modelOrAlias": map[string]any{"model": "claude-opus"}, "quotaInfo": map[string]any{"remainingFraction": 0.25}},
			map[string]any{"label": "no quota"},
		}}}}
	q := QuotaFromStatus(st)
	if q.TierLabel != "Pro" || len(q.Limits) != 2 || q.Limits[0]["label"] != "claude-opus" || q.Limits[0]["percent"] != 0.75 {
		t.Fatalf("%+v", q)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if !NotReset("2026-10-12T00:00:00Z", now) || NotReset("2026-10-10T00:00:00Z", now) || !NotReset("", now) {
		t.Fatal("NotReset")
	}
}
