package config

import (
	"strings"
	"testing"
)

func TestParseOwners(t *testing.T) {
	got, err := ParseOwners("Ana Pérez|ana@g.com|pa$$w.0rd;  Luis |luis@g.com|otra-1234;")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if got[0] != (OwnerSeed{"Ana Pérez", "ana@g.com", "pa$$w.0rd"}) || got[1].Name != "Luis" {
		t.Fatalf("parsed wrong: %+v", got)
	}
	if owners, err := ParseOwners(""); err != nil || len(owners) != 0 {
		t.Fatalf("empty must mean no owners, got %+v err=%v", owners, err)
	}
}

func TestParseOwnersErrorsNeverLeakThePassword(t *testing.T) {
	for _, bad := range []string{"solo-nombre", "A|a@g.com", "A||clave-secreta-9", "|a@g.com|clave-secreta-9", "A|a@g.com|x|y"} {
		_, err := ParseOwners(bad)
		if err == nil {
			t.Errorf("%q should be rejected", bad)
			continue
		}
		if strings.Contains(err.Error(), "clave-secreta-9") || strings.Contains(err.Error(), "a@g.com") {
			t.Errorf("error leaks the entry: %v", err)
		}
	}
}
