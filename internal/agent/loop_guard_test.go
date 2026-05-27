package agent

import "testing"

func TestAllToolsErrored(t *testing.T) {
	cases := []struct {
		name    string
		results []map[string]interface{}
		want    bool
	}{
		{
			name:    "all errored",
			results: []map[string]interface{}{{"is_error": true}, {"is_error": true}},
			want:    true,
		},
		{
			name:    "mixed (one success) resets",
			results: []map[string]interface{}{{"is_error": true}, {"is_error": false}},
			want:    false,
		},
		{
			name:    "empty round is not a failure",
			results: []map[string]interface{}{},
			want:    false,
		},
		{
			name:    "missing is_error treated as non-error",
			results: []map[string]interface{}{{"content": "ok"}},
			want:    false,
		},
		{
			name:    "single error",
			results: []map[string]interface{}{{"is_error": true}},
			want:    true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allToolsErrored(c.results); got != c.want {
				t.Errorf("allToolsErrored = %v, want %v", got, c.want)
			}
		})
	}
}
