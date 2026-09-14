package handler

import "testing"

// TestContractEnforcedOperations_ExcludeTheWebhook pins the decision that the
// GitHub webhook body is authenticated by its HMAC and never validated by
// shape: enforcing it here would reject deliveries the contract deliberately
// leaves open.
func TestContractEnforcedOperations_ExcludeTheWebhook(t *testing.T) {
	for _, op := range ContractEnforcedOperations() {
		if op.Path == "/api/v1/integrations/github/webhook" {
			t.Fatalf("the webhook body must not be enforced by shape, but %s is in ContractEnforcedOperations", op)
		}
	}
}
