//go:build integration

package outbox_test

import (
	"os"
	"testing"

	"github.com/claudioed/customer-ordering/internal/testing/kafkatest"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	kafkatest.Shutdown()
	os.Exit(code)
}
