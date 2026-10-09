package suite_test

import (
	"os"
	"path/filepath"
	"runtime"
)

type testEnv struct {
	host      string
	edition   string
	binary    string
	apiURL    string
	user      string
	password  string
	tokenFile string
}

func loadEnv() *testEnv {
	host := os.Getenv("NIPA_TEST_HOST")
	if host == "" {
		return nil
	}

	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	tokenFile := os.Getenv("NIPA_TOKEN_FILE")
	if tokenFile == "" {
		f, err := os.CreateTemp("", "nipa-e2e-tokens-*.json")
		if err != nil {
			panic(err)
		}
		tokenFile = f.Name()
		_ = f.Close()
	}
	_ = os.Setenv("NIPA_TOKEN_FILE", tokenFile)

	return &testEnv{
		host:      host,
		edition:   envOr("NIPA_TEST_EDITION", "free"),
		binary:    envOr("NIPA_TEST_BINARY", filepath.Join(root, "bin", "nipa")),
		apiURL:    envOr("NIPA_TEST_API_URL", "http://"+host+"/api/v1"),
		user:      envOr("NIPA_TEST_USER", "nipa"),
		password:  envOr("NIPA_TEST_PASS", "nipa"),
		tokenFile: tokenFile,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
