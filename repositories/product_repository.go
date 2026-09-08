package repositories

import (
	"database/sql"
	"errors"
	"product/common"
	"product/datamodels"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrProductNotFound = errors.New("product not found")
	ErrProductInUse    = errors.New("product is referenced by an order")
)

// IProduct 定义商品仓储层对外提供的能力。
// service 层只依赖接口，不直接依赖具体实现。
type IProduct interface {
	Conn() error
	Insert(*datamodels.Product) (int64, error)
	Delete(int64) error
	Update(*datamodels.Product) error
	SelectByKey(int64) (*datamodels.Product, error)
	SelectAll() ([]*datamodels.Product, error)
	Search(string, int, int) ([]*datamodels.Product, int64, error)
}

func (p *ProductManager) Search(keyword string, limit, offset int) ([]*datamodels.Product, int64, error) {
	if err := p.Conn(); err != nil {
		return nil, 0, err
	}
	pattern := "%" + keyword + "%"
	var total int64
	if err := p.mysqlConn.QueryRow("SELECT COUNT(*) FROM product WHERE productName LIKE ?", pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := p.mysqlConn.Query("SELECT ID,productName,productNum,productImage,productUrl FROM product WHERE productName LIKE ? ORDER BY ID DESC LIMIT ? OFFSET ?", pattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]*datamodels.Product, 0, limit)
	for rows.Next() {
		item := &datamodels.Product{}
		if err := rows.Scan(&item.ID, &item.ProductName, &item.ProductNum, &item.ProductImage, &item.ProductUrl); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// ProductManager 是商品仓储层的默认实现。
// 它负责直接操作 product 表。
type ProductManager struct {
	table     string
	mysqlConn *sql.DB
}

// NewProductManager 创建商品仓储层实例。
func NewProductManager(table string, db *sql.DB) IProduct {
	return &ProductManager{table: "product", mysqlConn: db}
}

// Conn 确保仓储层持有可用数据库连接。
// 如果外部没有注入连接，这里会按默认配置自行创建。
func (p *ProductManager) Conn() (err error) {
	if p.mysqlConn == nil {
		mysql, err := common.GetMySQLConn()
		if err != nil {
			return err
		}
		p.mysqlConn = mysql
	}
	p.table = "product"
	return nil
}

// Insert 向商品表插入一条新记录，并返回数据库生成的主键。
func (p *ProductManager) Insert(product *datamodels.Product) (int64, error) {
	if err := p.Conn(); err != nil {
		return 0, err
	}
	query := "INSERT INTO product (productName, productNum, productImage, productUrl) VALUES (?, ?, ?, ?)"

	result, err := p.mysqlConn.Exec(
		query,
		product.ProductName,
		product.ProductNum,
		product.ProductImage,
		product.ProductUrl,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// Delete 按商品 ID 删除记录。
// 返回值表示是否至少删除了一条数据。
func (p *ProductManager) Delete(productID int64) error {
	if err := p.Conn(); err != nil {
		return err
	}
	query := "DELETE FROM product WHERE ID = ?"
	result, err := p.mysqlConn.Exec(query, productID)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1451 {
			return ErrProductInUse
		}
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrProductNotFound
	}
	return nil
}

// Update 按商品 ID 更新商品的主要字段。
func (p *ProductManager) Update(product *datamodels.Product) error {
	if err := p.Conn(); err != nil {
		return err
	}
	query := "UPDATE product SET productName = ?, productNum = ?, productImage = ?, productUrl = ? WHERE ID = ?"

	result, err := p.mysqlConn.Exec(
		query,
		product.ProductName,
		product.ProductNum,
		product.ProductImage,
		product.ProductUrl,
		product.ID,
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrProductNotFound
	}
	return nil
}

// SelectByKey 按商品主键查询单条记录。
func (p *ProductManager) SelectByKey(productID int64) (*datamodels.Product, error) {
	if err := p.Conn(); err != nil {
		return nil, err
	}
	query := "SELECT * FROM product WHERE ID = ?"
	rows, err := p.mysqlConn.Query(query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := common.RowsToMap(rows)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrProductNotFound
	}

	return common.MapToProduct(result[0]), nil
}

// SelectAll 查询整张商品表，并逐条映射为 Product 结构体切片。
func (p *ProductManager) SelectAll() ([]*datamodels.Product, error) {
	if err := p.Conn(); err != nil {
		return nil, err
	}
	query := "SELECT * FROM product"
	rows, err := p.mysqlConn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := common.RowsToMap(rows)
	if err != nil {
		return nil, err
	}

	products := make([]*datamodels.Product, 0, len(result))
	for _, item := range result {
		products = append(products, common.MapToProduct(item))
	}
	return products, nil
}
