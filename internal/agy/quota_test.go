package agy

import (
	"testing"

	"agy-switch/internal/store"
)

func TestParseUsageJSONAndSummarize(t *testing.T) {
	sample := `Some notice on stderr...
{"conversation_id":"","status":"SUCCESS","command":{"name":"usage","data":{"description":"desc","groups":[{"name":"Gemini Models","description":"Gemini 4","buckets":[{"id":"gemini-weekly","name":"Weekly Limit Remaining","window":"weekly","remaining_fraction":0.9438,"reset_time":"2026-12-03T07:04:50Z"},{"id":"gemini-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":0.9284,"reset_time":"2026-11-26T17:04:50Z"}]},{"name":"Claude and GPT models","buckets":[{"id":"3p-weekly","name":"Weekly Limit Remaining","window":"weekly","remaining_fraction":1,"reset_time":"1970-01-01T00:00:00Z"},{"id":"3p-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":0.75,"reset_time":"2026-11-26T18:00:00Z"}]}]}}}`

	q, err := ParseUsageJSON(sample)
	if err != nil {
		t.Fatalf("ParseUsageJSON failed: %v", err)
	}
	if len(q.Groups) != 2 || q.Source != "live" {
		t.Fatalf("Unexpected groups: %+v", q)
	}

	sum := SummarizeQuota(q)
	if !sum.HasData || sum.Gemini5hPct != 93 || sum.GeminiWkPct != 94 || sum.Claude5hPct != 75 || sum.ClaudeWkPct != 100 {
		t.Fatalf("Unexpected summary: %+v", sum)
	}
}

func TestNormalizeProxyURL(t *testing.T) {
	if got := NormalizeProxyURL("7897"); got != "http://127.0.0.1:7897" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeProxyURL("127.0.0.1:7890"); got != "http://127.0.0.1:7890" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeProxyURL("socks5://127.0.0.1:10808"); got != "socks5://127.0.0.1:10808" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildDarwinAppleScript(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.ProxyURL = "http://127.0.0.1:7897"
	cfg.PreferredTerm = "iterm"

	s := BuildDarwinAppleScript("/usr/local/bin/agy", []string{"--dangerously-skip-permissions", "-c"}, "/Users/dev/proj", cfg)
	if s == "" {
		t.Fatal("empty applescript")
	}
	for _, want := range []string{`tell application "iTerm"`, `HTTPS_PROXY`, `/usr/local/bin/agy`, `-c`} {
		if !containsStr(s, want) {
			t.Fatalf("expected %q in script:\n%s", want, s)
		}
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexStr(haystack, needle) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
