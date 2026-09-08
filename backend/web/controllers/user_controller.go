package controllers

import (
	"product/common"
	"product/services"
	"strconv"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/mvc"
)

type UserController struct {
	Ctx         iris.Context
	UserService services.IUserService
}

func (u *UserController) Get() mvc.View {
	return mvc.View{
		Name: "user/view.html",
	}
}

func (u *UserController) PostLogin() {
	userName := u.Ctx.FormValue("userName")
	password := u.Ctx.FormValue("password")
	if userName == "" {
		u.Ctx.StatusCode(iris.StatusBadRequest)
		_ = u.Ctx.JSON(common.NewFailResult("userName is required"))
		return
	}
	if password == "" {
		u.Ctx.StatusCode(iris.StatusBadRequest)
		_ = u.Ctx.JSON(common.NewFailResult("password is required"))
		return
	}
	user, ok := u.UserService.IsPwdSuccess(userName, password)
	if !ok {
		u.Ctx.StatusCode(iris.StatusUnauthorized)

		_ = u.Ctx.JSON(common.NewFailResult("用户名或者密码错误"))
		return
	}
	_ = u.Ctx.JSON(common.NewSuccessResult("登陆成功", iris.Map{
		"id":       user.ID,
		"nickName": user.NickName,
		"userName": user.Username,
		"token":    common.IssueToken(int64(user.ID)),
	}))

}

func (u *UserController) PostCreate() {
	user, err := common.BuildUserForCreateFromContext(u.Ctx)
	if err != nil {
		u.Ctx.StatusCode(iris.StatusBadRequest)

		_ = u.Ctx.JSON(common.NewFailResult(err.Error()))
		return
	}

	userID, err := u.UserService.AddUser(user)
	if err != nil {
		if err.Error() == "userName already exists" {
			u.Ctx.StatusCode(iris.StatusBadRequest)
			_ = u.Ctx.JSON(common.NewFailResult(err.Error()))
			return
		}
		u.Ctx.StatusCode(iris.StatusInternalServerError)
		_ = u.Ctx.JSON(common.NewFailResult("用户创建失败"))
		return
	}

	_ = u.Ctx.JSON(common.NewSuccessResult("用户创建成功", iris.Map{
		"id":       userID,
		"nickName": user.NickName,
		"userName": user.Username,
	}))

}

func (u *UserController) GetAll() {
	u.Ctx.StatusCode(iris.StatusForbidden)
	_ = u.Ctx.JSON(common.NewFailResult("user enumeration is disabled"))
}

func (u *UserController) GetBy(id int64) {
	if !u.requireSelf(id) {
		return
	}
	user, err := u.UserService.GetUserById(id)
	if err != nil {
		u.Ctx.StatusCode(404)
		_ = u.Ctx.JSON(common.NewFailResult("没有得到"))
		return
	}
	_ = u.Ctx.JSON(common.NewSuccessResult("成功得到", user))

}

func (u *UserController) PostDelete() {
	idString := u.Ctx.FormValue("id")
	id, err := strconv.ParseInt(idString, 10, 64)
	if err != nil {
		u.Ctx.StatusCode(400)
		_ = u.Ctx.JSON(common.NewFailResult("用户输入不合法"))
		return
	}
	if !u.requireSelf(id) {
		return
	}
	ok := u.UserService.DeleteUserById(id)
	if !ok {
		u.Ctx.StatusCode(iris.StatusInternalServerError)
		_ = u.Ctx.JSON(common.NewFailResult("删除失败"))
		return
	}
	_ = u.Ctx.JSON(common.NewSuccessResult("删除成功", nil))
}

func (u *UserController) PostUpdate() {
	user, err := common.BuildUserForUpdateFromContext(u.Ctx)
	if err != nil {
		u.Ctx.StatusCode(iris.StatusBadRequest)

		_ = u.Ctx.JSON(common.NewFailResult(err.Error()))
		return
	}
	if !u.requireSelf(int64(user.ID)) {
		return
	}
	err = u.UserService.UpdateUser(user)
	if err != nil {
		u.Ctx.StatusCode(iris.StatusInternalServerError)

		_ = u.Ctx.JSON(common.NewFailResult("更新失败"))
		return
	}

	_ = u.Ctx.JSON(common.NewSuccessResult("更新成功", nil))
}

func (u *UserController) requireSelf(targetID int64) bool {
	userID, ok := common.AuthenticatedUserID(u.Ctx)
	if !ok {
		u.Ctx.StatusCode(iris.StatusUnauthorized)
		_ = u.Ctx.JSON(common.NewFailResult("authorization required"))
		return false
	}
	if userID != targetID {
		u.Ctx.StatusCode(iris.StatusForbidden)
		_ = u.Ctx.JSON(common.NewFailResult("cannot access another user"))
		return false
	}
	return true
}
