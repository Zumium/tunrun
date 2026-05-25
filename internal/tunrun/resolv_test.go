package tunrun

import (
	"os"
	"testing"
)

func TestReadResolvConfNameservers(t *testing.T) {
	path := t.TempDir() + "/resolv.conf"
	if err := os.WriteFile(path, []byte(`
# comment
search local
nameserver 10.255.255.254
nameserver 10.255.255.254 # duplicate
nameserver 1.1.1.1
nameserver not-an-ip
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readResolvConfNameservers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].String() != "10.255.255.254" || got[1].String() != "1.1.1.1" {
		t.Fatalf("unexpected nameservers: %v", got)
	}
}
