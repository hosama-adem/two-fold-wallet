package history

import "testing"

func TestEscapeCSVCell(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "hello", "hello"},
		{"equals", "=1+1", "'=1+1"},
		{"plus", "+1", "'+1"},
		{"minus", "-1", "'-1"},
		{"at", "@SUM(A1)", "'@SUM(A1)"},
		{"tab", "\tcmd", "'\tcmd"},
		{"carriage_return", "\rcmd", "'\rcmd"},
		{"formula_classic", "=cmd|'/C calc'!A0", "'=cmd|'/C calc'!A0"},
		{"inner_plus_safe", "a+b", "a+b"},
		{"inner_equals_safe", "a=b", "a=b"},
		{"leading_space_formula", " =1", "' =1"},
		{"leading_newline_formula", "\n=1+1", "'\n=1+1"},
		{"negative_number_is_escaped", "-250", "'-250"},
		{"amharic", "ሰላም", "ሰላም"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := escapeCSVCell(c.in); got != c.want {
				t.Fatalf("escapeCSVCell(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestEscapeCSVRow(t *testing.T) {
	in := []string{"=1", "safe", "-2", ""}
	want := []string{"'=1", "safe", "'-2", ""}
	got := escapeCSVRow(in)
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cell %d = %q, want %q", i, got[i], want[i])
		}
	}
}
