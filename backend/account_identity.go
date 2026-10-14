package main

import (
	"strings"
	"unicode/utf8"
)

// Account maintenance and authentication must address the same stored identity.
// The database's VARCHAR(100) limit counts Unicode characters, not UTF-8 bytes.
func normalizeUsername(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fail(400, "账号不能为空且不能超过100个字符")
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || utf8.RuneCountInString(value) > 100 {
		return "", fail(400, "账号不能为空且不能超过100个字符")
	}
	return value, nil
}
