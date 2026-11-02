package trustanchor

import (
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

func wireOf(t *testing.T, signed trustbundle.Signed) enrollwire.Bundle {
	t.Helper()
	return enrollwire.EncodeBundle(signed)
}
