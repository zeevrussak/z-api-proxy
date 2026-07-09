package tunnel

import "testing"

// TestTunnelURLPattern covers tunnelURLPattern, the regex startQuick's
// stdout/stderr scanner uses to pull the ephemeral trycloudflare.com URL
// out of cloudflared's decorated box-drawing log output.
func TestTunnelURLPattern(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "url inside cloudflared's decorative box output",
			line: "2026-01-01T00:00:00Z INF |  https://random-words-here.trycloudflare.com                                     |",
			want: "https://random-words-here.trycloudflare.com",
		},
		{
			name: "no url present",
			line: "2026-01-01T00:00:00Z INF Starting tunnel...",
			want: "",
		},
		{
			name: "http (not https) is not matched",
			line: "2026-01-01T00:00:00Z INF |  http://random-words-here.trycloudflare.com  |",
			want: "",
		},
		{
			name: "trailing path/query is not part of the match",
			line: "see https://random-words-here.trycloudflare.com/foo?bar=1 for details",
			want: "https://random-words-here.trycloudflare.com",
		},
		{
			name: "uppercase hostname does not match (char class is lowercase-only)",
			line: "https://Random-Words-Here.trycloudflare.com",
			want: "",
		},
		{
			name: "multiple urls in one line - FindString returns only the first",
			line: "https://first-one.trycloudflare.com and also https://second-one.trycloudflare.com",
			want: "https://first-one.trycloudflare.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tunnelURLPattern.FindString(tt.line)
			if got != tt.want {
				t.Errorf("FindString(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

// TestTunnelURLPattern_TrailingPathExcludedExplicitly is a focused,
// standalone assertion (beyond the table test above) that the match
// never includes anything past the bare host, since the pattern has no
// trailing anchor and no '/' in its char class — this is relied upon by
// startQuick, which uses the match verbatim as the tunnel's base URL.
func TestTunnelURLPattern_TrailingPathExcludedExplicitly(t *testing.T) {
	line := "https://abc-def.trycloudflare.com/some/path?x=1"
	got := tunnelURLPattern.FindString(line)
	want := "https://abc-def.trycloudflare.com"
	if got != want {
		t.Fatalf("FindString(%q) = %q, want %q (path/query must not be captured)", line, got, want)
	}
	if got == line {
		t.Fatal("match unexpectedly included the full line with path/query")
	}
}
