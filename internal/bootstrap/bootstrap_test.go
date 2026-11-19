package bootstrap_test

import (
	"errors"
	"testing"

	"github.com/gatework/fabric/internal/bootstrap"
)

func TestCredentialsFromEnv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NC_DEVICE_USERNAME": "ops", "NC_DEVICE_PASSWORD": "generic", "NC_CISCO_NXOS_PASSWORD": "specific"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	c, err := bootstrap.CredentialsFromEnv(lookup)
	if err != nil || c.Username != "ops" || c.Password != "specific" {
		t.Fatalf("credentials = %+v %v", c, err)
	}
	if _, err := bootstrap.CredentialsFromEnv(func(string) (string, bool) { return "", false }); !errors.Is(err, bootstrap.ErrCredentials) {
		t.Fatalf("missing credentials: %v", err)
	}
}

func TestReadFilesOptional(t *testing.T) {
	t.Parallel()
	if in, err := bootstrap.ReadIntent(""); in != nil || err != nil {
		t.Fatal("empty intent path must mean none")
	}
	if _, err := bootstrap.ReadIntent("../../testdata/intent.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.ReadPolicy("missing.yaml"); err == nil {
		t.Fatal("missing policy accepted")
	}
}
