package shop

import "time"

type Review struct {
	ID        string    `json:"id"`
	ProductID string    `json:"productId"`
	UserID    string    `json:"-"`
	Author    string    `json:"author"`
	Rating    int       `json:"rating"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}
type ReturnRequest struct {
	ID         string    `json:"id"`
	OrderID    string    `json:"orderId"`
	UserID     string    `json:"-"`
	Reason     string    `json:"reason"`
	Status     string    `json:"status"`
	SellerNote string    `json:"sellerNote"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}
