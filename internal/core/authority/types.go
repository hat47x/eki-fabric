package authority

import "fmt"

type Role string

const (
	RoleExecutorAdmission      Role = "EXE-1"
	RolePlacementAuthorization Role = "PLC-1"
	RoleLease                  Role = "LSE-1"
	RoleDataCapability         Role = "DAT-1"
	RoleWorkOrder              Role = "WOR-1"
)

func (r Role) Valid() bool {
	switch r {
	case RoleExecutorAdmission, RolePlacementAuthorization, RoleLease, RoleDataCapability, RoleWorkOrder:
		return true
	default:
		return false
	}
}

type DecisionKind string

const (
	DecisionPlacementApproval  DecisionKind = "eki.placement-approval/v1"
	DecisionLease              DecisionKind = "eki.lease/v1"
	DecisionExecutorApproval   DecisionKind = "eki.executor-approval/v1"
	DecisionArtifactReadGrant  DecisionKind = "eki.artifact-read-grant/v1"
	DecisionArtifactWriteGrant DecisionKind = "eki.artifact-write-grant/v1"
	DecisionWorkOrder          DecisionKind = "eki.work-order/v1"
)

func (k DecisionKind) Valid() bool {
	_, ok := ExpectedRole(k)
	return ok
}

func ExpectedRole(kind DecisionKind) (Role, bool) {
	switch kind {
	case DecisionExecutorApproval:
		return RoleExecutorAdmission, true
	case DecisionPlacementApproval:
		return RolePlacementAuthorization, true
	case DecisionLease:
		return RoleLease, true
	case DecisionArtifactReadGrant, DecisionArtifactWriteGrant:
		return RoleDataCapability, true
	case DecisionWorkOrder:
		return RoleWorkOrder, true
	default:
		return "", false
	}
}

func ValidateRoleForDecision(role Role, kind DecisionKind) error {
	expected, ok := ExpectedRole(kind)
	if !ok {
		return fmt.Errorf("unknown decision kind %q", kind)
	}
	if role != expected {
		return fmt.Errorf("decision kind %q requires authority role %q, got %q", kind, expected, role)
	}
	return nil
}
