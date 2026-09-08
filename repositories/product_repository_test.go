package repositories

import (
	"errors"
	"regexp"
	"testing"

	"product/datamodels"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestProductRepositorySearch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewProductManager("ignored", db)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM product WHERE productName LIKE ?")).WithArgs("%Go%").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT ID,productName,productNum,productImage,productUrl FROM product WHERE productName LIKE ? ORDER BY ID DESC LIMIT ? OFFSET ?")).WithArgs("%Go%", 20, 0).WillReturnRows(sqlmock.NewRows([]string{"ID", "productName", "productNum", "productImage", "productUrl"}).AddRow(1, "Go Book", 10, "image", "url"))
	items, total, err := repo.Search("Go", 20, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].ProductName != "Go Book" {
		t.Fatalf("Search items=%+v total=%d err=%v", items, total, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProductRepositoryUpdateAndNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewProductManager("ignored", db)
	product := &datamodels.Product{ID: 7, ProductName: "Go", ProductNum: 3, ProductImage: "image", ProductUrl: "url"}
	update := regexp.QuoteMeta("UPDATE product SET productName = ?, productNum = ?, productImage = ?, productUrl = ? WHERE ID = ?")
	mock.ExpectExec(update).WithArgs(product.ProductName, product.ProductNum, product.ProductImage, product.ProductUrl, product.ID).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.Update(product); !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
	selectQuery := regexp.QuoteMeta("SELECT * FROM product WHERE ID = ?")
	mock.ExpectQuery(selectQuery).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"ID", "productName", "productNum", "productImage", "productUrl"}))
	if _, err := repo.SelectByKey(7); !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("expected select not found, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
