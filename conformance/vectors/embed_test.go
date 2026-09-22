package vectors

import "testing"

func TestSignedDecisionCOSEV1ReturnsDefensiveCopy(t *testing.T) {
	first := SignedDecisionCOSEV1()
	if len(first) == 0 {
		t.Fatal("SignedDecisionCOSEV1() returned empty fixture")
	}
	original := first[0]
	first[0] ^= 0xff

	second := SignedDecisionCOSEV1()
	if len(second) == 0 {
		t.Fatal("second SignedDecisionCOSEV1() returned empty fixture")
	}
	if second[0] != original {
		t.Fatal("caller mutation changed embedded fixture")
	}
}
