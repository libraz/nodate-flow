package constraint

import "testing"

func TestExplain_Basic(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"op":"time.due_before","arg":"2026-05-01"}`, "due_before(2026-05-01)"},
		{`{"op":"dependency.all_done","taskIds":["a","b"]}`, "all_done(a,b)"},
		{`{"op":"actor.has_role","arg":"reviewer"}`, "actor_has_role(reviewer)"},
		{`{"op":"ci.status_is","arg":"success"}`, "ci_status_is(success)"},
	}
	for _, tc := range cases {
		c, err := Parse([]byte(tc.in))
		if err != nil {
			t.Fatalf("parse %q: %v", tc.in, err)
		}
		if got := Explain(c); got != tc.want {
			t.Errorf("Explain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExplain_Composite(t *testing.T) {
	c, _ := Parse([]byte(`{
		"op":"and","terms":[
			{"op":"ci.status_is","arg":"success"},
			{"op":"not","term":{"op":"signal.received","arg":"github.pr.closed"}}
		]
	}`))
	got := Explain(c)
	want := "(ci_status_is(success) AND NOT signal_received(github.pr.closed))"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExplain_Deterministic(t *testing.T) {
	c, _ := Parse([]byte(`{"op":"or","terms":[
		{"op":"actor.has_role","arg":"reviewer"},
		{"op":"approval.granted","arg":"owner"}
	]}`))
	a := Explain(c)
	b := Explain(c)
	if a != b {
		t.Fatal("Explain must be deterministic")
	}
}
