package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
)

func TestRun_EachOrderInvoicedOnce(t *testing.T) {
	invoices, err := run(context.Background(), 4)
	require.NoError(t, err)

	require.Len(t, invoices, 4)
	instances := map[string]int{}
	for order, by := range invoices {
		assert.Len(t, by, 1, "order %s invoiced by %v, want one instance", order, by)
		instances[by[0]]++
	}
	assert.Len(t, instances, 2, "invoices by instance %v, want both instances to share the orders", instances)
}

// errNoBroker is the error of failingTransport's Subscribe.
var errNoBroker = errors.New("no broker")

// failingTransport sends nothing anywhere and refuses every subscription.
type failingTransport struct{}

func (failingTransport) Send(context.Context, doger.Envelope) error { return nil }

func (failingTransport) Subscribe(context.Context, doger.Subscription, doger.EnvelopeHandler) (func(context.Context) error, error) {
	return nil, errNoBroker
}

func TestNewBillingInstance_StartFailureShutsDown(t *testing.T) {
	injector, err := newBillingInstance(failingTransport{}, "billing-1", NewLedger(1))

	require.ErrorIs(t, err, errNoBroker)
	require.NotNil(t, injector)
	require.ErrorIs(t, doger.Publish(context.Background(), injector, OrderCreatedEvent{OrderID: "o1"}), doger.ErrPublishAfterShutdown,
		"the container whose Start failed is shut down")
}

func TestLedger_RecordAfterDone(t *testing.T) {
	ledger := NewLedger(1)
	ledger.Record("o1", "billing-1")
	<-ledger.Done()

	require.NotPanics(t, func() { ledger.Record("o1", "billing-2") }, "a redelivered order after Done")
	assert.Equal(t, map[string][]string{"o1": {"billing-1", "billing-2"}}, ledger.Invoices())
}
