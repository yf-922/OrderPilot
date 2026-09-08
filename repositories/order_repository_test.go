package repositories

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"product/datamodels"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestOrderRepositoryIdempotentReplayAndConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewOrderManagerRepository("ignored", db)
	order := &datamodels.Order{UserID: 1, ProductId: 2, Quantity: 3, OrderStatus: datamodels.OrderWait, IdempotencyKey: "request-key"}
	query := regexp.QuoteMeta("SELECT ID,userID,productID,quantity,orderStatus FROM `order` WHERE idempotencyKey = ?")
	mock.ExpectBegin()
	mock.ExpectQuery(query).WithArgs(order.IdempotencyKey).WillReturnRows(sqlmock.NewRows([]string{"ID", "userID", "productID", "quantity", "orderStatus"}).AddRow(9, 1, 2, 3, 0))
	mock.ExpectCommit()
	id, err := repo.CreateWithStock(context.Background(), order)
	if err != nil || id != 9 {
		t.Fatalf("replay id=%d err=%v", id, err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(query).WithArgs(order.IdempotencyKey).WillReturnRows(sqlmock.NewRows([]string{"ID", "userID", "productID", "quantity", "orderStatus"}).AddRow(9, 1, 2, 4, 0))
	mock.ExpectRollback()
	if _, err := repo.CreateWithStock(context.Background(), order); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOrderRepositoryUpdateStatusRequiresPendingOwnedOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewOrderManagerRepository("ignored", db)
	query := regexp.QuoteMeta("UPDATE `order` SET orderStatus=? WHERE ID=? AND userID=? AND orderStatus=?")
	mock.ExpectExec(query).WithArgs(datamodels.OrderSuccess, int64(5), int64(1), datamodels.OrderWait).WillReturnResult(sqlmock.NewResult(0, 0))
	stateQuery := regexp.QuoteMeta("SELECT orderStatus FROM `order` WHERE ID=? AND userID=?")
	mock.ExpectQuery(stateQuery).WithArgs(int64(5), int64(1)).WillReturnRows(sqlmock.NewRows([]string{"orderStatus"}).AddRow(datamodels.OrderSuccess))
	if err := repo.UpdateStatus(context.Background(), 5, 1, datamodels.OrderSuccess); !errors.Is(err, ErrInvalidOrderState) {
		t.Fatalf("expected invalid state, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
