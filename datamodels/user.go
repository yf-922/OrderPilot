package datamodels

// User 对应用户表的数据结构。
type User struct {
	// ID 是用户主键。
	ID int `json:"id" form:"ID" sql:"ID"`
	// NickName 是用户昵称。
	NickName string `json:"nickName" form:"nickName" sql:"nickName"`
	// Username 是登录用户名。
	Username string `json:"userName" form:"userName" sql:"userName"`
	// HashPassword 存储数据库中的密码哈希值。
	// 对外返回 JSON 时不暴露该字段。
	HashPassword string `json:"-" form:"hashPassword" sql:"hashPassword"`
}
