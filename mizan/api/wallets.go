package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
)

func notFound(entity string) error { return apperr.NotFound(entity) }

func (a *API) mountWallets(r *httpx.Router, p string) {
	r.Route("POST "+p+"/wallets", a.openWallet)
	r.Route("GET "+p+"/wallets", a.walletsByOwner)
	r.Route("GET "+p+"/wallets/{id}", func(w http.ResponseWriter, r *http.Request) error { return a.getWallet(w, r, "") })
	r.Route("GET "+p+"/wallets/{id}/statement", func(w http.ResponseWriter, r *http.Request) error { return a.walletStatement(w, r, "") })
	r.Route("POST "+p+"/wallets/{id}/credit", func(w http.ResponseWriter, r *http.Request) error { return a.adjust(w, r, true) })
	r.Route("POST "+p+"/wallets/{id}/debit", func(w http.ResponseWriter, r *http.Request) error { return a.adjust(w, r, false) })
	r.Route("POST "+p+"/wallets/{id}/status", a.walletStatus)
	r.Route("POST "+p+"/wallets/{id}/topups", func(w http.ResponseWriter, r *http.Request) error { return a.topUp(w, r, "") })
	r.Route("POST "+p+"/wallet-transfers", a.transfer)
}

func (a *API) openWallet(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		OwnerRef string         `json:"owner_ref"`
		Currency money.Currency `json:"currency"`
	}](r)
	if err != nil {
		return err
	}
	if req.Currency == "" {
		req.Currency = money.IRR
	}
	wl, err := a.m.Wallets.Open(r.Context(), req.OwnerRef, req.Currency)
	if err != nil {
		return err
	}
	return created(w, toWallet(wl, nil))
}

func (a *API) walletsByOwner(w http.ResponseWriter, r *http.Request) error {
	owner := r.URL.Query().Get("owner_ref")
	if owner == "" {
		return apperr.InvalidField("owner_ref", "required")
	}
	list, err := a.m.Wallets.ByOwner(r.Context(), owner)
	if err != nil {
		return err
	}
	out := make([]walletDTO, len(list))
	for i, x := range list {
		bal, err := a.m.Wallets.Balance(r.Context(), x.ID)
		if err != nil {
			return err
		}
		out[i] = toWallet(x, &bal)
	}
	return ok(w, out)
}

func (a *API) wallet(r *http.Request, owner string) (wallet.Wallet, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return wallet.Wallet{}, err
	}
	wl, err := a.m.Wallets.Get(r.Context(), id)
	if err != nil {
		return wl, err
	}
	if owner != "" && wl.OwnerRef != owner {
		return wallet.Wallet{}, notFound("wallet")
	}
	return wl, nil
}

func (a *API) getWallet(w http.ResponseWriter, r *http.Request, owner string) error {
	wl, err := a.wallet(r, owner)
	if err != nil {
		return err
	}
	bal, err := a.m.Wallets.Balance(r.Context(), wl.ID)
	if err != nil {
		return err
	}
	return ok(w, toWallet(wl, &bal))
}

func (a *API) walletStatement(w http.ResponseWriter, r *http.Request, owner string) error {
	wl, err := a.wallet(r, owner)
	if err != nil {
		return err
	}
	// The statement of a wallet is the statement of its ledger account: one source of truth.
	r.SetPathValue("code", wl.AccountCode)
	return a.statement(w, r)
}

func (a *API) adjust(w http.ResponseWriter, r *http.Request, credit bool) error {
	wl, err := a.wallet(r, "")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount      money.Money `json:"amount"`
		Description string      `json:"description"`
	}](r)
	if err != nil {
		return err
	}
	jid, err := a.m.Wallets.Adjust(r.Context(), wallet.Movement{WalletID: wl.ID, Amount: req.Amount, Key: "adjust:" + wl.ID.String() + ":" + key,
		Description: req.Description, RefType: "wallet_adjustment", RefID: key, Actor: actorOf(r.Context())}, credit)
	if err != nil {
		return err
	}
	bal, err := a.m.Wallets.Balance(r.Context(), wl.ID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"journal_id": jid, "balance": bal})
}

func (a *API) walletStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Status wallet.Status `json:"status"`
	}](r)
	if err != nil {
		return err
	}
	if err := a.m.Wallets.SetStatus(r.Context(), id, req.Status, actorOf(r.Context())); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (a *API) topUp(w http.ResponseWriter, r *http.Request, owner string) error {
	wl, err := a.wallet(r, owner)
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount    money.Money `json:"amount"`
		Gateway   string      `json:"gateway"`
		ReturnURL string      `json:"return_url"`
	}](r)
	if err != nil {
		return err
	}
	p, err := a.m.StartWalletTopUp(r.Context(), mizan.TopUpInput{WalletID: wl.ID, Amount: req.Amount, Gateway: req.Gateway, ReturnURL: req.ReturnURL, IdemKey: key, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toPayment(p))
}

func (a *API) transfer(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		From        uuid.UUID   `json:"from"`
		To          uuid.UUID   `json:"to"`
		Amount      money.Money `json:"amount"`
		Description string      `json:"description"`
	}](r)
	if err != nil {
		return err
	}
	jid, err := a.m.Wallets.Transfer(r.Context(), req.From, req.To, req.Amount, key, req.Description, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, map[string]any{"journal_id": jid})
}
