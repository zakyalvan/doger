package doger_test

import (
	"testing"

	"github.com/kartaladev/doger"
	"github.com/kartaladev/doger/eventtransporttest"
)

func TestMemoryTransport_Conformance(t *testing.T) {
	t.Parallel()

	eventtransporttest.Run(t, func(t *testing.T) doger.EventTransport {
		return doger.NewMemoryEventTransport(doger.MemoryLogger(t.Logf))
	})
}
