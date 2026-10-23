// Package shopper is the Shopper aggregate: a synthetic customer account with
// saved addresses. State ACTIVE only; the model gives it no lifecycle and no
// events. Invariant: the e-mail is unique (ErrShopperExists, raised by the
// repository on its unique index, ADR 0002).
package shopper

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// ErrShopperExists: the e-mail is already registered (409 shopper-exists).
var ErrShopperExists = errors.New("shopper exists")

// Bounds (ADR 0002, A15). Plain literals.
const (
	// MaxEmail is the longest accepted e-mail address.
	MaxEmail = 254
	// MaxSavedAddresses is the most saved addresses a shopper holds.
	MaxSavedAddresses = 20
)

// ID identifies a shopper (shopperId).
type ID string

// Status is the lifecycle state of a shopper.
type Status string

// StatusActive is the only state of the model.
const StatusActive Status = "ACTIVE"

// Shopper is the aggregate root.
type Shopper struct {
	id           ID
	email        string
	status       Status
	addresses    []shipto.ShipTo
	registeredAt time.Time
	version      int64
}

// Snapshot is the persisted form of a shopper. Restore validates it.
type Snapshot struct {
	ID             ID
	Email          string
	Status         Status
	SavedAddresses []shipto.ShipTo
	RegisteredAt   time.Time
	Version        int64
}

// NewShopper is the input of Register.
type NewShopper struct {
	ID             ID
	Email          string
	SavedAddresses []shipto.ShipTo
}

// NormalizeEmail trims and lower-cases an e-mail (A16).
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func validEmail(e string) bool {
	if e == "" || len(e) > MaxEmail {
		return false
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || a.Name != "" {
		return false
	}
	local, host, found := strings.Cut(e, "@")
	return found && local != "" && strings.Contains(host, ".") && !strings.Contains(host, "@")
}

func validateAddresses(list []shipto.ShipTo) error {
	if len(list) > MaxSavedAddresses {
		return shared.Invalid("savedAddresses holds at most %d addresses", MaxSavedAddresses)
	}
	for i, a := range list {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("savedAddresses[%d]: %w", i, err)
		}
	}
	return nil
}

// Register creates an ACTIVE shopper at version 1 (RegisterShopper). The model
// has no shopper event, so none is raised.
func Register(in NewShopper, now time.Time) (*Shopper, error) {
	if !shared.ValidID(string(in.ID)) {
		return nil, shared.Invalid("shopperId %q is not a valid id", in.ID)
	}
	email := NormalizeEmail(in.Email)
	if !validEmail(email) {
		return nil, shared.Invalid("email is not a valid address of at most %d characters", MaxEmail)
	}
	if err := validateAddresses(in.SavedAddresses); err != nil {
		return nil, err
	}
	return &Shopper{
		id: in.ID, email: email, status: StatusActive, addresses: append([]shipto.ShipTo{}, in.SavedAddresses...),
		registeredAt: shared.UTC(now), version: 1,
	}, nil
}

// Restore rebuilds a persisted shopper, validating every invariant.
func Restore(s Snapshot) (*Shopper, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("shopper %q: %v", s.ID, err)
	}
	return &Shopper{
		id: s.ID, email: s.Email, status: s.Status, addresses: append([]shipto.ShipTo{}, s.SavedAddresses...),
		registeredAt: shared.UTC(s.RegisteredAt), version: s.Version,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) {
		return errors.New("shopperId must be a valid id")
	}
	if s.Email != NormalizeEmail(s.Email) || !validEmail(s.Email) {
		return errors.New("email must be a normalised valid address")
	}
	if s.Status != StatusActive {
		return fmt.Errorf("status %q is not ACTIVE", s.Status)
	}
	if s.RegisteredAt.IsZero() || s.Version < 1 {
		return errors.New("registeredAt and a version of at least 1 are required")
	}
	if err := validateAddresses(s.SavedAddresses); err != nil {
		return err
	}
	return nil
}

// ID returns the shopper id.
func (s *Shopper) ID() ID { return s.id }

// Email returns the normalised e-mail.
func (s *Shopper) Email() string { return s.email }

// Status returns ACTIVE.
func (s *Shopper) Status() Status { return s.status }

// SavedAddresses returns a copy of the saved addresses.
func (s *Shopper) SavedAddresses() []shipto.ShipTo { return append([]shipto.ShipTo{}, s.addresses...) }

// RegisteredAt returns when the shopper registered.
func (s *Shopper) RegisteredAt() time.Time { return s.registeredAt }

// Version returns the aggregate version.
func (s *Shopper) Version() int64 { return s.version }

// Snapshot returns the persisted form.
func (s *Shopper) Snapshot() Snapshot {
	return Snapshot{ID: s.id, Email: s.email, Status: s.status, SavedAddresses: s.SavedAddresses(), RegisteredAt: s.registeredAt, Version: s.version}
}
