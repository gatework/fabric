package synth_test

import (
	"os"
	"testing"
)

func mkdirTemp() (string, error) { return os.MkdirTemp("", "fabric-synth-*") }

func TestMain(m *testing.M) {
	code := m.Run()
	if thousandDir != "" {
		_ = os.RemoveAll(thousandDir)
	}
	os.Exit(code)
}
