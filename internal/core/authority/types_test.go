package authority

import "testing"

func TestExpectedRole(t *testing.T) {
	tests := []struct {
		kind DecisionKind
		role Role
	}{
		{DecisionExecutorApproval, RoleExecutorAdmission},
		{DecisionPlacementApproval, RolePlacementAuthorization},
		{DecisionLease, RoleLease},
		{DecisionArtifactReadGrant, RoleDataCapability},
		{DecisionArtifactWriteGrant, RoleDataCapability},
		{DecisionWorkOrder, RoleWorkOrder},
	}
	for _, tt := range tests {
		got, ok := ExpectedRole(tt.kind)
		if !ok {
			t.Fatalf("ExpectedRole(%q) not found", tt.kind)
		}
		if got != tt.role {
			t.Fatalf("ExpectedRole(%q) = %q, want %q", tt.kind, got, tt.role)
		}
		if err := ValidateRoleForDecision(tt.role, tt.kind); err != nil {
			t.Fatalf("ValidateRoleForDecision(%q, %q) error = %v", tt.role, tt.kind, err)
		}
	}
}

func TestValidateRoleForDecisionRejectsCrossAuthorityUse(t *testing.T) {
	if err := ValidateRoleForDecision(RoleWorkOrder, DecisionLease); err == nil {
		t.Fatal("WorkOrder authority must not authorize Lease decisions")
	}
}

func TestUnknownRoleAndDecisionAreInvalid(t *testing.T) {
	if Role("UNKNOWN").Valid() {
		t.Fatal("unknown role unexpectedly valid")
	}
	if DecisionKind("eki.unknown/v1").Valid() {
		t.Fatal("unknown decision kind unexpectedly valid")
	}
}
