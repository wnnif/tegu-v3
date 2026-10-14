package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudCredentialRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	encoded, e := encryptSecret("test-only-credential")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(encoded, "test-only-credential") {
		t.Fatal("plaintext stored")
	}
	plain, e := decryptSecret(encoded)
	if e != nil || plain != "test-only-credential" {
		t.Fatal("credential roundtrip", e)
	}
	if _, e = decryptSecret(encoded[:len(encoded)-4] + "AAAA"); e == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if e = os.WriteFile(filepath.Join(dir, "backup.key"), make([]byte, 32), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = decryptSecret(encoded); e == nil {
		t.Fatal("wrong restore key accepted")
	}
}

func TestScaledDecimal(t *testing.T) {
	for _, c := range []struct {
		n    int64
		p    int
		want string
	}{{1, 2, "0.01"}, {-1, 2, "-0.01"}, {100, 2, "1.00"}, {9007199254740993, 2, "90071992547409.93"}, {-9223372036854775808, 2, "-92233720368547758.08"}, {1234567, 6, "1.234567"}} {
		if got := scaledDecimal(c.n, c.p); got != c.want {
			t.Fatalf("got %s want %s", got, c.want)
		}
	}
}
