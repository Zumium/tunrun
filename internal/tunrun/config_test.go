package tunrun

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadConfigMissingFile(t *testing.T) {
	_, ok, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected missing config to be ignored")
	}
}

func TestLoadConfigProxy(t *testing.T) {
	path := writeTestConfig(t, `# tunrun config
proxy = "socks5://127.0.0.1:1080" # comment
`)

	cfg, ok, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected config to be loaded")
	}
	if cfg.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("got proxy %q", cfg.ProxyURL)
	}
}

func TestLoadConfigRejectsUnknownKey(t *testing.T) {
	path := writeTestConfig(t, `dns = "1.1.1.1:53"`)

	_, _, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected unknown key error")
	}
}

func TestLoadConfigRejectsMalformedLine(t *testing.T) {
	path := writeTestConfig(t, `proxy "socks5://127.0.0.1:1080"`)

	_, _, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected malformed line error")
	}
}

func TestLoadConfigRejectsEmptyProxy(t *testing.T) {
	path := writeTestConfig(t, `proxy = ""`)

	_, _, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected empty proxy error")
	}
}

func TestResolveProxyPrecedence(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "tunrun")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`proxy = "socks5://127.0.0.1:1080"`), 0o644); err != nil {
		t.Fatal(err)
	}

	env := []string{
		"HOME=" + home,
		"ALL_PROXY=http://127.0.0.1:7890",
	}

	resolved, ok, err := ResolveProxy("http://127.0.0.1:8080", true, env)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || resolved.URL != "http://127.0.0.1:8080" || resolved.Source != "-proxy" {
		t.Fatalf("got %+v ok=%v", resolved, ok)
	}

	resolved, ok, err = ResolveProxy("", false, env)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || resolved.URL != "socks5://127.0.0.1:1080" || resolved.Source != ConfigProxySource {
		t.Fatalf("got %+v ok=%v", resolved, ok)
	}
}

func TestResolveProxyFallsBackToEnvironment(t *testing.T) {
	resolved, ok, err := ResolveProxy("", false, []string{
		"HOME=" + t.TempDir(),
		"ALL_PROXY=http://127.0.0.1:7890",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || resolved.URL != "http://127.0.0.1:7890" || resolved.Source != "ALL_PROXY" {
		t.Fatalf("got %+v ok=%v", resolved, ok)
	}
}

func TestResolveProxyValidatesConfigBeforeCLI(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "tunrun")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`dns = "1.1.1.1:53"`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := ResolveProxy("http://127.0.0.1:8080", true, []string{"HOME=" + home})
	if err == nil {
		t.Fatal("expected malformed config to block execution")
	}
}

func TestDefaultConfigPathUsesSudoCallerHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	path, err := DefaultConfigPath([]string{
		"HOME=/root",
		"SUDO_UID=" + strconv.Itoa(os.Getuid()),
		"SUDO_GID=" + strconv.Itoa(os.Getgid()),
	})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, ".config", "tunrun", "config.toml")
	if path != want {
		t.Fatalf("got %q want %q", path, want)
	}
}

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
