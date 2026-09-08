package repositories

import (
	"database/sql"
	"errors"
	"fmt"
	"product/common"
	"product/datamodels"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrUserNotFound  = errors.New("user not found")
	ErrUserNameTaken = errors.New("userName already exists")
	ErrUserHasOrders = errors.New("user is referenced by an order")
)

// IUserRepository 定义用户仓储层对外能力。
type IUserRepository interface {
	Conn() error
	Select(userName string) (user *datamodels.User, err error)
	Insert(user *datamodels.User) (userId int64, err error)
	SelectAll() ([]*datamodels.User, error)
	SelectById(id int64) (user *datamodels.User, err error)
	DeleteById(id int64) error
	Update(user *datamodels.User) error
}

// NewUserRepository 创建用户仓储层实例。
func NewUserRepository(table string, db *sql.DB) IUserRepository {
	return &UserManagerRepository{"User", db}
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
	u.table = "User"
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
	query := "SELECT * FROM `User` WHERE Username = ?"
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
	query := "INSERT INTO `User` (NickName,Username,HashPassword) VALUES (?, ?, ?)"
	result, err := u.mysqlConn.Exec(
		query,
		user.NickName,
		user.Username,
		user.HashPassword,
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return 0, ErrUserNameTaken
		}
		return 0, err
	}
	return result.LastInsertId()
}

func (u *UserManagerRepository) SelectAll() (users []*datamodels.User, err error) {
	if err = u.Conn(); err != nil {
		return nil, err
	}
	query := "SELECT * FROM `User`"
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
	query := "SELECT * FROM `User` WHERE ID = ?"
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

func (u *UserManagerRepository) DeleteById(id int64) error {
	if err := u.Conn(); err != nil {
		return err
	}
	query := "DELETE FROM `User` WHERE ID = ?"
	result, err := u.mysqlConn.Exec(query, id)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1451 {
			return ErrUserHasOrders
		}
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (u *UserManagerRepository) Update(user *datamodels.User) error {
	if err := u.Conn(); err != nil {
		return err
	}
	query := "UPDATE `User` SET NickName = ?,Username = ?,HashPassword = ? WHERE ID = ?"
	result, err := u.mysqlConn.Exec(query, user.NickName, user.Username, user.HashPassword, user.ID)
	if err != nil {
		if isDuplicateKeyError(err) {
			return ErrUserNameTaken
		}
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
