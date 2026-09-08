package services

import (
	"errors"
	"testing"

	"product/datamodels"
)

type productRepositoryMock struct {
	items       []*datamodels.Product
	updated     *datamodels.Product
	inserted    *datamodels.Product
	deletedID   int64
	selectError error
}

func (m *productRepositoryMock) Conn() error { return nil }
func (m *productRepositoryMock) Insert(p *datamodels.Product) (int64, error) {
	m.inserted = p
	return 99, nil
}
func (m *productRepositoryMock) Delete(id int64) error { m.deletedID = id; return nil }
func (m *productRepositoryMock) Update(p *datamodels.Product) error {
	m.updated = p
	return m.selectError
}
func (m *productRepositoryMock) SelectByKey(id int64) (*datamodels.Product, error) {
	if m.selectError != nil {
		return nil, m.selectError
	}
	return &datamodels.Product{ID: id, ProductName: "mock"}, nil
}
func (m *productRepositoryMock) SelectAll() ([]*datamodels.Product, error) {
	return m.items, m.selectError
}
func (m *productRepositoryMock) Search(string, int, int) ([]*datamodels.Product, int64, error) {
	return m.items, int64(len(m.items)), m.selectError
}

func TestProductServiceDelegatesCRUD(t *testing.T) {
	repo := &productRepositoryMock{items: []*datamodels.Product{{ID: 1}}}
	svc := NewProductService(repo, nil)
	product := &datamodels.Product{ProductName: "new"}
	if id, err := svc.InsertProduct(product); err != nil || id != 99 || repo.inserted != product {
		t.Fatalf("insert result id=%d err=%v", id, err)
	}
	if err := svc.UpdateProduct(product); err != nil || repo.updated != product {
		t.Fatalf("update result err=%v", err)
	}
	if err := svc.DeleteProductById(7); err != nil || repo.deletedID != 7 {
		t.Fatalf("delete was not delegated")
	}
	if got, err := svc.GetProductById(3); err != nil || got.ID != 3 {
		t.Fatalf("get result=%+v err=%v", got, err)
	}
}

func TestProductServiceNormalizesPagination(t *testing.T) {
	repo := &productRepositoryMock{}
	svc := NewProductService(repo, nil)
	items, total, err := svc.ListProducts(0, 1000, "go")
	if err != nil || total != 0 {
		t.Fatalf("list result items=%v total=%d err=%v", items, total, err)
	}
}

func TestProductServicePropagatesRepositoryErrors(t *testing.T) {
	want := errors.New("database unavailable")
	repo := &productRepositoryMock{selectError: want}
	svc := NewProductService(repo, nil)
	if _, err := svc.GetProductById(1); !errors.Is(err, want) {
		t.Fatalf("expected repository error, got %v", err)
	}
	if err := svc.UpdateProduct(&datamodels.Product{ID: 1}); !errors.Is(err, want) {
		t.Fatalf("expected update error, got %v", err)
	}
}
