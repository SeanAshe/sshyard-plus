package migration

import (
	"encoding/csv"
	"strings"
)

// parseCSV reads a host table whose first row names its columns with the
// keys of a text import: one session per row, a cell under its column
// name as a key=value field, and a blank cell as no field. Columns split
// on commas, or on semicolons or tabs when the header has no comma; rows
// without a value and # comments are skipped.
func parseCSV(data []byte, _ string) (Result, error) {
	text := string(data)
	reader := csv.NewReader(strings.NewReader(text))
	if header, _, _ := strings.Cut(text, "\n"); !strings.Contains(header, ",") {
		if strings.Contains(header, ";") {
			reader.Comma = ';'
		} else if strings.Contains(header, "\t") {
			reader.Comma = '\t'
		}
	}
	reader.Comment = '#'
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	rows, err := reader.ReadAll()
	if err != nil || len(rows) == 0 {
		return Result{}, ErrFormat
	}
	keys := rows[0]
	address := false
	for i, k := range keys {
		keys[i] = strings.TrimSpace(k)
		address = address || strings.EqualFold(keys[i], "host") || strings.EqualFold(keys[i], "ip")
	}
	if !address {
		return Result{}, ErrFormat
	}
	var r Result
	for _, row := range rows[1:] {
		var fields []textField
		for i, v := range row[:min(len(row), len(keys))] {
			if !strings.EqualFold(keys[i], "password") {
				v = strings.TrimSpace(v)
			}
			if v != "" && keys[i] != "" {
				fields = append(fields, textField{keys[i], v})
			}
		}
		if len(fields) > 0 {
			r.Entries = append(r.Entries, textEntry(fields))
		}
	}
	return r, nil
}
