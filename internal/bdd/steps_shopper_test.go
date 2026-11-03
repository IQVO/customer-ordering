package bdd

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// savedAddressBody is the ShipTo of the save-address scenarios, spelled as
// openapi.yaml's ShipTo.
const savedAddressBody = `{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US","phone":"+1 555 0100"}`

// registerShopperExt wires the steps of the NG1-NG3 scenarios: saved
// addresses, the shopper reads and the order history.
func registerShopperExt(sc *godog.ScenarioContext, w *world) {
	registerSavedAddressSteps(sc, w)
	registerShopperLookupSteps(sc, w)
	registerOrderHistorySteps(sc, w)
}

// registerSavedAddressSteps covers AddSavedAddress and GetShopper.
func registerSavedAddressSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the shopper (?:saves|saved) an address with Idempotency-Key "([^"]*)"$`, w.saveAddressOfShopper1)
	sc.Step(`^the shopper "([^"]*)" saves an address with Idempotency-Key "([^"]*)"$`, w.saveAddress)
	sc.Step(`^the shopper saved 20 addresses$`, w.saveTwentyAddresses)
	sc.Step(`^the saved address has an addressId$`, w.savedAddressHasAnID)
	sc.Step(`^the shopper profile lists (\d+) saved address(?:es)?$`, w.profileListsAddresses)
}

// registerShopperLookupSteps covers FindShopperByEmail and the checkout by a
// saved address.
func registerShopperLookupSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the API looks up the shopper with e-mail "([^"]*)"$`, w.lookupShopperByEmail)
	sc.Step(`^the shopper checks out a cart of 3 lines in 2 groups by saved address "([^"]*)"$`, w.checkoutBySavedAddress)
	sc.Step(`^the stored order ships to the saved address$`, w.storedOrderShipsToTheSavedAddress)
}

// registerOrderHistorySteps covers ListOrdersByShopper and its paging.
func registerOrderHistorySteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the shopper places order "([^"]*)" at minute (\d+)$`, func(id string, minute int) error {
		return w.placeOrderAs(id, "shopper-1", minute)
	})
	sc.Step(`^the shopper "([^"]*)" places order "([^"]*)" at minute (\d+)$`, func(shopper, id string, minute int) error {
		return w.placeOrderAs(id, shopper, minute)
	})
	sc.Step(`^the API lists the orders of shopper "([^"]*)"$`, func(shopper string) error {
		return w.listOrders(shopper, "")
	})
	sc.Step(`^the API lists the orders of shopper "([^"]*)" with limit (\d+)$`, func(shopper string, limit int) error {
		return w.listOrders(shopper, fmt.Sprintf("&limit=%d", limit))
	})
	sc.Step(`^the history lists the orders newest first:$`, w.historyLists)
	sc.Step(`^the history page has a next cursor$`, w.historyHasACursor)
	sc.Step(`^the history page has no next cursor$`, w.historyHasNoCursor)
	sc.Step(`^the API follows the history cursor$`, w.followHistoryCursor)
}

func (w *world) saveAddressOfShopper1(key string) error { return w.saveAddress("shopper-1", key) }

func (w *world) saveTwentyAddresses() error {
	for i := 0; i < 20; i++ {
		if err := w.saveAddress("shopper-1", fmt.Sprintf("addr-%d", i)); err != nil {
			return err
		}
		if w.apiStatus != 201 {
			return fmt.Errorf("address %d = %d %s", i+1, w.apiStatus, w.apiBody)
		}
	}
	return nil
}

func (w *world) savedAddressHasAnID() error {
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	if id, _ := m["addressId"].(string); id == "" {
		return fmt.Errorf("no addressId in %s", truncate(w.apiBody))
	}
	return nil
}

func (w *world) profileListsAddresses(want int) error {
	if err := w.call("GET", "/shoppers/shopper-1", "", ""); err != nil {
		return err
	}
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	list, _ := m["savedAddresses"].([]any)
	if len(list) != want {
		return fmt.Errorf("profile lists %d addresses, want %d (%s)", len(list), want, truncate(w.apiBody))
	}
	return nil
}

func (w *world) lookupShopperByEmail(email string) error {
	return w.call("GET", "/shoppers?email="+url.QueryEscape(email), "", "")
}

func (w *world) checkoutBySavedAddress(key string) error {
	id, found := w.addressIDs[key]
	if !found {
		return fmt.Errorf("no address saved under key %q", key)
	}
	body := fmt.Sprintf(`{"shopperId":"shopper-1","shipToAddressId":%q,"lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1},{"lineNo":3,"catalogItemId":"sku-300","quantity":5}],`+
		`"groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1,2]},{"groupNo":2,"offerId":"offer-2","lineNos":[3]}]}`, id)
	if err := w.call("POST", "/checkout", "saved-address", body); err != nil {
		return err
	}
	w.orderID = "order-saved-address" // the order id derives from the key
	return nil
}

func (w *world) storedOrderShipsToTheSavedAddress() error {
	if err := w.call("GET", "/orders/"+w.orderID+"/ship-to", "", ""); err != nil {
		return err
	}
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	if w.apiStatus != 200 || m["line1"] != "1 Sample Street" || m["postalCode"] != "78701" {
		return fmt.Errorf("ship-to = %d %s", w.apiStatus, truncate(w.apiBody))
	}
	return nil
}

func (w *world) historyLists(doc *godog.DocString) error {
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	items, _ := m["items"].([]any)
	var want []string
	for _, line := range strings.Split(doc.Content, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			want = append(want, line)
		}
	}
	if len(items) != len(want) {
		return fmt.Errorf("history holds %d orders, want %d (%s)", len(items), len(want), truncate(w.apiBody))
	}
	for i, id := range want {
		item, _ := items[i].(map[string]any)
		if item["orderId"] != id {
			return fmt.Errorf("history[%d] = %v, want %s", i, item["orderId"], id)
		}
	}
	return nil
}

func (w *world) historyHasACursor() error {
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	if cursor, _ := m["nextCursor"].(string); cursor == "" {
		return fmt.Errorf("no nextCursor in %s", truncate(w.apiBody))
	}
	return nil
}

func (w *world) historyHasNoCursor() error {
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	if m["nextCursor"] != nil {
		return fmt.Errorf("unexpected nextCursor in %s", truncate(w.apiBody))
	}
	return nil
}

func (w *world) followHistoryCursor() error {
	m, err := w.apiJSON()
	if err != nil {
		return err
	}
	cursor, _ := m["nextCursor"].(string)
	if cursor == "" {
		return fmt.Errorf("no cursor to follow in %s", truncate(w.apiBody))
	}
	return w.call("GET", "/orders?shopperId="+url.QueryEscape(w.historyShopper)+"&cursor="+url.QueryEscape(cursor), "", "")
}

// listOrders requests one history page of the shopper, with an extra query
// (the limit) when given.
func (w *world) listOrders(shopper, extra string) error {
	w.historyShopper = shopper
	return w.call("GET", "/orders?shopperId="+url.QueryEscape(shopper)+extra, "", "")
}

// saveAddress posts the add-address route and remembers the id under its key.
func (w *world) saveAddress(shopper, key string) error {
	if err := w.call("POST", "/shoppers/"+shopper+"/addresses", key, savedAddressBody); err != nil {
		return err
	}
	if w.apiStatus == 201 {
		m, err := w.apiJSON()
		if err != nil {
			return err
		}
		w.addressIDs[key] = m["addressId"].(string)
	}
	return nil
}

// placeOrderAs checks a cart out at a chosen minute, as a chosen shopper, so
// the history has distinct placedAt values.
func (w *world) placeOrderAs(id, shopper string, minute int) error {
	w.clock.Set(t0.Add(time.Duration(minute) * time.Minute))
	in := w.cart("78701", twoGroups()...)
	in.OrderID = customerorder.ID(id)
	in.ShopperID = shopper
	w.orderID = id
	w.checkout(in)
	return w.lastErr
}
