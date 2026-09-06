package testreport

import "testing"

func TestExternalTestID(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		fullTitle string
		want      string // "" means nil
	}{
		{
			name:      "id on the leaf title",
			title:     "MM-T4783_1 should post a message",
			fullTitle: "Channel > MM-T4783_1 should post a message",
			want:      "MM-T4783_1",
		},
		{
			name:      "id only on the ancestor path is still found",
			title:     "should post a message",
			fullTitle: "MM-T107 Channel > should post a message",
			want:      "MM-T107",
		},
		{
			name:      "fullTitle wins when both carry an id",
			title:     "MM-T999 leaf",
			fullTitle: "MM-T107 parent > MM-T999 leaf",
			want:      "MM-T107",
		},
		{
			name:      "falls back to title when fullTitle is empty",
			title:     "MM-T4862_2 reactor",
			fullTitle: "",
			want:      "MM-T4862_2",
		},
		{
			name:      "no id yields nil",
			title:     "logs in with email",
			fullTitle: "Login > logs in with email",
			want:      "",
		},
		{
			name:      "does not match mid-word",
			title:     "XMM-T123 not an id",
			fullTitle: "XMM-T123 not an id",
			want:      "",
		},
		{
			name:      "long ids are not truncated",
			title:     "MM-T123456 wide id",
			fullTitle: "MM-T123456 wide id",
			want:      "MM-T123456",
		},
		{
			name:      "sub-case suffix is captured",
			title:     "MM-T585_12 sub case",
			fullTitle: "MM-T585_12 sub case",
			want:      "MM-T585_12",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExternalTestID(tc.title, tc.fullTitle)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("ExternalTestID(%q, %q) = %q, want nil", tc.title, tc.fullTitle, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ExternalTestID(%q, %q) = nil, want %q", tc.title, tc.fullTitle, tc.want)
			}
			if *got != tc.want {
				t.Fatalf("ExternalTestID(%q, %q) = %q, want %q", tc.title, tc.fullTitle, *got, tc.want)
			}
		})
	}
}
