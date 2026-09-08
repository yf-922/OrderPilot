package repositories

import (
	"context"
	"database/sql"
	"errors"
	"product/common"
	"product/datamodels"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrOrderUserNotFound    = errors.New("user not found")
	ErrOrderProductNotFound = errors.New("product not found")
	ErrInsufficientStock    = errors.New("insufficient stock")
	ErrOrderNotFound        = errors.New("order not found")
	ErrInvalidOrderState    = errors.New("invalid order state")
	ErrIdempotencyConflict  = errors.New("idempotency key conflicts with request")
)

// OrderRepository 定义订单仓储层需要实现的能力。
type OrderRepository interface {
	Conn() error
	CreateWithStock(context.Context, *datamodels.Order) (int64, error)
	SelectByKeyForUser(context.Context, int64, int64) (*datamodels.Order, error)
	ListByUser(context.Context, int64, int, int) ([]*datamodels.Order, int64, error)
	UpdateStatus(context.Context, int64, int64, int64) error
	CancelAndRestock(context.Context, int64, int64) error
}

func (o *OrderManagerRepository) SelectByKeyForUser(ctx context.Context, orderID, userID int64) (*datamodels.Order, error) {
	if err := o.Conn(); err != nil {
		return nil, err
	}
	row := o.mysqlConn.QueryRowContext(ctx, "SELECT ID,userID,productID,quantity,orderStatus,idempotencyKey FROM `order` WHERE ID=? AND userID=?", orderID, userID)
	order := &datamodels.Order{}
	if err := row.Scan(&order.ID, &order.UserID, &order.ProductId, &order.Quantity, &order.OrderStatus, &order.IdempotencyKey); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOrderNotFound
	} else if err != nil {
		return nil, err
	}
	return order, nil
}

func (o *OrderManagerRepository) ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*datamodels.Order, int64, error) {
	if err := o.Conn(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := o.mysqlConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM `order` WHERE userID=?", userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := o.mysqlConn.QueryContext(ctx, "SELECT ID,userID,productID,quantity,orderStatus,idempotencyKey FROM `order` WHERE userID=? ORDER BY ID DESC LIMIT ? OFFSET ?", userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	orders := make([]*datamodels.Order, 0, limit)
	for rows.Next() {
		order := &datamodels.Order{}
		if err := rows.Scan(&order.ID, &order.UserID, &order.ProductId, &order.Quantity, &order.OrderStatus, &order.IdempotencyKey); err != nil {
			return nil, 0, err
		}
		orders = append(orders, order)
	}
	return orders, total, rows.Err()
}

func (o *OrderManagerRepository) UpdateStatus(ctx context.Context, orderID, userID, status int64) error {
	if err := o.Conn(); err != nil {
		return err
	}
	result, err := o.mysqlConn.ExecContext(ctx, "UPDATE `order` SET orderStatus=? WHERE ID=? AND userID=? AND orderStatus=?", status, orderID, userID, datamodels.OrderWait)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		var currentStatus int64
		err := o.mysqlConn.QueryRowContext(ctx, "SELECT orderStatus FROM `order` WHERE ID=? AND userID=?", orderID, userID).Scan(&currentStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		return ErrInvalidOrderState
	}
	return nil
}

func (o *OrderManagerRepository) CancelAndRestock(ctx context.Context, orderID, userID int64) error {
	if err := o.Conn(); err != nil {
		return err
	}
	tx, err := o.mysqlConn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var productID, quantity, status int64
	err = tx.QueryRowContext(ctx, "SELECT productID,quantity,orderStatus FROM `order` WHERE ID=? AND userID=? FOR UPDATE", orderID, userID).Scan(&productID, &quantity, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOrderNotFound
	}
	if err != nil {
		return err
	}
	if status != datamodels.OrderWait {
		return ErrInvalidOrderState
	}
	if _, err = tx.ExecContext(ctx, "UPDATE `order` SET orderStatus=? WHERE ID=?", datamodels.OrderFailed, orderID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE product SET productNum=productNum+? WHERE ID=?", quantity, productID); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateWithStock atomically locks inventory, deducts stock and creates an order.
// Reusing an idempotency key returns the original order ID without deducting twice.
func (o *OrderManagerRepository) CreateWithStock(ctx context.Context, order *datamodels.Order) (int64, error) {
	if err := o.Conn(); err != nil {
		return 0, err
	}
	tx, err := o.mysqlConn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var existing datamodels.Order
	err = tx.QueryRowContext(ctx, "SELECT ID,userID,productID,quantity,orderStatus FROM `order` WHERE idempotencyKey = ?", order.IdempotencyKey).
		Scan(&existing.ID, &existing.UserID, &existing.ProductId, &existing.Quantity, &existing.OrderStatus)
	if err == nil {
		if existing.UserID != order.UserID || existing.ProductId != order.ProductId || existing.Quantity != order.Quantity || existing.OrderStatus != order.OrderStatus {
			return 0, ErrIdempotencyConflict
		}
		return existing.ID, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	var userExists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM `User` WHERE ID = ?", order.UserID).Scan(&userExists); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrOrderUserNotFound
	} else if err != nil {
		return 0, err
	}

	var stock int64
	if err = tx.QueryRowContext(ctx, "SELECT productNum FROM product WHERE ID = ? FOR UPDATE", order.ProductId).Scan(&stock); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrOrderProductNotFound
	} else if err != nil {
		return 0, err
	}
	if stock < order.Quantity {
		return 0, ErrInsufficientStock
	}

	result, err := tx.ExecContext(ctx, "UPDATE product SET productNum = productNum - ? WHERE ID = ? AND productNum >= ?", order.Quantity, order.ProductId, order.Quantity)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rows != 1 {
		return 0, ErrInsufficientStock
	}

	result, err = tx.ExecContext(ctx, "INSERT INTO `order` (userID,productID,quantity,orderStatus,idempotencyKey) VALUES (?,?,?,?,?)", order.UserID, order.ProductId, order.Quantity, order.OrderStatus, order.IdempotencyKey)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			_ = tx.Rollback()
			var existing datamodels.Order
			if lookupErr := o.mysqlConn.QueryRowContext(ctx, "SELECT ID,userID,productID,quantity,orderStatus FROM `order` WHERE idempotencyKey=?", order.IdempotencyKey).Scan(&existing.ID, &existing.UserID, &existing.ProductId, &existing.Quantity, &existing.OrderStatus); lookupErr == nil {
				if existing.UserID != order.UserID || existing.ProductId != order.ProductId || existing.Quantity != order.Quantity || existing.OrderStatus != order.OrderStatus {
					return 0, ErrIdempotencyConflict
				}
				return existing.ID, nil
			}
		}
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// NewOrderManagerRepository 创建订单仓储层实现。
func NewOrderManagerRepository(table string, sql *sql.DB) OrderRepository {
	return &OrderManagerRepository{table: "order", mysqlConn: sql}
}

// OrderManagerRepository 负责直接读写订单表。
type OrderManagerRepository struct {
	table     string
	mysqlConn *sql.DB
}

// Conn 确保订单仓储层拥有可用连接，并补齐默认表名。
func (o *OrderManagerRepository) Conn() error {
	if o.mysqlConn == nil {
		mysql, err := common.GetMySQLConn()
		if err != nil {
			return err
		}
		o.mysqlConn = mysql
	}
	o.table = "order"
	return nil
}
