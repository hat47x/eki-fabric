package vectors

import (
	"bytes"
	_ "embed"
)

//go:embed signed-decision-cose-v1.json
var signedDecisionCOSEV1 []byte

func SignedDecisionCOSEV1() []byte {
	return bytes.Clone(signedDecisionCOSEV1)
}
