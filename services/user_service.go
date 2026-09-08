package services

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"product/datamodels"
	"product/repositories"

	"golang.org/x/crypto/bcrypt"
)

// IUserService 定义用户业务层能力。
type IUserService interface {
	IsPwdSuccess(userName string, pwd string) (user *datamodels.User, isok bool)
	AddUser(user *datamodels.User) (userId int64, err error)
	GetAllUsers() ([]*datamodels.User, error)
	GetUserById(userId int64) (*datamodels.User, error)
	DeleteUserById(userId int64) bool
	UpdateUser(user *datamodels.User) error
}

// NewUserService 创建用户业务层实例。
func NewUserService(repository repositories.IUserRepository) IUserService {
	return &UserService{repository}
}

// UserService 负责用户登录校验和新增用户逻辑。
type UserService struct {
	UserRepository repositories.IUserRepository
}

// IsPwdSuccess 按用户名查询用户，并校验明文密码和数据库哈希值是否一致。
func (u *UserService) IsPwdSuccess(userName string, pwd string) (user *datamodels.User, isok bool) {
	user, err := u.UserRepository.Select(userName)
	if err != nil {
		return &datamodels.User{}, false
	}

	if !ValidatePassword(pwd, user.HashPassword) {
		return user, false
	}

	return user, true
}

// AddUser 在写入数据库前先将用户密码做哈希处理。
func (u *UserService) AddUser(user *datamodels.User) (userId int64, err error) {
	_, err = u.UserRepository.Select(user.Username)
	if err == nil {
		return 0, errors.New("userName already exists")
	}
	if !errors.Is(err, repositories.ErrUserNotFound) {
		return 0, err
	}
	user.HashPassword = EncodePassword(user.HashPassword)
	return u.UserRepository.Insert(user)
}

func (u *UserService) GetAllUsers() ([]*datamodels.User, error) {
	return u.UserRepository.SelectAll()
}

func (u *UserService) GetUserById(userId int64) (*datamodels.User, error) {
	return u.UserRepository.SelectById(userId)
}

func (u *UserService) DeleteUserById(userId int64) bool {
	return u.UserRepository.DeleteById(userId)
}

func (u *UserService) UpdateUser(user *datamodels.User) error {
	user.HashPassword = EncodePassword(user.HashPassword)
	return u.UserRepository.Update(user)
}

// ValidatePassword 使用相同哈希规则校验用户输入密码是否匹配。
func ValidatePassword(userPassword string, hashed string) bool {
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(userPassword)); err == nil {
		return true
	}
	return EncodePasswordLegacy(userPassword) == hashed
}

// EncodePassword 使用 SHA-256 对明文密码做哈希编码。
func EncodePassword(password string) string {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return ""
	}
	return string(hashed)
}

func EncodePasswordLegacy(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}
