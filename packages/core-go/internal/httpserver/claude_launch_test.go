package httpserver

import "testing"

func TestWithTicketContext(t *testing.T) {
	cases := []struct {
		name     string
		prompt   string
		ticketID string
		want     string
	}{
		{
			name:     "no ticket id leaves prompt untouched",
			prompt:   "この対応は終わったのでDONEもしくは削除して",
			ticketID: "",
			want:     "この対応は終わったのでDONEもしくは削除して",
		},
		{
			name:     "custom prompt without the ticket id gets it prepended",
			prompt:   "この対応は終わったのでDONEもしくは削除して",
			ticketID: "DFLT-00014",
			want:     "This instruction concerns ticket DFLT-00014.\n\nこの対応は終わったのでDONEもしくは削除して",
		},
		{
			name:     "default prompt already naming the ticket is left as-is",
			prompt:   "チケット DFLT-00014 の未完了ノードを実行してください",
			ticketID: "DFLT-00014",
			want:     "チケット DFLT-00014 の未完了ノードを実行してください",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withTicketContext(tc.prompt, tc.ticketID)
			if got != tc.want {
				t.Fatalf("withTicketContext(%q, %q) = %q, want %q", tc.prompt, tc.ticketID, got, tc.want)
			}
		})
	}
}
