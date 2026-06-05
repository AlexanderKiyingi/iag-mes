package handlers

import "testing"

func TestMapStageEvent(t *testing.T) {
	cases := map[string]string{
		"wetmill:started":  "mes.wetmill.started",
		"wetmill:completed": "mes.wetmill.completed",
		"drying:started":   "mes.drying.started",
		"drying:completed": "mes.drying.completed",
		"drymill:completed": "mes.drymill.completed",
	}
	for key, want := range cases {
		parts := splitKey(key)
		if got := mapStageEvent(parts[0], parts[1]); got != want {
			t.Fatalf("%s: got %q want %q", key, got, want)
		}
	}
}

func splitKey(s string) [2]string {
	i := 0
	for j, ch := range s {
		if ch == ':' {
			i = j
			break
		}
	}
	if i == 0 {
		return [2]string{s, ""}
	}
	return [2]string{s[:i], s[i+1:]}
}
