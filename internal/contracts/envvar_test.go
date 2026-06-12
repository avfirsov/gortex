package contracts

import (
	"testing"
)

func TestEnvVarExtractor_GoConsumer(t *testing.T) {
	ext := &EnvVarExtractor{}
	src := []byte(`
package config

func Load() {
	dbHost := os.Getenv("DATABASE_HOST")
	secret, ok := os.LookupEnv("JWT_SECRET")
}
`)
	contracts := ext.Extract("config.go", src, nil, nil)
	if len(contracts) != 2 {
		t.Fatalf("expected 2 consumer contracts, got %d", len(contracts))
	}
	assertContract(t, contracts[0], "env::DATABASE_HOST", ContractEnv, RoleConsumer)
	assertContract(t, contracts[1], "env::JWT_SECRET", ContractEnv, RoleConsumer)
}

func TestEnvVarExtractor_DotEnvProvider(t *testing.T) {
	ext := &EnvVarExtractor{}
	src := []byte(`DATABASE_HOST=localhost
DATABASE_PORT=5432
JWT_SECRET=supersecret
`)
	contracts := ext.Extract("/app/.env", src, nil, nil)
	if len(contracts) != 3 {
		t.Fatalf("expected 3 provider contracts, got %d", len(contracts))
	}
	assertContract(t, contracts[0], "env::DATABASE_HOST", ContractEnv, RoleProvider)
	assertContract(t, contracts[1], "env::DATABASE_PORT", ContractEnv, RoleProvider)
	assertContract(t, contracts[2], "env::JWT_SECRET", ContractEnv, RoleProvider)
}

func TestEnvVarExtractor_TSConsumer(t *testing.T) {
	ext := &EnvVarExtractor{}
	src := []byte(`
const port = process.env.PORT;
const secret = process.env["API_KEY"];
`)
	contracts := ext.Extract("config.ts", src, nil, nil)
	if len(contracts) != 2 {
		t.Fatalf("expected 2 consumer contracts, got %d", len(contracts))
	}
	assertContract(t, contracts[0], "env::PORT", ContractEnv, RoleConsumer)
	assertContract(t, contracts[1], "env::API_KEY", ContractEnv, RoleConsumer)
}
