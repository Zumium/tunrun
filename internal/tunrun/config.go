package tunrun

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ConfigProxySource = "config file"

type FileConfig struct {
	ProxyURL string
}

type ProxyResolution struct {
	URL    string
	Source string
}

func DefaultConfigPath(env []string) (string, error) {
	home, err := configHomeDir(env)
	if err != nil {
		return "", err
	}
	if home == "" {
		return "", nil
	}
	return filepath.Join(home, ".config", "tunrun", "config.toml"), nil
}

func ResolveProxy(cliProxy string, cliProxySet bool, env []string) (ProxyResolution, bool, error) {
	configPath, err := DefaultConfigPath(env)
	if err != nil {
		return ProxyResolution{}, false, err
	}
	var configProxy string
	if configPath != "" {
		cfg, ok, err := LoadConfig(configPath)
		if err != nil {
			return ProxyResolution{}, false, err
		}
		if ok {
			configProxy = cfg.ProxyURL
		}
	}

	if cliProxySet {
		return ProxyResolution{URL: strings.TrimSpace(cliProxy), Source: "-proxy"}, true, nil
	}
	if configProxy != "" {
		return ProxyResolution{URL: configProxy, Source: ConfigProxySource}, true, nil
	}

	proxyURL, source, ok := ProxyFromEnvironment(env)
	if ok {
		return ProxyResolution{URL: proxyURL, Source: source}, true, nil
	}
	return ProxyResolution{}, false, nil
}

func LoadConfig(path string) (FileConfig, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileConfig{}, false, nil
		}
		return FileConfig{}, false, fmt.Errorf("read config file %s: %w", path, err)
	}
	defer f.Close()

	cfg := FileConfig{}
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripTOMLComment(scanner.Text()))
		if line == "" {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return FileConfig{}, true, fmt.Errorf("parse config file %s:%d: expected key = value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "proxy" {
			return FileConfig{}, true, fmt.Errorf("parse config file %s:%d: unknown key %q", path, lineNo, key)
		}
		if _, ok := seen[key]; ok {
			return FileConfig{}, true, fmt.Errorf("parse config file %s:%d: duplicate key %q", path, lineNo, key)
		}
		seen[key] = struct{}{}

		proxyURL, err := parseTOMLString(value)
		if err != nil {
			return FileConfig{}, true, fmt.Errorf("parse config file %s:%d: %w", path, lineNo, err)
		}
		if strings.TrimSpace(proxyURL) == "" {
			return FileConfig{}, true, fmt.Errorf("parse config file %s:%d: proxy cannot be empty", path, lineNo)
		}
		cfg.ProxyURL = proxyURL
	}
	if err := scanner.Err(); err != nil {
		return FileConfig{}, true, fmt.Errorf("read config file %s: %w", path, err)
	}
	return cfg, true, nil
}

func configHomeDir(env []string) (string, error) {
	identity, err := TargetIdentityFromEnvironment(env)
	if err != nil {
		return "", err
	}
	if identity.Valid && identity.HomeDir != "" {
		return identity.HomeDir, nil
	}

	for _, item := range env {
		key, value, found := strings.Cut(item, "=")
		if found && key == "HOME" {
			return value, nil
		}
	}
	return os.UserHomeDir()
}

func stripTOMLComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if inString && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if !inString && r == '#' {
			return line[:i]
		}
	}
	return line
}

func parseTOMLString(raw string) (string, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("proxy must be a quoted string")
	}

	body := raw[1 : len(raw)-1]
	var b strings.Builder
	escaped := false
	for _, r := range body {
		if escaped {
			switch r {
			case '"', '\\':
				b.WriteRune(r)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", fmt.Errorf("unsupported escape sequence \\%c", r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		return "", fmt.Errorf("unfinished escape sequence")
	}
	return b.String(), nil
}
