package services

import (
	"encoding/json"
	"log"
	"product/datamodels"
	"product/repositories"
	"strconv"

	"github.com/mediocregopher/radix/v3"
)

const (
	productAllCacheKey = "product:all"
	productAllCacheTTL = 60
)

// IProductService 定义商品业务层对外能力。
type IProductService interface {
	GetProductById(int64) (*datamodels.Product, error)
	GetProductAll() ([]*datamodels.Product, error)
	DeleteProductById(int64) bool
	InsertProduct(product *datamodels.Product) (int64, error)
	UpdateProduct(product *datamodels.Product) error
	InvalidateCache()
	ListProducts(int, int, string) ([]*datamodels.Product, int64, error)
}

func (p *ProductService) ListProducts(page, pageSize int, keyword string) ([]*datamodels.Product, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return p.productRepository.Search(keyword, pageSize, (page-1)*pageSize)
}

// ProductService 是商品业务层实现。
// 当前逻辑较薄，主要是对 repository 做一层封装。
type ProductService struct {
	productRepository repositories.IProduct
	redisPool         *radix.Pool
}

// NewProductService 创建商品业务层实例。
func NewProductService(repository repositories.IProduct, redisPool *radix.Pool) IProductService {
	return &ProductService{
		productRepository: repository,
		redisPool:         redisPool,
	}
}

// GetProductById 按商品 ID 查询单个商品。
func (p *ProductService) GetProductById(id int64) (*datamodels.Product, error) {
	return p.productRepository.SelectByKey(id)
}

// GetProductAll 查询全部商品。
func (p *ProductService) GetProductAll() ([]*datamodels.Product, error) {
	if p.redisPool == nil {
		return p.productRepository.SelectAll()
	}
	var cacheValue string
	err := p.redisPool.Do(radix.Cmd(&cacheValue, "GET",
		productAllCacheKey))
	if err != nil {
		log.Println("product cache get error:", err)
	}
	if err == nil && cacheValue != "" {
		var products []*datamodels.Product
		unmarshalErr := json.Unmarshal([]byte(cacheValue), &products)
		if unmarshalErr == nil {
			log.Println("product cache hit")
			return products, nil
		}

		log.Println("product cache unmarshal error:", unmarshalErr)
	}
	log.Println("product cache miss, fallback to mysql")
	products, err := p.productRepository.SelectAll()
	if err != nil {
		return nil, err
	}

	cacheBytes, err := json.Marshal(products)
	if err == nil {
		_ = p.redisPool.Do(radix.Cmd(nil, "SETEX", productAllCacheKey,
			strconv.Itoa(productAllCacheTTL), string(cacheBytes)))
	}

	return products, nil

}

// DeleteProductById 按 ID 删除商品。
func (p *ProductService) DeleteProductById(id int64) bool {
	ok := p.productRepository.Delete(id)
	if ok {
		p.invalidateProductAllCache()
	}
	return ok
}

// InsertProduct 新增商品。
func (p *ProductService) InsertProduct(product *datamodels.Product) (int64, error) {
	productID, err := p.productRepository.Insert(product)
	if err != nil {
		return 0, err
	}
	p.invalidateProductAllCache()
	return productID, nil
}

// UpdateProduct 更新商品信息。
func (p *ProductService) UpdateProduct(product *datamodels.Product) error {
	if err := p.productRepository.Update(product); err != nil {
		return err
	}
	p.invalidateProductAllCache()
	return nil
}

func (p *ProductService) invalidateProductAllCache() {
	if p.redisPool == nil {
		return
	}
	_ = p.redisPool.Do(radix.Cmd(nil, "DEL", productAllCacheKey))
}

func (p *ProductService) InvalidateCache() { p.invalidateProductAllCache() }
