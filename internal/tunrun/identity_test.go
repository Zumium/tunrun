package tunrun

import "testing"

func TestTargetIdentityFromEnvironmentMissing(t *testing.T) {
	identity, err := TargetIdentityFromEnvironment([]string{"PATH=/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Valid {
		t.Fatalf("expected no sudo identity: %+v", identity)
	}
}

func TestTargetIdentityFromEnvironmentInvalid(t *testing.T) {
	_, err := TargetIdentityFromEnvironment([]string{"SUDO_UID=bad", "SUDO_GID=1000"})
	if err == nil {
		t.Fatal("expected invalid uid error")
	}
}

func TestFormatAndParseGroupList(t *testing.T) {
	raw := FormatGroupList([]uint32{1000, 27})
	groups, err := ParseGroupList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0] != 1000 || groups[1] != 27 {
		t.Fatalf("unexpected groups: %v", groups)
	}
}
