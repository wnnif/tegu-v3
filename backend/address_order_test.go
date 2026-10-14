package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectFromAddress(t *testing.T) {
	address := strings.Repeat("地😀", 150)
	p, err := projectFromAddress(" company ", "\t"+address+"\u3000")
	if err != nil || p.Company != "company" || p.Address != address || p.Name != strings.Repeat("地😀", 100) || !utf8.ValidString(p.Name) {
		t.Fatalf("full address/display name: %+v %v", p, err)
	}
	for _, tc := range []struct{ company, address string }{
		{"", "合成地址"}, {"company", " \t\u3000"}, {"company", strings.Repeat("地", 301)},
	} {
		_, err := projectFromAddress(tc.company, tc.address)
		maintenanceFaultError(t, err, 400)
	}
}
