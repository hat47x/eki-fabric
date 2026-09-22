package decisiontrust

import (
	"errors"
	"fmt"
)

const (
	COSESign1Tag uint64 = 18

	COSEHeaderLabelAlgorithm  int64 = 1
	COSEHeaderLabelCritical   int64 = 2
	COSEHeaderLabelKeyID      int64 = 4
	COSEHeaderLabelEKIProfile int64 = -70000

	SignedDecisionProfileV1 = "eki.signed-decision/v1"

	MaxCOSESign1Bytes       = 256 * 1024
	MaxProtectedHeaderBytes = 4 * 1024
	MaxKeyIDBytes           = 128
)

var ErrInvalidCOSEProfile = errors.New("invalid EKI signed-decision COSE profile")

type ProtectedHeaderProfile struct {
	AlgorithmIdentifier int64
	CriticalLabels      []int64
	KeyID               []byte
	Profile             string
}

func (h ProtectedHeaderProfile) Validate() (Algorithm, KeyID, error) {
	algorithm, ok := AlgorithmFromCOSEIdentifier(h.AlgorithmIdentifier)
	if !ok {
		return "", "", fmt.Errorf(
			"%w: unsupported algorithm identifier %d",
			ErrInvalidCOSEProfile,
			h.AlgorithmIdentifier,
		)
	}

	if len(h.CriticalLabels) != 1 || h.CriticalLabels[0] != COSEHeaderLabelEKIProfile {
		return "", "", fmt.Errorf(
			"%w: crit must contain only the EKI profile label",
			ErrInvalidCOSEProfile,
		)
	}
	if h.Profile != SignedDecisionProfileV1 {
		return "", "", fmt.Errorf(
			"%w: profile %q is unsupported",
			ErrInvalidCOSEProfile,
			h.Profile,
		)
	}
	if len(h.KeyID) == 0 || len(h.KeyID) > MaxKeyIDBytes {
		return "", "", fmt.Errorf(
			"%w: kid must contain 1..%d bytes",
			ErrInvalidCOSEProfile,
			MaxKeyIDBytes,
		)
	}
	if err := validateText("key ID", string(h.KeyID), MaxKeyIDBytes); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidCOSEProfile, err)
	}

	return algorithm, KeyID(string(h.KeyID)), nil
}
