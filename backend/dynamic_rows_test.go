package main

import (
	"database/sql"
	"strconv"
	"strings"
)

func rows(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]map[string]any, error) {
	r, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	columns, err := r.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for r.Next() {
		values := make([]any, len(columns))
		places := make([]any, len(columns))
		for i := range values {
			places[i] = &values[i]
		}
		if err = r.Scan(places...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, column := range columns {
			if bytes, ok := values[i].([]byte); ok {
				item[column] = string(bytes)
			} else {
				item[column] = values[i]
			}
		}
		out = append(out, item)
	}
	return out, r.Err()
}

func number(item map[string]any, key string) int64 {
	switch value := item[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case string:
		n, _ := strconv.ParseInt(value, 10, 64)
		return n
	}
	return 0
}

func text(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return strings.TrimSpace(value)
}
