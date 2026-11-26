package calendars

import "testing"

func TestLeastUsedPaletteColor(t *testing.T) {
	tests := []struct {
		name string
		used []string
		want string
	}{
		{"empty takes the first", nil, "#47B2F7"},
		{"unused colours go in palette order", []string{"#47B2F7"}, "#F35F8C"},
		{"case is ignored", []string{"#47b2f7", "#f35f8c"}, "#B38BDC"},
		{"off-palette colours count for nothing", []string{"#4285F4", "#ABCDEF", "red"}, "#47B2F7"},
		{
			"every colour used once wraps to the first",
			[]string{"#47B2F7", "#F35F8C", "#B38BDC", "#FDC02D", "#E73B3B", "#2ECC87", "#F5A623", "#8F8F8F", "#42A5F5", "#FF7043"},
			"#47B2F7",
		},
		{
			"fewest uses wins over order",
			[]string{"#47B2F7", "#47B2F7", "#F35F8C", "#B38BDC", "#FDC02D", "#E73B3B", "#2ECC87", "#F5A623", "#8F8F8F", "#42A5F5", "#FF7043", "#FF7043"},
			"#F35F8C",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := leastUsedPaletteColor(tc.used); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
