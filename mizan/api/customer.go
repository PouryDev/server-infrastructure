package api

import (
	"net/http"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
)

// Customer routes act on behalf of the authenticated subject only: the subject of the token is the owner of
// the wallet and the customer of the invoices, never a value from the request.
func (a *API) mountCustomer(r *httpx.Router, p string) {
	r.Route("GET "+p+"/me/wallet", a.myWallet)
	r.Route("GET "+p+"/me/wallet/statement", a.myWalletStatement)
	r.Route("POST "+p+"/me/wallet/topups", a.myTopUp)
	r.Route("GET "+p+"/me/invoices", a.myInvoices)
	r.Route("GET "+p+"/me/invoices/{id}", func(w http.ResponseWriter, r *http.Request) error {
		s, err := subject(r)
		if err != nil {
			return err
		}
		return a.getInvoice(w, r, s)
	})
	r.Route("POST "+p+"/me/invoices/{id}/payments", func(w http.ResponseWriter, r *http.Request) error {
		s, err := subject(r)
		if err != nil {
			return err
		}
		return a.payInvoice(w, r, s)
	})
	r.Route("POST "+p+"/me/invoices/{id}/pay-from-wallet", func(w http.ResponseWriter, r *http.Request) error {
		s, err := subject(r)
		if err != nil {
			return err
		}
		return a.payFromWallet(w, r, s)
	})
}

func subject(r *http.Request) (string, error) {
	c, ok := auth.ClaimsFrom(r.Context())
	if !ok || c.Subject == "" {
		return "", apperr.Unauthorized("authentication required")
	}
	return c.Subject, nil
}

// ownWallet opens (on first use) the subject's wallet in the requested currency (default IRR).
func (a *API) ownWallet(r *http.Request) (wallet.Wallet, error) {
	s, err := subject(r)
	if err != nil {
		return wallet.Wallet{}, err
	}
	cur := money.Currency(r.URL.Query().Get("currency"))
	if cur == "" {
		cur = money.IRR
	}
	return a.m.Wallets.Open(r.Context(), s, cur)
}

func (a *API) myWallet(w http.ResponseWriter, r *http.Request) error {
	wl, err := a.ownWallet(r)
	if err != nil {
		return err
	}
	bal, err := a.m.Wallets.Balance(r.Context(), wl.ID)
	if err != nil {
		return err
	}
	return ok(w, toWallet(wl, &bal))
}

func (a *API) myWalletStatement(w http.ResponseWriter, r *http.Request) error {
	wl, err := a.ownWallet(r)
	if err != nil {
		return err
	}
	r.SetPathValue("code", wl.AccountCode)
	return a.statement(w, r)
}

func (a *API) myTopUp(w http.ResponseWriter, r *http.Request) error {
	wl, err := a.ownWallet(r)
	if err != nil {
		return err
	}
	r.SetPathValue("id", wl.ID.String())
	s, _ := subject(r)
	return a.topUp(w, r, s)
}

func (a *API) myInvoices(w http.ResponseWriter, r *http.Request) error {
	s, err := subject(r)
	if err != nil {
		return err
	}
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	// The customer filter is forced: the client's own customer_ref filters are dropped, the others stay.
	kept := make([]query.Filter, 0, len(params.Filters)+1)
	for _, f := range params.Filters {
		if f.Field != "customer_ref" {
			kept = append(kept, f)
		}
	}
	params.Filters = append(kept, query.Filter{Field: "customer_ref", Op: query.OpEq, Values: []string{s}})
	page, err := a.m.Invoices.List(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, page)
}
