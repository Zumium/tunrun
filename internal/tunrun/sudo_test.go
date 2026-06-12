package tunrun

import "testing"

func TestSudoCommandArgsPreserveTargetPath(t *testing.T) {
	got := sudoCommandArgs(
		"/usr/local/bin/tunrun",
		"/tmp/tunrun-proxy-123",
		"environment before sudo",
		[]string{"--", "agy"},
		"/home/alice/.local/bin:/usr/local/bin:/usr/bin",
	)
	want := []string{
		"/usr/local/bin/tunrun",
		"_sudo",
		"-proxy-file",
		"/tmp/tunrun-proxy-123",
		"-proxy-source",
		"environment before sudo",
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

func TestProxySourceBeforeSudo(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "config", in: ConfigProxySource, want: "config file before sudo"},
		{name: "cli", in: "-proxy", want: "-proxy"},
		{name: "env", in: "ALL_PROXY", want: "environment before sudo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := proxySourceBeforeSudo(tt.in); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}
