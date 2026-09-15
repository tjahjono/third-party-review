package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDotEnvLine(t *testing.T) {
	cases := []struct {
		line      string
		key, want string
		ok        bool
	}{
		{`APP_ENV=development`, "APP_ENV", "development", true},
		{`  APP_ENV = development  `, "APP_ENV", "development", true},
		{`export APP_ENV=development`, "APP_ENV", "development", true},
		// The shipped .env.example documents most settings with a trailing
		// comment, so this is the common case, not an edge one.
		{`AI_BATCH_SIZE=8               # answers per call`, "AI_BATCH_SIZE", "8", true},
		{`AI_MODEL="qwen2.5:32b"`, "AI_MODEL", "qwen2.5:32b", true},
		{`AI_MODEL='qwen2.5:32b'`, "AI_MODEL", "qwen2.5:32b", true},
		// A '#' inside a quoted value is data, not a comment.
		{`SESSION_SECRET="abc#def"`, "SESSION_SECRET", "abc#def", true},
		// A URL's fragment must survive, because it is not preceded by a space.
		{`AI_BASE_URL=https://host/api#frag`, "AI_BASE_URL", "https://host/api#frag", true},
		{`EMPTY=`, "EMPTY", "", true},
		{`# comment`, "", "", false},
		{``, "", "", false},
		{`   `, "", "", false},
		{`no_equals_here`, "", "", false},
		{`=novalue`, "", "", false},
	}
	for _, c := range cases {
		key, value, ok := parseDotEnvLine(c.line)
		if ok != c.ok {
			t.Errorf("parseDotEnvLine(%q) ok = %v, want %v", c.line, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if key != c.key || value != c.want {
			t.Errorf("parseDotEnvLine(%q) = (%q, %q), want (%q, %q)", c.line, key, value, c.key, c.want)
		}
	}
}

// TestRealEnvironmentWins is the property that matters in a container: the
// orchestrator supplies the environment, and a .env that happened to be baked
// into the image must not override it.
func TestRealEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("TPSA_TEST_A=from_file\nTPSA_TEST_B=from_file\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv("TPSA_TEST_A", "from_environment")
	os.Unsetenv("TPSA_TEST_B")
	t.Cleanup(func() { os.Unsetenv("TPSA_TEST_B") })

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("TPSA_TEST_A"); got != "from_environment" {
		t.Errorf("TPSA_TEST_A = %q; the exported value must win over the file", got)
	}
	if got := os.Getenv("TPSA_TEST_B"); got != "from_file" {
		t.Errorf("TPSA_TEST_B = %q, want the value from the file", got)
	}
}

// A missing .env is the normal case in production and must not be an error.
func TestMissingFileIsNotAnError(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("a missing .env should be ignored, got %v", err)
	}
}

func TestENVFileOverridesSearchPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.env")
	if err := os.WriteFile(path, []byte("TPSA_TEST_C=custom\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("ENV_FILE", path)
	os.Unsetenv("TPSA_TEST_C")
	t.Cleanup(func() { os.Unsetenv("TPSA_TEST_C") })

	if err := LoadDotEnv(); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("TPSA_TEST_C"); got != "custom" {
		t.Errorf("TPSA_TEST_C = %q, want custom", got)
	}
}
