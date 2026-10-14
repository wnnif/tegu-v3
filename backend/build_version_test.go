package main

import (
	"bytes"
	"testing"
)

func TestBuildVersionDefaultsAndOutput(t *testing.T) {
	if Version != "development" || BuildCommit != "development" {
		t.Skip("build version overridden by release ldflags")
	}
	var out bytes.Buffer
	if err := runTegu([]string{"version"}, &out, nil); err != nil || out.String() != "tegu development\nBuildCommit: development\n" {
		t.Fatal("development version must be explicit and need no database factory")
	}
}
