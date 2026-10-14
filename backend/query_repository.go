package main

import "database/sql"

type rowQuery interface {
	Query(string, ...any) (*sql.Rows, error)
}

func scanRows[T any](q rowQuery, query string, args []any, scan func(*sql.Rows, *T) error) ([]T, error) {
	records, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer records.Close()
	result := []T{}
	for records.Next() {
		var item T
		if err = scan(records, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, records.Err()
}

type queryPageResult[T any] struct {
	Rows  []T   `json:"rows"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
}
