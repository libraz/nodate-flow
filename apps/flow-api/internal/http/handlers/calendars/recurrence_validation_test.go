package calendars

import (
	"encoding/json"
	"testing"
)

// rawRule wraps a JSON literal in a *json.RawMessage for table tests.
func rawRule(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

func TestValidateRecurrenceRule(t *testing.T) {
	tests := []struct {
		name    string
		rule    *json.RawMessage
		wantErr bool
	}{
		{"nil rule ok", nil, false},
		{"empty rule ok", rawRule(``), false},
		{"null rule ok", rawRule(`null`), false},
		{"valid daily", rawRule(`{"freq":"daily","interval":1}`), false},
		{"valid weekly byDay", rawRule(`{"freq":"weekly","interval":1,"byDay":["MO","WE"]}`), false},
		{"valid monthly byMonthDay", rawRule(`{"freq":"monthly","interval":1,"byMonthDay":[15]}`), false},
		{"valid monthly bySetPos", rawRule(`{"freq":"monthly","interval":1,"byDay":["MO"],"bySetPos":2}`), false},
		{"valid yearly", rawRule(`{"freq":"yearly"}`), false},
		{"valid no interval", rawRule(`{"freq":"daily"}`), false},
		{"valid until date", rawRule(`{"freq":"daily","until":"2025-12-31"}`), false},
		{"valid until rfc3339", rawRule(`{"freq":"daily","until":"2025-12-31T23:59:59Z"}`), false},
		{"valid count", rawRule(`{"freq":"daily","count":10}`), false},

		{"malformed json rejected", rawRule(`{not json`), true},
		{"uppercase freq rejected", rawRule(`{"freq":"DAILY","interval":1}`), true},
		{"capitalized freq rejected", rawRule(`{"freq":"Daily","interval":1}`), true},
		{"unknown freq rejected", rawRule(`{"freq":"hourly","interval":1}`), true},
		{"empty freq rejected", rawRule(`{"freq":"","interval":1}`), true},
		{"interval zero rejected", rawRule(`{"freq":"daily","interval":0}`), true},
		{"interval negative rejected", rawRule(`{"freq":"daily","interval":-1}`), true},
		{"interval too large rejected", rawRule(`{"freq":"daily","interval":1000}`), true},
		{"count zero rejected", rawRule(`{"freq":"daily","count":0}`), true},
		{"count too large rejected", rawRule(`{"freq":"daily","count":5000}`), true},
		{"bad byDay rejected", rawRule(`{"freq":"weekly","byDay":["XX"]}`), true},
		{"lowercase byDay rejected", rawRule(`{"freq":"weekly","byDay":["mo"]}`), true},
		{"byMonthDay zero rejected", rawRule(`{"freq":"monthly","byMonthDay":[0]}`), true},
		{"byMonthDay out of range rejected", rawRule(`{"freq":"monthly","byMonthDay":[40]}`), true},
		{"bySetPos zero rejected", rawRule(`{"freq":"monthly","byDay":["MO"],"bySetPos":0}`), true},
		{"bySetPos too large rejected", rawRule(`{"freq":"monthly","byDay":["MO"],"bySetPos":6}`), true},
		{"unparseable until rejected", rawRule(`{"freq":"daily","until":"not-a-date"}`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateRecurrenceRule(tt.rule)
			if (got != nil) != tt.wantErr {
				t.Fatalf("validateRecurrenceRule(%v) = %v, wantErr = %v", tt.rule, got, tt.wantErr)
			}
		})
	}
}
