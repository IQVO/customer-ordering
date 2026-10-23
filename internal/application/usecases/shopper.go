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

// Execute registers the shopper. A duplicate e-mail is shopper.ErrShopperExists.
func (u RegisterShopper) Execute(ctx context.Context, in RegisterShopperInput) (views.Shopper, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.Shopper, error) {
		s, err := shopper.Register(shopper.NewShopper{ID: u.IDs.NewShopperID(), Email: in.Email, SavedAddresses: in.SavedAddresses}, u.now())
		if err != nil {
			return views.Shopper{}, err
		}
		if err := u.Shoppers.Save(ctx, s); err != nil {
			return views.Shopper{}, err
		}
		return views.ShopperFrom(s), nil
	})
}
