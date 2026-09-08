package repositories

import (
	"database/sql"
	"errors"
	"fmt"
	"product/common"
	"product/datamodels"
)

var ErrUserNotFound = errors.New("user not found")

// IUserRepository 定义用户仓储层对外能力。
type IUserRepository interface {
	Conn() error
	Select(userName string) (user *datamodels.User, err error)
	Insert(user *datamodels.User) (userId int64, err error)
	SelectAll() ([]*datamodels.User, error)
	SelectById(id int64) (user *datamodels.User, err error)
	DeleteById(id int64) bool
	Update(user *datamodels.User) error
}

// NewUserRepository 创建用户仓储层实例。
func NewUserRepository(table string, db *sql.DB) IUserRepository {
	return &UserManagerRepository{table, db}
}

// UserManagerRepository 负责查询和写入用户表。
type UserManagerRepository struct {
	table     string
	mysqlConn *sql.DB
}

// Conn 确保仓储层持有可用连接，并在缺省时补齐表名。
func (u *UserManagerRepository) Conn() error {
	if u.mysqlConn == nil {
		mysql, err := common.GetMySQLConn()
		if err != nil {
			return err
		}
		u.mysqlConn = mysql
	}
	if u.table == "" {
		u.table = "User"
	}
	return nil
}

// Select 按用户名查询用户。
// 主要用于登录时取出用户信息和密码哈希值。
func (u *UserManagerRepository) Select(userName string) (user *datamodels.User, err error) {
	if userName == "" {
		return &datamodels.User{}, fmt.Errorf("userName is empty")
	}
	if err = u.Conn(); err != nil {
		return &datamodels.User{}, err
	}
	query := fmt.Sprintf("SELECT * FROM %s WHERE Username = ?", u.table)
	rows, err := u.mysqlConn.Query(query, userName)

	if err != nil {
		return &datamodels.User{}, err
	}
	defer rows.Close()
	result, err := common.RowsToMap(rows)
	if err != nil {
		return &datamodels.User{}, err
	}
	if len(result) == 0 {
		return &datamodels.User{}, ErrUserNotFound
	}
	return common.MapToUser(result[0]), nil
}

// Insert 向用户表插入新用户。
func (u *UserManagerRepository) Insert(user *datamodels.User) (userId int64, err error) {
	if err := u.Conn(); err != nil {
		return 0, err
	}
	query := fmt.Sprintf("INSERT INTO %s ( NickName,Username,HashPassword) VALUES (?, ?, ?)", u.table)
	result, err := u.mysqlConn.Exec(
		query,
		user.NickName,
		user.Username,
		user.HashPassword,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (u *UserManagerRepository) SelectAll() (users []*datamodels.User, err error) {
	if err = u.Conn(); err != nil {
		return nil, err
	}
	query := fmt.Sprintf("SELECT * FROM %s", u.table)
	rows, err := u.mysqlConn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result, err := common.RowsToMap(rows)
	if err != nil {
		return nil, err
	}
	users = make([]*datamodels.User, 0, len(result))
	for _, v := range result {
		users = append(users, common.MapToUser(v))
	}
	return users, nil
}

func (u *UserManagerRepository) SelectById(id int64) (user *datamodels.User, err error) {
	if err := u.Conn(); err != nil {
		return &datamodels.User{}, err
	}
	query := fmt.Sprintf("SELECT * FROM %s WHERE ID = ?", u.table)
	rows, err := u.mysqlConn.Query(query, id)
	if err != nil {
		return &datamodels.User{}, err
	}
	defer rows.Close()
	result, err := common.RowsToMap(rows)
	if err != nil {
		return &datamodels.User{}, err
	}
	if len(result) == 0 {
		return &datamodels.User{}, ErrUserNotFound
	}
	return common.MapToUser(result[0]), nil
}

func (u *UserManagerRepository) DeleteById(id int64) bool {
	if err := u.Conn(); err != nil {
		return false
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE ID = ?", u.table)
	result, err := u.mysqlConn.Exec(query, id)
	if err != nil {
		return false
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false
	}
	return rowsAffected > 0
}

func (u *UserManagerRepository) Update(user *datamodels.User) error {
	if err := u.Conn(); err != nil {
		return err
	}
	query := fmt.Sprintf("UPDATE %s SET NickName = ?,Username = ? ,HashPassword = ? WHERE ID = ?", u.table)
	result, err := u.mysqlConn.Exec(query, user.NickName, user.Username, user.HashPassword, user.ID)
	if err != nil {
		return err
	}
	_, err = result.RowsAffected()
	return err
}
