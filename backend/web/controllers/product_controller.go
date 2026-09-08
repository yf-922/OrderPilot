package controllers

import (
	"product/common"
	"product/services"
	"strconv"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/mvc"
)

type ProductController struct {
	Ctx            iris.Context
	ProductService services.IProductService
}

func (p *ProductController) GetList() {
	page, _ := strconv.Atoi(p.Ctx.URLParamDefault("page", "1"))
	pageSize, _ := strconv.Atoi(p.Ctx.URLParamDefault("pageSize", "20"))
	items, total, err := p.ProductService.ListProducts(page, pageSize, p.Ctx.URLParam("keyword"))
	if err != nil {
		p.Ctx.StatusCode(iris.StatusInternalServerError)
		_ = p.Ctx.JSON(common.NewFailResult("product list failed"))
		return
	}
	_ = p.Ctx.JSON(common.NewSuccessResult("ok", iris.Map{"items": items, "page": page, "pageSize": pageSize, "total": total}))
}

func (p *ProductController) GetAll() mvc.Result {
	productArray, err := p.ProductService.GetProductAll()

	if err != nil {
		p.Ctx.StatusCode(iris.StatusInternalServerError)

		p.Ctx.ViewData("message", "商品列表加载失败: "+err.Error())
		return mvc.View{
			Name:   "shared/error.html",
			Layout: "",
		}
	}

	return mvc.View{
		Name: "product/view.html",
		Data: iris.Map{
			"products": productArray,
		},
	}
}

func (p *ProductController) PostUpdate() {
	product, err := common.BuildProductForUpdateFromContext(p.Ctx)

	if err != nil {

		p.Ctx.StatusCode(iris.StatusBadRequest)
		_ = p.Ctx.JSON(common.NewFailResult(err.Error()))
		return
	}

	if err = p.ProductService.UpdateProduct(product); err != nil {
		if err.Error() == "product not found" {
			p.Ctx.StatusCode(iris.StatusNotFound)
			_ = p.Ctx.JSON(common.NewFailResult(err.Error()))
			return
		}
		p.Ctx.StatusCode(iris.StatusInternalServerError)
		_ = p.Ctx.JSON(common.NewFailResult(err.Error()))

		return
	}
	_ = p.Ctx.JSON(common.NewSuccessResult("商品修改成功", product))

}

func (p *ProductController) PostCreate() {
	product, err := common.BuildProductForCreateFromContext(p.Ctx)
	if err != nil {
		p.Ctx.StatusCode(iris.StatusBadRequest)
		_ = p.Ctx.JSON(common.NewFailResult(err.Error()))
		return
	}

	productID, err := p.ProductService.InsertProduct(product)
	if err != nil {
		p.Ctx.StatusCode(iris.StatusInternalServerError)
		_ = p.Ctx.JSON(common.NewFailResult("商品创建失败"))
		return
	}
	_ = p.Ctx.JSON(iris.Map{
		"id":           productID,
		"productName":  product.ProductName,
		"productNum":   product.ProductNum,
		"productImage": product.ProductImage,
		"productUrl":   product.ProductUrl,
	})

}

func (p *ProductController) GetBy(id int64) {
	product, err := p.ProductService.GetProductById(id)
	if err != nil {
		p.Ctx.StatusCode(iris.StatusNotFound)
		_ = p.Ctx.JSON(common.NewFailResult("product not found"))
		return
	}
	_ = p.Ctx.JSON(common.NewSuccessResult("ok", product))
}

func (p *ProductController) PostDelete() {
	id, err := strconv.ParseInt(p.Ctx.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		p.Ctx.StatusCode(iris.StatusBadRequest)
		_ = p.Ctx.JSON(common.NewFailResult("id must be a positive integer"))
		return
	}
	if !p.ProductService.DeleteProductById(id) {
		p.Ctx.StatusCode(iris.StatusNotFound)
		_ = p.Ctx.JSON(common.NewFailResult("product not found"))
		return
	}
	_ = p.Ctx.JSON(common.NewSuccessResult("product deleted", nil))
}
