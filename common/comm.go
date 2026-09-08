package common

import (
	"errors"
	"fmt"
	"strconv"

	"product/datamodels"

	"github.com/kataras/iris/v12"
)

type Result struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func NewSuccessResult(message string, data interface{}) *Result {
	return &Result{Success: true, Message: message, Data: data}
}

func NewFailResult(message string) *Result {
	return &Result{Success: false, Message: message}
}

func BuildProductForCreateFromContext(ctx iris.Context) (*datamodels.Product, error) {
	productName := ctx.FormValue("productName")
	productImg := ctx.FormValue("productImage")
	productUrl := ctx.FormValue("productUrl")
	if productName == "" {
		return nil, errors.New("productName is required")
	}
	if productImg == "" {
		return nil, errors.New("productImage is required")
	}
	if productUrl == "" {
		return nil, errors.New("productUrl is required")
	}

	productNum, err := readInt64FormValue(ctx, "productNum")
	if err != nil {
		return nil, err
	}
	if productNum < 0 {
		return nil, errors.New("productNum must be non-negative")
	}

	product := &datamodels.Product{
		ProductName:  productName,
		ProductNum:   productNum,
		ProductImage: productImg,
		ProductUrl:   productUrl,
	}

	return product, nil
}

func BuildProductForUpdateFromContext(ctx iris.Context) (*datamodels.Product, error) {
	productName := ctx.FormValue("productName")
	productImg := ctx.FormValue("productImage")
	productUrl := ctx.FormValue("productUrl")
	if productName == "" {
		return nil, errors.New("productName is required")
	}
	if productImg == "" {
		return nil, errors.New("productImage is required")
	}
	if productUrl == "" {
		return nil, errors.New("productUrl is required")
	}
	id, err := readInt64FormValue(ctx, "id")
	if err != nil {
		return nil, err
	}

	productNum, err := readInt64FormValue(ctx, "productNum")
	if err != nil {
		return nil, err
	}
	if productNum < 0 {
		return nil, errors.New("productNum must be non-negative")
	}

	product := &datamodels.Product{
		ID:           id,
		ProductName:  productName,
		ProductNum:   productNum,
		ProductImage: productImg,
		ProductUrl:   productUrl,
	}

	return product, nil
}

// MapToProduct 将通用 map 查询结果转换成 Product 结构体。
// 这样 repository 层可以先做通用查询，再在这里统一映射为业务对象。
func MapToProduct(data map[string]any) *datamodels.Product {
	return &datamodels.Product{
		ID:           toInt64(data["ID"]),
		ProductName:  toString(data["productName"]),
		ProductNum:   toInt64(data["productNum"]),
		ProductImage: toString(data["productImage"]),
		ProductUrl:   toString(data["productUrl"]),
	}
}

// MapToOrder 将数据库查询结果转换成 Order 结构体。
// 这里同时兼容 productID 和 productId 两种键名，避免字段大小写不一致导致取值失败。
func MapToOrder(data map[string]any) *datamodels.Order {
	return &datamodels.Order{
		ID:             toInt64(data["ID"]),
		UserID:         toInt64(data["userID"]),
		ProductId:      toInt64(firstNonNil(data["productID"], data["productId"])),
		Quantity:       toInt64(data["quantity"]),
		IdempotencyKey: toString(data["idempotencyKey"]),
		OrderStatus:    toInt64(data["orderStatus"]),
	}
}

// MapToUser 将查询结果转换成 User 结构体。
func MapToUser(data map[string]any) *datamodels.User {
	return &datamodels.User{
		ID:           int(toInt64(data["ID"])),
		NickName:     toString(data["NickName"]),
		Username:     toString(data["Username"]),
		HashPassword: toString(data["HashPassword"]),
	}
}

// BuildUserFromContext 从请求表单中提取用户信息并组装为 User。
// 当前逻辑要求 id 必填，密码会先暂存到 HashPassword 字段，后续由 service 再做哈希处理。
func BuildUserForCreateFromContext(ctx iris.Context) (*datamodels.User, error) {
	nickName := ctx.FormValue("nickName")
	userName := ctx.FormValue("userName")
	password := ctx.FormValue("password")
	if nickName == "" {
		return nil, fmt.Errorf("nickName is required")
	}
	if userName == "" {
		return nil, fmt.Errorf("userName is required")
	}
	if password == "" {
		return nil, fmt.Errorf("password is required")
	}
	if len(userName) < 3 || len(userName) > 64 {
		return nil, fmt.Errorf("userName length must be between 3 and 64")
	}
	if len(password) < 6 {
		return nil, fmt.Errorf("password must be at least 6 characters")
	}
	return &datamodels.User{
		NickName:     nickName,
		Username:     userName,
		HashPassword: password,
	}, nil
}

func BuildUserForUpdateFromContext(ctx iris.Context) (*datamodels.User, error) {
	nickName := ctx.FormValue("nickName")
	userName := ctx.FormValue("userName")
	password := ctx.FormValue("password")
	if nickName == "" {
		return nil, fmt.Errorf("nickName is required")
	}
	if userName == "" {
		return nil, fmt.Errorf("userName is required")
	}
	if password == "" {
		return nil, fmt.Errorf("password is required")
	}
	id, err := readInt64FormValue(ctx, "id")
	if err != nil {
		return nil, err
	}
	return &datamodels.User{
		ID:           int(id),
		NickName:     nickName,
		Username:     userName,
		HashPassword: password,
	}, nil
}

func BuildOrderForCreateFromContext(ctx iris.Context) (*datamodels.Order, error) {
	userID, err := readInt64FormValue(ctx, "userID")
	if err != nil {
		return nil, err
	}
	productID, err := readInt64FormValue(ctx, "productID")
	if err != nil {
		return nil, err
	}
	orderStatus, err := readInt64FormValue(ctx, "orderStatus")
	if err != nil {
		return nil, err
	}
	quantity := int64(1)
	if value := ctx.FormValue("quantity"); value != "" {
		quantity, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("quantity must be int64")
		}
	}
	if quantity <= 0 {
		return nil, fmt.Errorf("quantity must be positive")
	}
	if orderStatus < 0 || orderStatus > 2 {
		return nil, fmt.Errorf("orderStatus must be 0, 1 or 2")
	}
	return &datamodels.Order{
		UserID:      userID,
		ProductId:   productID,
		OrderStatus: orderStatus,
		Quantity:    quantity,
	}, nil
}

// readInt64FormValue 读取指定表单字段，并将其转换成 int64。
// 如果字段缺失或格式错误，会直接返回可读错误信息。
func readInt64FormValue(ctx iris.Context, key string) (int64, error) {
	value := ctx.FormValue(key)
	if value == "" {
		return 0, fmt.Errorf("%s is required", key)
	}

	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be int64", key)
	}

	return number, nil
}

// toString 将任意值尽量转换成字符串，便于模板渲染或结构体赋值。
func toString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// toInt64 将常见的数据库返回类型统一转成 int64。
func toInt64(value any) int64 {
	switch v := value.(type) {
	case nil:
		return 0
	case int64:
		return v
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case []byte:
		number, _ := strconv.ParseInt(string(v), 10, 64)
		return number
	case string:
		number, _ := strconv.ParseInt(v, 10, 64)
		return number
	default:
		number, _ := strconv.ParseInt(fmt.Sprintf("%v", v), 10, 64)
		return number
	}
}

// firstNonNil 返回参数列表中第一个非 nil 的值。
// 这个函数主要用于兼容不同字段名场景。
func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
