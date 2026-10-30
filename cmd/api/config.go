package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/deliverypromise"
	outboxrelay "github.com/claudioed/customer-ordering/internal/adapters/outbound/outbox"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/telemetry"
)

// Boot errors for the variables the context cannot run without. Each message
// names the variable.
var (
	errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")
	errMissingBrokers     = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)")
	errMissingPromiseURL  = errors.New("DELIVERY_PROMISE_URL is required: checkout calls delivery-promise to quote, make and confirm promises")
)

// runtimeEnv is the validated part of the environment that selects the real
// runtime.
type runtimeEnv struct {
	databaseURL            string
	brokers                []string
	deliveryPromiseURL     string
	deliveryPromiseTimeout time.Duration
	// groups maps a consumer's group variable to its value; every consumer
	// always runs, so every entry is present.
	groups map[string]string
}

// loadRuntime validates the environment before anything is opened or bound:
// DATABASE_URL, KAFKA_BROKERS, DELIVERY_PROMISE_URL and every consumer group
// are mandatory. Every error names the variable.
func loadRuntime(env func(string) string) (runtimeEnv, error) {
	rt := runtimeEnv{databaseURL: env("DATABASE_URL"), groups: map[string]string{}}
	if rt.databaseURL == "" {
		return runtimeEnv{}, errMissingDatabaseURL
	}
	if rt.brokers = splitBrokers(env("KAFKA_BROKERS")); len(rt.brokers) == 0 {
		return runtimeEnv{}, errMissingBrokers
	}
	if rt.deliveryPromiseURL = strings.TrimSpace(env("DELIVERY_PROMISE_URL")); rt.deliveryPromiseURL == "" {
		return runtimeEnv{}, errMissingPromiseURL
	}
	rt.deliveryPromiseTimeout = deliverypromise.DefaultTimeout
	if raw := env("DELIVERY_PROMISE_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return runtimeEnv{}, fmt.Errorf("DELIVERY_PROMISE_TIMEOUT: %q is not a positive Go duration", raw)
		}
		rt.deliveryPromiseTimeout = d
	}
	for _, spec := range consumerSpecs() {
		group := env(spec.groupEnv)
		if group == "" {
			return runtimeEnv{}, fmt.Errorf("%s is required: the consumer always runs on real Kafka", spec.groupEnv)
		}
		rt.groups[spec.groupEnv] = group
	}
	return rt, nil
}

// splitBrokers splits KAFKA_BROKERS on commas, trimming blanks.
func splitBrokers(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func joinBrokers(brokers []string) string { return strings.Join(brokers, ",") }

// parseRelayInterval reads OUTBOX_RELAY_INTERVAL (Go duration or plain
// seconds), defaulting when unset, malformed or non-positive.
func parseRelayInterval(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return outboxrelay.DefaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid OUTBOX_RELAY_INTERVAL; using the default", "value", raw, "default", outboxrelay.DefaultInterval)
		return outboxrelay.DefaultInterval
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: how many times an outbox row may
// fail before the relay excludes it from claims. Unset = the fleet default (20);
// 0 = unlimited; negative or unparsable values log a warning and use the
// default, so a typo never silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY ("0" disables; unset, negative
// or unparsable = the default).
func shutdownDrainDelay(logger *slog.Logger) time.Duration {
	raw := os.Getenv("SHUTDOWN_DRAIN_DELAY")
	if raw == "" {
		return DefaultShutdownDrainDelay
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", DefaultShutdownDrainDelay.String())
		return DefaultShutdownDrainDelay
	}
	return d
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (case-insensitive; "warning" is accepted like "warn" fleet-wide). The handler
// is wrapped so a record written with a span-carrying context (the *Context
// slog variants) also carries trace_id and span_id.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
