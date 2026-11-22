package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountInventory(r *httpx.Router, p string) {
	r.Route("POST "+p+"/inventory/items", a.createInvItem)
	r.Route("GET "+p+"/inventory/items", a.listInvItems)
	r.Route("GET "+p+"/inventory/items/{id}", a.getInvItem)
	r.Route("GET "+p+"/inventory/items/{id}/movements", a.invMovements)
	r.Route("POST "+p+"/inventory/receipts", a.invReceive)
	r.Route("POST "+p+"/inventory/issues", a.invIssue)
	r.Route("POST "+p+"/inventory/counts", a.invCount)
	r.Route("POST "+p+"/inventory/write-downs", a.invWriteDown)
	r.Route("POST "+p+"/inventory/write-down-reversals", a.invReverseWriteDown)
	r.Route("GET "+p+"/inventory/valuation", a.invValuation)
	a.mountInventory2(r, p)
}

type invItemDTO struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Unit     string    `json:"unit"`
	Method   string    `json:"method"`
	Qty      int64     `json:"quantity"`
	Value    int64     `json:"value"`
	UnitCost int64     `json:"unit_cost"`
	Currency string    `json:"currency"`
	Active   bool      `json:"active"`
	FEFO     bool      `json:"fefo,omitempty"`
}

func toInvItem(i inventory.Item) invItemDTO {
	return invItemDTO{i.ID, i.Code, i.Name, i.Unit, string(i.Method), i.Qty, i.Value.Amount(), i.UnitCost(), string(i.Value.Currency()), i.Active, i.FEFO}
}

func (a *API) createInvItem(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		Unit   string `json:"unit"`
		Method string `json:"method"`
		FEFO   bool   `json:"fefo"`
	}](r)
	if err != nil {
		return err
	}
	it, err := a.m.Inventory.CreateItem(r.Context(), inventory.ItemInput{Code: req.Code, Name: req.Name, Unit: req.Unit, Method: inventory.Method(req.Method), FEFO: req.FEFO, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toInvItem(it))
}

func (a *API) listInvItems(w http.ResponseWriter, r *http.Request) error {
	items, err := a.m.Inventory.Items(r.Context())
	if err != nil {
		return err
	}
	out := make([]invItemDTO, len(items))
	for i, it := range items {
		out[i] = toInvItem(it)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getInvItem(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	it, err := a.m.Inventory.Item(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toInvItem(it))
}

type movementDTO struct {
	ID         int64      `json:"id"`
	ItemID     uuid.UUID  `json:"item_id"`
	Kind       string     `json:"kind"`
	Qty        int64      `json:"quantity"`
	Value      int64      `json:"value"`
	QtyAfter   int64      `json:"quantity_after"`
	ValueAfter int64      `json:"value_after"`
	Date       string     `json:"date"`
	DateJalali string     `json:"date_jalali"`
	JournalID  *uuid.UUID `json:"journal_id,omitempty"`
	Ref        string     `json:"ref,omitempty"`
	Note       string     `json:"note,omitempty"`
	Warehouse  *uuid.UUID `json:"warehouse_id,omitempty"`
	LotID      *int64     `json:"lot_id,omitempty"`
}

func toMovement(m inventory.Movement) movementDTO {
	return movementDTO{m.ID, m.ItemID, string(m.Kind), m.Qty, m.Value, m.QtyAfter, m.ValueAfter, m.Date.Format("2006-01-02"), jdate(m.Date), m.Journal, m.Ref, m.Note, m.WarehouseID, m.LotID}
}

func (a *API) invMovements(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ms, err := a.m.Inventory.Movements(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]movementDTO, len(ms))
	for i, m := range ms {
		out[i] = toMovement(m)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) invReceive(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		ItemID         uuid.UUID   `json:"item_id"`
		Qty            int64       `json:"quantity"`
		TotalCost      money.Money `json:"total_cost"`
		VAT            money.Money `json:"vat"`
		Date           string      `json:"date"`
		CounterAccount string      `json:"counter_account"`
		Ref            string      `json:"ref"`
		WarehouseID    uuid.UUID   `json:"warehouse_id"`
		LotNo          string      `json:"lot_no"`
		ExpiresOn      string      `json:"expires_on"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.ReceiveInput{IdemKey: key, ItemID: req.ItemID, Qty: req.Qty, TotalCost: req.TotalCost, VAT: req.VAT, CounterAccount: req.CounterAccount, Ref: req.Ref, Actor: actorOf(r.Context()),
		WarehouseID: req.WarehouseID, LotNo: req.LotNo}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	if req.ExpiresOn != "" {
		d, err := optDay(req.ExpiresOn)
		if err != nil {
			return err
		}
		in.ExpiresOn = &d
	}
	m, err := a.m.Inventory.Receive(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toMovement(m))
}

func (a *API) invIssue(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		ItemID         uuid.UUID `json:"item_id"`
		Qty            int64     `json:"quantity"`
		Date           string    `json:"date"`
		ExpenseAccount string    `json:"expense_account"`
		Ref            string    `json:"ref"`
		Reason         string    `json:"reason"`
		WarehouseID    uuid.UUID `json:"warehouse_id"`
		LotID          int64     `json:"lot_id"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.IssueInput{IdemKey: key, ItemID: req.ItemID, Qty: req.Qty, ExpenseAccount: req.ExpenseAccount, Ref: req.Ref, Reason: req.Reason, Actor: actorOf(r.Context()),
		WarehouseID: req.WarehouseID, LotID: req.LotID}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	m, err := a.m.Inventory.Issue(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toMovement(m))
}

func (a *API) invCount(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		ItemID     uuid.UUID `json:"item_id"`
		CountedQty int64     `json:"counted_quantity"`
		Date       string    `json:"date"`
		Reason     string    `json:"reason"`
		Warehouse  uuid.UUID `json:"warehouse_id"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.CountInput{IdemKey: key, ItemID: req.ItemID, CountedQty: req.CountedQty, Reason: req.Reason, Actor: actorOf(r.Context()), WarehouseID: req.Warehouse}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	m, err := a.m.Inventory.Count(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toMovement(m))
}

func (a *API) invWriteDown(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		ItemID uuid.UUID   `json:"item_id"`
		NRV    money.Money `json:"net_realizable_value"`
		Date   string      `json:"date"`
		Reason string      `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.WriteDownInput{IdemKey: key, ItemID: req.ItemID, NetRealizableValue: req.NRV, Reason: req.Reason, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	m, err := a.m.Inventory.WriteDown(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toMovement(m))
}

// invValuation is the stock valuation (ارزش‌گذاری موجودی); its total equals the balance of the inventory account.
func (a *API) invValuation(w http.ResponseWriter, r *http.Request) error {
	v, err := a.m.Inventory.Valuation(r.Context())
	if err != nil {
		return err
	}
	out := make([]invItemDTO, len(v.Items))
	for i, it := range v.Items {
		out[i] = toInvItem(it)
	}
	return ok(w, map[string]any{"items": out, "total_value": v.TotalValue})
}

// optDay parses an optional date (empty = today in the service).
func optDay(s string) (t time.Time, err error) {
	if s == "" {
		return t, nil
	}
	if t, err = parseDate(s); err != nil {
		return t, apperr.InvalidField("date", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	return t, nil
}

// invReverseWriteDown raises the carrying value again after the net realizable value rose, up to the write-down still standing.
func (a *API) invReverseWriteDown(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		ItemID uuid.UUID   `json:"item_id"`
		NRV    money.Money `json:"net_realizable_value"`
		Date   string      `json:"date"`
		Reason string      `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.ReverseWriteDownInput{IdemKey: key, ItemID: req.ItemID, NetRealizableValue: req.NRV, Reason: req.Reason, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	m, err := a.m.Inventory.ReverseWriteDown(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toMovement(m))
}
