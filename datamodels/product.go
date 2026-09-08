package datamodels

// Product 对应商品表的数据结构。
// 这个结构体会在 repository、service、controller、模板之间传递。
type Product struct {
	// ID 是商品主键。
	ID int64 `json:"id" sql:"ID" imooc:"id"`
	// ProductName 是商品名称。
	ProductName string `json:"ProductName" sql:"productName" imooc:"ProductName"`
	// ProductNum 是库存数量。
	ProductNum int64 `json:"ProductNum" sql:"productNum" imooc:"ProductNum"`
	// ProductImage 是商品图片地址。
	ProductImage string `json:"ProductImage" sql:"productImage" imooc:"ProductImage"`
	// ProductUrl 是商品详情页或跳转链接地址。
	ProductUrl string `json:"ProductUrl" sql:"productUrl" imooc:"ProductUrl"`
}
