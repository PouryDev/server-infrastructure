package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountInventory2(r *httpx.Router, p string) {
	r.Route("POST "+p+"/inventory/warehouses", a.createWarehouse)
	r.Route("GET "+p+"/inventory/warehouses", a.listWarehouses)
	r.Route("GET "+p+"/inventory/items/{id}/stock", a.itemStock)
	r.Route("POST "+p+"/inventory/transfers", a.invTransfer)
	r.Route("GET "+p+"/inventory/expiring-lots", a.expiringLots)
	r.Route("POST "+p+"/production/overhead-rates", a.addOverheadRate)
	r.Route("GET "+p+"/production/overhead-rates", a.listOverheadRates)
	r.Route("POST "+p+"/production/orders", a.openOrder)
	r.Route("GET "+p+"/production/orders", a.listOrders)
	r.Route("GET "+p+"/production/orders/{id}", a.getOrder)
	r.Route("POST "+p+"/production/orders/{id}/materials", a.orderMaterial)
	r.Route("POST "+p+"/production/orders/{id}/labor", a.orderLabor)
	r.Route("POST "+p+"/production/orders/{id}/complete", a.orderComplete)
	r.Route("POST "+p+"/production/overhead", a.recordOverhead)
	r.Route("POST "+p+"/production/overhead-settlements", a.settleOverhead)
	r.Route("GET "+p+"/production/work-in-process", a.workInProcess)
}

type warehouseDTO struct {
	ID     uuid.UUID `json:"id"`
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

func (a *API) createWarehouse(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}](r)
	if err != nil {
		return err
	}
	x, err := a.m.Inventory.CreateWarehouse(r.Context(), req.Code, req.Name, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, warehouseDTO{x.ID, x.Code, x.Name, x.Active})
}

func (a *API) listWarehouses(w http.ResponseWriter, r *http.Request) error {
	ws, err := a.m.Inventory.Warehouses(r.Context())
	if err != nil {
		return err
	}
	out := make([]warehouseDTO, len(ws))
	for i, x := range ws {
		out[i] = warehouseDTO{x.ID, x.Code, x.Name, x.Active}
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) itemStock(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := a.m.Inventory.StockOf(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(rows))
	for i, x := range rows {
		out[i] = map[string]any{"warehouse_id": x.WarehouseID, "quantity": x.Qty}
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) invTransfer(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		ItemID uuid.UUID `json:"item_id"`
		From   uuid.UUID `json:"from_warehouse_id"`
		To     uuid.UUID `json:"to_warehouse_id"`
		Qty    int64     `json:"quantity"`
		LotID  int64     `json:"lot_id"`
		Date   string    `json:"date"`
		Note   string    `json:"note"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.TransferInput{IdemKey: key, ItemID: req.ItemID, From: req.From, To: req.To, Qty: req.Qty, LotID: req.LotID, Note: req.Note, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	m, err := a.m.Inventory.Transfer(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toMovement(m))
}

func (a *API) expiringLots(w http.ResponseWriter, r *http.Request) error {
	day, err := optDay(r.URL.Query().Get("on_or_before"))
	if err != nil {
		return err
	}
	if day.IsZero() {
		return apperr.InvalidField("on_or_before", "required")
	}
	lots, err := a.m.Inventory.ExpiringLots(r.Context(), day)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(lots))
	for i, l := range lots {
		out[i] = map[string]any{"lot_id": l.LotID, "item_id": l.ItemID, "item_code": l.ItemCode, "warehouse_id": l.WarehouseID, "lot_no": l.LotNo,
			"expires_on": l.ExpiresOn.Format("2006-01-02"), "expires_on_jalali": jdate(l.ExpiresOn), "remaining": l.Remaining, "value": l.Value}
	}
	return ok(w, map[string]any{"items": out})
}

type overheadRateDTO struct {
	ID            uuid.UUID `json:"id"`
	EffectiveFrom string    `json:"effective_from"`
	Basis         string    `json:"basis"`
	FixedRate     int64     `json:"fixed_rate"`
	VariableRate  int64     `json:"variable_rate"`
	BudgetFixed   int64     `json:"budget_fixed,omitempty"`
	NormalCap     int64     `json:"normal_capacity,omitempty"`
	Verified      bool      `json:"verified"`
	Source        string    `json:"source"`
}

func toRate(x inventory.OverheadRate) overheadRateDTO {
	return overheadRateDTO{x.ID, x.EffectiveFrom.Format("2006-01-02"), string(x.Basis), x.FixedRate, x.VariableRate, x.BudgetFixed, x.NormalCap, x.Verified, x.Source}
}

// addOverheadRate stores a version of the overhead allocation rates. The rate is in basis points of the base for the cost bases and rials per unit for the quantity bases;
// fixed_rate may be left out when budget_fixed and normal_capacity are given (standard 8 para 11).
func (a *API) addOverheadRate(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[overheadRateDTO](r)
	if err != nil {
		return err
	}
	from, err := optDay(req.EffectiveFrom)
	if err != nil {
		return err
	}
	x, err := a.m.Inventory.AddOverheadRate(r.Context(), inventory.OverheadRate{EffectiveFrom: from, Basis: inventory.Basis(req.Basis), FixedRate: req.FixedRate, VariableRate: req.VariableRate,
		BudgetFixed: req.BudgetFixed, NormalCap: req.NormalCap, Verified: req.Verified, Source: req.Source}, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, toRate(x))
}

func (a *API) listOverheadRates(w http.ResponseWriter, r *http.Request) error {
	rs, err := a.m.Inventory.OverheadRates(r.Context())
	if err != nil {
		return err
	}
	out := make([]overheadRateDTO, len(rs))
	for i, x := range rs {
		out[i] = toRate(x)
	}
	return ok(w, map[string]any{"items": out})
}

type orderDTO struct {
	ID            uuid.UUID `json:"id"`
	Code          string    `json:"code"`
	ProductItemID uuid.UUID `json:"product_item_id"`
	PlannedQty    int64     `json:"planned_quantity"`
	Status        string    `json:"status"`
	MaterialCost  int64     `json:"material_cost"`
	LaborCost     int64     `json:"labor_cost"`
	FixedOverhead int64     `json:"fixed_overhead"`
	VarOverhead   int64     `json:"variable_overhead"`
	OutputQty     int64     `json:"output_quantity"`
	AbnormalLoss  int64     `json:"abnormal_loss"`
	FinishedCost  int64     `json:"finished_cost"`
	WIP           int64     `json:"work_in_process"`
	CompletedOn   string    `json:"completed_on,omitempty"`
}

func toOrder(o inventory.Order) orderDTO {
	d := orderDTO{o.ID, o.Code, o.ProductItemID, o.PlannedQty, string(o.Status), o.MaterialCost, o.LaborCost, o.FixedOverhead, o.VarOverhead, o.OutputQty, o.AbnormalLoss, o.FinishedCost, o.WIP(), ""}
	if o.CompletedOn != nil {
		d.CompletedOn = o.CompletedOn.Format("2006-01-02")
	}
	return d
}

func (a *API) openOrder(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code          string    `json:"code"`
		ProductItemID uuid.UUID `json:"product_item_id"`
		PlannedQty    int64     `json:"planned_quantity"`
	}](r)
	if err != nil {
		return err
	}
	o, err := a.m.Inventory.OpenOrder(r.Context(), req.Code, req.ProductItemID, req.PlannedQty, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, toOrder(o))
}

func (a *API) listOrders(w http.ResponseWriter, r *http.Request) error {
	os, err := a.m.Inventory.Orders(r.Context(), inventory.OrderStatus(r.URL.Query().Get("status")))
	if err != nil {
		return err
	}
	out := make([]orderDTO, len(os))
	for i, o := range os {
		out[i] = toOrder(o)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	o, err := a.m.Inventory.Order(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toOrder(o))
}

func (a *API) orderMaterial(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		ItemID      uuid.UUID `json:"item_id"`
		Qty         int64     `json:"quantity"`
		WarehouseID uuid.UUID `json:"warehouse_id"`
		LotID       int64     `json:"lot_id"`
		Date        string    `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.IssueMaterialInput{IdemKey: key, ItemID: req.ItemID, Qty: req.Qty, WarehouseID: req.WarehouseID, LotID: req.LotID, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	m, err := a.m.Inventory.IssueMaterial(r.Context(), id, in)
	if err != nil {
		return err
	}
	return created(w, toMovement(m))
}

func (a *API) orderLabor(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount         money.Money `json:"amount"`
		CounterAccount string      `json:"counter_account"`
		Date           string      `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.AddLaborInput{IdemKey: key, Amount: req.Amount, CounterAccount: req.CounterAccount, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	if err := a.m.Inventory.AddLabor(r.Context(), id, in); err != nil {
		return err
	}
	o, err := a.m.Inventory.Order(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toOrder(o))
}

func (a *API) orderComplete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		OutputQty    int64     `json:"output_quantity"`
		MachineUnits int64     `json:"machine_units"`
		AbnormalLoss int64     `json:"abnormal_loss"`
		Date         string    `json:"date"`
		WarehouseID  uuid.UUID `json:"warehouse_id"`
		LotNo        string    `json:"lot_no"`
		ExpiresOn    string    `json:"expires_on"`
		NoOverhead   bool      `json:"no_overhead"`
	}](r)
	if err != nil {
		return err
	}
	in := inventory.CompleteInput{IdemKey: key, OutputQty: req.OutputQty, MachineUnits: req.MachineUnits, AbnormalLoss: req.AbnormalLoss, WarehouseID: req.WarehouseID, LotNo: req.LotNo,
		NoOverhead: req.NoOverhead, Actor: actorOf(r.Context())}
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
	o, err := a.m.Inventory.CompleteOrder(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, toOrder(o))
}

func (a *API) recordOverhead(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount         money.Money `json:"amount"`
		CounterAccount string      `json:"counter_account"`
		Ref            string      `json:"ref"`
		Date           string      `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	day, err := optDay(req.Date)
	if err != nil {
		return err
	}
	if err := a.m.Inventory.RecordOverhead(r.Context(), key, req.Amount, req.CounterAccount, req.Ref, day, actorOf(r.Context())); err != nil {
		return err
	}
	return created(w, map[string]any{"recorded": req.Amount.Amount()})
}

func (a *API) settleOverhead(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Date string `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	day, err := optDay(req.Date)
	if err != nil {
		return err
	}
	st, err := a.m.Inventory.SettleOverhead(r.Context(), key, day, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"actual": st.Actual, "applied": st.Applied, "variance": st.Actual - st.Applied, "journal_id": st.Journal})
}

func (a *API) workInProcess(w http.ResponseWriter, r *http.Request) error {
	v, err := a.m.Inventory.WorkInProcess(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"work_in_process": v})
}
