package datamodels

// Order 对应订单表的数据结构。
type Order struct {
	// ID 是订单主键。
	ID int64 `json:"id" sql:"ID"`
	// UserID 是下单用户 ID。
	UserID int64 `json:"userId" sql:"userID"`
	// ProductId 是关联商品 ID。
	ProductId      int64  `json:"productId" sql:"productId"`
	Quantity       int64  `json:"quantity" sql:"quantity"`
	IdempotencyKey string `json:"idempotencyKey" sql:"idempotencyKey"`
	// OrderStatus 是订单状态码。
	OrderStatus int64 `json:"orderStatus" sql:"orderStatus"`
}

const (
	// OrderWait 表示订单待处理。
	OrderWait = iota
	// OrderSuccess 表示订单成功。
	OrderSuccess
	// OrderFailed 表示订单失败。
	OrderFailed
)

// OrederSuccess keeps source compatibility with the original misspelled name.
const OrederSuccess = OrderSuccess
