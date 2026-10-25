package usecases

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// RegisterShopperInput is the command RegisterShopper.
type RegisterShopperInput struct {
	Email          string
	SavedAddresses []shipto.ShipTo
}

// RegisterShopper registers a synthetic shopper (POST /shoppers).
type RegisterShopper struct{ Deps }

// Execute registers the shopper, minting an id for every registration-time
// address (A24). A duplicate e-mail is shopper.ErrShopperExists.
func (u RegisterShopper) Execute(ctx context.Context, in RegisterShopperInput) (views.Shopper, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.Shopper, error) {
		id := u.IDs.NewShopperID()
		addresses := make([]shopper.SavedAddress, 0, len(in.SavedAddresses))
		for _, a := range in.SavedAddresses {
			addresses = append(addresses, shopper.SavedAddress{ID: u.IDs.NewAddressID(), ShipTo: a})
		}
		s, err := shopper.Register(shopper.NewShopper{ID: id, Email: in.Email, SavedAddresses: addresses}, u.now())
		if err != nil {
			return views.Shopper{}, err
		}
		if err := u.Shoppers.Save(ctx, s); err != nil {
			return views.Shopper{}, err
		}
		return views.ShopperFrom(s), nil
	})
}

// AddSavedAddressInput is the command AddSavedAddress
// (POST /shoppers/{shopperId}/addresses). IdempotencyKey is the request's
// Idempotency-Key header: the address id derives from it (A24).
type AddSavedAddressInput struct {
	ShopperID      string
	Address        shipto.ShipTo
	IdempotencyKey string
}

// AddSavedAddress saves an address on the shopper (NG1).
type AddSavedAddress struct{ Deps }

// Execute adds the address and returns it with its id. An unknown shopper is
// ports.ErrNotFound; the cap of 20 is shopper.ErrTooManyAddresses. A replay of
// the same Idempotency-Key (after the idempotency record expired) derives the
// same address id and returns the existing address unchanged.
func (u AddSavedAddress) Execute(ctx context.Context, in AddSavedAddressInput) (views.SavedAddress, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.SavedAddress, error) {
		s, err := u.Shoppers.Get(ctx, shopper.ID(in.ShopperID))
		if err != nil {
			return views.SavedAddress{}, err
		}
		id := u.IDs.AddressIDFor(s.ID(), in.IdempotencyKey)
		if existing, found := s.Address(id); found {
			return views.SavedAddressFrom(shopper.SavedAddress{ID: id, ShipTo: existing}), nil
		}
		if err := s.AddSavedAddress(id, in.Address); err != nil {
			return views.SavedAddress{}, err
		}
		if err := u.Shoppers.Save(ctx, s); err != nil {
			return views.SavedAddress{}, err
		}
		return views.SavedAddressFrom(shopper.SavedAddress{ID: id, ShipTo: in.Address}), nil
	})
}

// GetShopper is the query GetShopper (GET /shoppers/{shopperId}, NG1).
type GetShopper struct{ Deps }

// Execute returns the shopper with its saved addresses, or ports.ErrNotFound.
func (u GetShopper) Execute(ctx context.Context, id shopper.ID) (views.Shopper, error) {
	s, err := u.Shoppers.Get(ctx, id)
	if err != nil {
		return views.Shopper{}, err
	}
	return views.ShopperFrom(s), nil
}

// FindShopperByEmail is the query FindShopperByEmail (GET /shoppers?email=,
// NG3): the match is trimmed and lower-cased (A16).
type FindShopperByEmail struct{ Deps }

// Execute returns the shopper of the e-mail, or ports.ErrNotFound.
func (u FindShopperByEmail) Execute(ctx context.Context, email string) (views.Shopper, error) {
	s, err := u.Shoppers.FindByEmail(ctx, shopper.NormalizeEmail(email))
	if err != nil {
		return views.Shopper{}, err
	}
	return views.ShopperFrom(s), nil
}
