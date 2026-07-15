package languages

import (
	"reflect"
	"testing"
)

func TestRustPositiveTraitBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "qualified raw unicode and omitted non-positive bounds",
			raw:  "crate::r#match::Διαβάζει<Item = T> + ?Sized + 'a",
			want: []string{"crate::match::Διαβάζει"},
		},
		{
			name: "higher ranked trait bound with whitespace",
			raw:  "for <'a> Fn(&'a str) + Send",
			want: []string{"Fn", "Send"},
		},
		{
			name: "optional qualified trait is omitted",
			raw:  "?crate::Maybe + Sync",
			want: []string{"Sync"},
		},
		{
			name: "unmatched close rejects entire list",
			raw:  "Matcher> + Send",
		},
		{
			name: "unmatched open rejects entire list",
			raw:  "Matcher<Item = T + Send",
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rustPositiveTraitBounds(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rustPositiveTraitBounds(%q) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}
