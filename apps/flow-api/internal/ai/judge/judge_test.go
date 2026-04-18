package judge

import (
	"testing"
)

func TestDeterministicJudge_AllPass(t *testing.T) {
	t.Parallel()
	r := Rubric{
		Name: "triage.open",
		Criteria: []Criterion{
			{Name: "mentions action", Contains: []string{"open"}},
			{Name: "short enough", MaxLength: 80},
		},
	}
	rep := (DeterministicJudge{}).Evaluate(r, "Open the PR review promptly")
	if !rep.Passed {
		t.Fatalf("expected pass, got %+v", rep)
	}
	if rep.Score < 1 {
		t.Fatalf("expected full score, got %v", rep.Score)
	}
}

func TestDeterministicJudge_PartialFailWeighted(t *testing.T) {
	t.Parallel()
	r := Rubric{
		Name:      "digest",
		PassScore: 0.75,
		Criteria: []Criterion{
			{Name: "has summary", Contains: []string{"summary"}, Weight: 3},
			{Name: "no secrets", NotContains: []string{"api_key"}, Weight: 1},
		},
	}
	rep := (DeterministicJudge{}).Evaluate(r, "Weekly summary of work")
	if !rep.Passed {
		t.Fatalf("weighted 3/4 should clear 0.75 threshold; got %+v", rep)
	}
	rep2 := (DeterministicJudge{}).Evaluate(r, "api_key=abc123 note")
	if rep2.Passed {
		t.Fatalf("forbidden substring should fail; got %+v", rep2)
	}
}

func TestDeterministicJudge_JSONKeyCheck(t *testing.T) {
	t.Parallel()
	r := Rubric{
		Name: "lens",
		Criteria: []Criterion{
			{Name: "lens shape", RequiredJSONKeys: []string{"filters", "sort"}},
		},
	}
	rep := (DeterministicJudge{}).Evaluate(r, `{"filters":{},"sort":"due"}`)
	if !rep.Passed {
		t.Fatalf("expected pass, got %+v", rep)
	}
	bad := (DeterministicJudge{}).Evaluate(r, `{"filters":{}}`)
	if bad.Passed {
		t.Fatalf("expected missing key to fail, got %+v", bad)
	}
	notJSON := (DeterministicJudge{}).Evaluate(r, `not json`)
	if notJSON.Passed {
		t.Fatalf("expected non-JSON to fail, got %+v", notJSON)
	}
}

func TestDeterministicJudge_LengthBounds(t *testing.T) {
	t.Parallel()
	r := Rubric{
		Name: "reminder",
		Criteria: []Criterion{
			{Name: "bounds", MinLength: 5, MaxLength: 20},
		},
	}
	if (DeterministicJudge{}).Evaluate(r, "hi").Passed {
		t.Fatal("short candidate should fail")
	}
	if !(DeterministicJudge{}).Evaluate(r, "hello there").Passed {
		t.Fatal("in-range should pass")
	}
	if (DeterministicJudge{}).Evaluate(r, "this string is much much too long").Passed {
		t.Fatal("too-long should fail")
	}
}

func TestDeterministicJudge_EmptyRubric(t *testing.T) {
	t.Parallel()
	rep := (DeterministicJudge{}).Evaluate(Rubric{Name: "empty"}, "anything")
	if !rep.Passed {
		t.Fatalf("empty rubric should vacuously pass, got %+v", rep)
	}
}
