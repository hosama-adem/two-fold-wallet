package history

import "strings"

// escapeCSVCell neutralises CSV/formula injection. A spreadsheet treats a cell
// beginning with =, +, -, or @ as a formula; prefixing a single quote forces it
// to be read as text. Leading tab, carriage return, or spaces can smuggle a
// formula past a check that only looks at the first byte, so the first
// non-whitespace character is tested too. Values that need no escaping are
// returned unchanged — encoding/csv still handles quoting of commas, quotes,
// and newlines.
func escapeCSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed == "" {
		return s
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

// escapeCSVRow escapes every cell in a row (see escapeCSVCell).
func escapeCSVRow(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = escapeCSVCell(cell)
	}
	return out
}
