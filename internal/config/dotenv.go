package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnv reads a .env file into the process environment before Load runs,
// so `go run ./cmd/server` works with nothing exported by hand.
//
// It is written here rather than pulled in as a dependency because the format
// is a dozen lines of parsing, and the alternative is a third-party package in
// the startup path of an application that handles credentials.
//
// Real environment variables always win. A .env is a developer convenience;
// in a container the orchestrator supplies the environment, and a stale .env
// baked into an image must never silently override it.
func LoadDotEnv(paths ...string) error {
	if len(paths) == 0 {
		paths = defaultDotEnvPaths()
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		err := loadDotEnvFile(p)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	// No .env anywhere is the normal case in production, not an error.
	return nil
}

// defaultDotEnvPaths is where to look, in order: an explicit ENV_FILE, the
// working directory, then one and two levels up. The walk upwards means
// `go run ./cmd/server` finds the .env at the repository root regardless of
// where it was invoked from.
func defaultDotEnvPaths() []string {
	if explicit := strings.TrimSpace(os.Getenv("ENV_FILE")); explicit != "" {
		return []string{explicit}
	}
	return []string{
		".env",
		filepath.Join("..", ".env"),
		filepath.Join("..", "..", ".env"),
	}
}

// loadDotEnvFile parses one file and sets any variable not already present.
func loadDotEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow a long line: a pasted key or a rubric path can exceed the default.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		key, value, ok := parseDotEnvLine(scanner.Text())
		if !ok {
			continue
		}
		// An exported variable wins over the file.
		if _, present := os.LookupEnv(key); present {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// parseDotEnvLine handles the subset of the format that matters: comments,
// blank lines, an optional `export` prefix, quoted values, and a trailing
// comment after an unquoted value.
func parseDotEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	value = strings.TrimSpace(line[eq+1:])
	if key == "" {
		return "", "", false
	}

	switch {
	case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
		// Double quotes: honour the common escapes.
		value = value[1 : len(value)-1]
		value = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(value)
	case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
		// Single quotes: literal.
		value = value[1 : len(value)-1]
	default:
		// Unquoted: strip a trailing comment. `.env.example` documents most
		// settings with one, so this is the common case rather than an edge.
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if i := strings.Index(value, "\t#"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
	}
	return key, value, true
}
