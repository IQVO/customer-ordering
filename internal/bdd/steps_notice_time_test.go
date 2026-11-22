package bdd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// noticesCarryTheirDecisionTime encodes every ShopperNoticeDecided the use
// cases published through the REAL encoder and reads the CloudEvents `time` off
// the wire: it must be the notice's own decidedAt, never the zero instant.
func (w *world) noticesCarryTheirDecisionTime(n int) error {
	list := w.published("ShopperNoticeDecided")
	if len(list) != n {
		return fmt.Errorf("%d ShopperNoticeDecided events, want %d", len(list), n)
	}
	for i, ev := range list {
		msgs, err := kafka.NewEncoder().Encode(ev.(shared.Event))
		if err != nil {
			return err
		}
		var wire struct {
			Time time.Time `json:"time"`
			Data struct {
				DecidedAt time.Time `json:"decidedAt"`
			} `json:"data"`
		}
		if err := json.Unmarshal(msgs[0].Value, &wire); err != nil {
			return err
		}
		if wire.Time.IsZero() || wire.Time.Year() < 2000 || !wire.Time.Equal(wire.Data.DecidedAt) {
			return fmt.Errorf("notice %d: CloudEvents time = %s, decidedAt = %s, want the decision time on both", i+1, wire.Time.Format(time.RFC3339Nano), wire.Data.DecidedAt.Format(time.RFC3339Nano))
		}
	}
	return nil
}

func registerNoticeTime(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the (\d+) published ShopperNoticeDecided events? carr(?:y|ies) the CloudEvents time of the decision$`, w.noticesCarryTheirDecisionTime)
}
