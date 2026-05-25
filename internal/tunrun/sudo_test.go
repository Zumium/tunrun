package tunrun

import "testing"

func TestSudoCommandArgsPreserveTargetPath(t *testing.T) {
	got := sudoCommandArgs(
		"/usr/local/bin/tunrun",
		"/tmp/tunrun-proxy-123",
		[]string{"--", "agy"},
		"/home/alice/.local/bin:/usr/local/bin:/usr/bin",
	)
	want := []string{
		"/usr/local/bin/tunrun",
		"_sudo",
		"-proxy-file",
		"/tmp/tunrun-proxy-123",
		"-target-path",
		"/home/alice/.local/bin:/usr/local/bin:/usr/bin",
		"--",
		"--",
		"agy",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
