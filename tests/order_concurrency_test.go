package tests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"product/common"
	"product/datamodels"
	"product/repositories"
)

func TestConcurrentOrdersDoNotOversell(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run MySQL tests")
	}
	db, err := common.GetMySQLConn()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE product SET productNum = 100 WHERE ID = 1"); err != nil {
		t.Fatal(err)
	}

	repo := repositories.NewOrderManagerRepository("order", db)
	prefix := fmt.Sprintf("concurrency-%d", time.Now().UnixNano())
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := repo.CreateWithStock(context.Background(), &datamodels.Order{UserID: 1, ProductId: 1, Quantity: 60, OrderStatus: datamodels.OrderWait, IdempotencyKey: fmt.Sprintf("%s-%d", prefix, n)})
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, stockFailures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, repositories.ErrInsufficientStock) {
			stockFailures++
		} else {
			t.Fatalf("unexpected order error: %v", err)
		}
	}
	if successes != 1 || stockFailures != 1 {
		t.Fatalf("success=%d stock_failures=%d", successes, stockFailures)
	}
	var stock int64
	if err = db.QueryRow("SELECT productNum FROM product WHERE ID = 1").Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if stock != 40 {
		t.Fatalf("expected stock 40, got %d", stock)
	}
}

func TestConcurrentIdempotentRetryDeductsOnce(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run MySQL tests")
	}
	db, err := common.GetMySQLConn()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE product SET productNum = 100 WHERE ID = 1"); err != nil {
		t.Fatal(err)
	}
	repo := repositories.NewOrderManagerRepository("order", db)
	key := fmt.Sprintf("same-key-%d", time.Now().UnixNano())
	start, ids, errs := make(chan struct{}), make(chan int64, 2), make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := repo.CreateWithStock(context.Background(), &datamodels.Order{UserID: 1, ProductId: 1, Quantity: 10, OrderStatus: datamodels.OrderWait, IdempotencyKey: key})
			ids <- id
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("idempotent request failed: %v", err)
		}
	}
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
		} else if id != first {
			t.Fatalf("expected same order ID, got %d and %d", first, id)
		}
	}
	var stock int64
	if err = db.QueryRow("SELECT productNum FROM product WHERE ID=1").Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if stock != 90 {
		t.Fatalf("expected one deduction and stock 90, got %d", stock)
	}
}

func TestIdempotencyKeyRejectsDifferentPayload(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run MySQL tests")
	}
	db, err := common.GetMySQLConn()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE product SET productNum = 100 WHERE ID = 1"); err != nil {
		t.Fatal(err)
	}
	repo := repositories.NewOrderManagerRepository("order", db)
	key := fmt.Sprintf("conflict-key-%d", time.Now().UnixNano())
	order := &datamodels.Order{UserID: 1, ProductId: 1, Quantity: 2, OrderStatus: datamodels.OrderWait, IdempotencyKey: key}
	if _, err = repo.CreateWithStock(context.Background(), order); err != nil {
		t.Fatal(err)
	}
	order.Quantity = 3
	if _, err = repo.CreateWithStock(context.Background(), order); !errors.Is(err, repositories.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	var stock int64
	if err = db.QueryRow("SELECT productNum FROM product WHERE ID=1").Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if stock != 98 {
		t.Fatalf("conflicting retry must not deduct more stock, got %d", stock)
	}
}
