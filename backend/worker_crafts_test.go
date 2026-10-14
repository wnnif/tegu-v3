package main

import "testing"

func TestWorkerCraftsValidation(t *testing.T) {
	for _, raw := range []any{nil, "开工", []string{"不存在"}, []string{"开工", "开工"}, []any{1}} {
		if _, err := parseWorkerCrafts(raw); err == nil {
			t.Fatalf("invalid authorization accepted: %v", raw)
		}
	}
	for _, raw := range []any{[]string{}, []string{"开工", "维修"}, categories} {
		if _, err := parseWorkerCrafts(raw); err != nil {
			t.Fatal(err)
		}
	}
}
