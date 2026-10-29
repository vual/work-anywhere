package cli

import "testing"

func TestStartupConfigurationIsNotAJSONRPCResponse(t *testing.T) {
	for _, raw := range []string{
		`{"appServerVersion":"0.165.0-alpha.1.2","configRequirements":{"requirements":null}}`,
		`{"appServerVersion":"0.165.0-alpha.1.2","configRequirements":{"requirements":{"application":{"network":{"enabled":true,"domains":{"example.test":"allow"}}}}}}`,
	} {
		if err := validateStartupConfiguration([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"", `{"id":1,"result":{}}`, `{"appServerVersion":"x","configRequirements":{}}`, `{"error":{"code":-1,"message":"denied"}}`} {
		if validateStartupConfiguration([]byte(raw)) == nil {
			t.Fatalf("accepted invalid policy: %s", raw)
		}
	}
}

func TestProtocolOutputIsBounded(t *testing.T) {
	b := &cappedOutput{limit: 4}
	if _, err := b.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("5")); err == nil || b.Len() != 4 {
		t.Fatal("output cap not enforced")
	}
}
