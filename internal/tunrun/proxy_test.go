package tunrun

import "testing"

func TestParseProxy(t *testing.T) {
	p, err := ParseProxy("socks5://user:pass@127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	if p.Scheme != "socks5" || p.Type != "socks" || p.Host != "127.0.0.1" || p.Port != 1080 || p.Username != "user" || p.Password != "pass" {
		t.Fatalf("unexpected proxy: %+v", p)
	}
}

func TestProxyURLWithEndpointKeepsAuth(t *testing.T) {
	p, err := ParseProxy("http://user:pass@127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}

	got := p.URLWithEndpoint("169.254.80.1", 45678)
	want := "http://user:pass@169.254.80.1:45678"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestProxyFromEnvironment(t *testing.T) {
	proxyURL, source, ok := ProxyFromEnvironment([]string{
		"HTTP_PROXY=http://127.0.0.1:7890",
		"ALL_PROXY=socks5://127.0.0.1:1080",
	})
	if !ok {
		t.Fatal("expected proxy from environment")
	}
	if proxyURL != "socks5://127.0.0.1:1080" || source != "ALL_PROXY" {
		t.Fatalf("got %q from %q", proxyURL, source)
	}
}

func TestEnvironmentWithoutProxy(t *testing.T) {
	got := EnvironmentWithoutProxy([]string{
		"PATH=/bin",
		"HTTP_PROXY=http://127.0.0.1:7890",
		"no_proxy=localhost",
		"HOME=/tmp",
	})
	want := []string{"PATH=/bin", "HOME=/tmp"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestTargetEnvironment(t *testing.T) {
	got := TargetEnvironment([]string{
		"PATH=/bin",
		"HTTP_PROXY=http://127.0.0.1:7890",
		"SUDO_UID=1000",
		"HOME=/root",
		"USER=root",
	}, TargetIdentity{
		Valid:    true,
		Username: "alice",
		HomeDir:  "/home/alice",
		Shell:    "/bin/bash",
	})
	want := []string{"PATH=/bin", "HOME=/home/alice", "USER=alice", "LOGNAME=alice", "SHELL=/bin/bash"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
