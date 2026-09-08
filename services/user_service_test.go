package services

import (
	"errors"
	"testing"

	"product/datamodels"
	"product/repositories"
)

type userRepositoryMock struct {
	selected  *datamodels.User
	selectErr error
	insertErr error
	updateErr error
	updated   *datamodels.User
	inserted  *datamodels.User
	deleteErr error
	all       []*datamodels.User
}

func (m *userRepositoryMock) Conn() error { return nil }
func (m *userRepositoryMock) Select(string) (*datamodels.User, error) {
	if m.selectErr != nil {
		return &datamodels.User{}, m.selectErr
	}
	return m.selected, nil
}
func (m *userRepositoryMock) Insert(user *datamodels.User) (int64, error) {
	m.inserted = user
	return 7, m.insertErr
}
func (m *userRepositoryMock) SelectAll() ([]*datamodels.User, error) { return m.all, nil }
func (m *userRepositoryMock) SelectById(int64) (*datamodels.User, error) {
	return m.selected, m.selectErr
}
func (m *userRepositoryMock) DeleteById(int64) error { return m.deleteErr }
func (m *userRepositoryMock) Update(user *datamodels.User) error {
	copy := *user
	m.updated = &copy
	return m.updateErr
}

func TestPasswordHashIsBcryptAndVerifies(t *testing.T) {
	hash := EncodePassword("demo123456")
	if len(hash) < 4 || hash[:4] != "$2a$" && hash[:4] != "$2b$" && hash[:4] != "$2y$" {
		t.Fatalf("expected bcrypt hash, got %q", hash)
	}
	if !ValidatePassword("demo123456", hash) {
		t.Fatal("password should verify")
	}
	if ValidatePassword("wrong-password", hash) {
		t.Fatal("wrong password should fail")
	}
}

func TestUserServiceRegistrationAndDuplicateHandling(t *testing.T) {
	repo := &userRepositoryMock{selectErr: repositories.ErrUserNotFound}
	svc := NewUserService(repo)
	user := &datamodels.User{Username: "new-user", HashPassword: "secret123"}
	id, err := svc.AddUser(user)
	if err != nil || id != 7 {
		t.Fatalf("AddUser id=%d err=%v", id, err)
	}
	if repo.inserted == nil || !ValidatePassword("secret123", repo.inserted.HashPassword) {
		t.Fatal("registered password was not stored as bcrypt")
	}

	repo = &userRepositoryMock{selected: &datamodels.User{ID: 1}}
	svc = NewUserService(repo)
	if _, err := svc.AddUser(&datamodels.User{Username: "existing", HashPassword: "secret123"}); !errors.Is(err, ErrUserNameExists) {
		t.Fatalf("expected duplicate username error, got %v", err)
	}
}

func TestUserServiceLoginUpgradesLegacyHash(t *testing.T) {
	legacy := EncodePasswordLegacy("demo123456")
	repo := &userRepositoryMock{selected: &datamodels.User{ID: 1, Username: "demo", HashPassword: legacy}}
	svc := NewUserService(repo)
	if _, ok := svc.IsPwdSuccess("demo", "demo123456"); !ok {
		t.Fatal("legacy password should authenticate")
	}
	if repo.updated == nil || IsLegacyPasswordHash(repo.updated.HashPassword) || !ValidatePassword("demo123456", repo.updated.HashPassword) {
		t.Fatal("legacy password was not upgraded to bcrypt")
	}
	if _, ok := svc.IsPwdSuccess("demo", "wrong-password"); ok {
		t.Fatal("wrong password should not authenticate")
	}
}

func TestUserServiceCRUDDelegation(t *testing.T) {
	user := &datamodels.User{ID: 3, HashPassword: "new-password"}
	repo := &userRepositoryMock{selected: user, all: []*datamodels.User{user}}
	svc := NewUserService(repo)
	if got, err := svc.GetUserById(3); err != nil || got.ID != 3 {
		t.Fatalf("GetUserById got=%+v err=%v", got, err)
	}
	if got, err := svc.GetAllUsers(); err != nil || len(got) != 1 {
		t.Fatalf("GetAllUsers got=%v err=%v", got, err)
	}
	if err := svc.DeleteUserById(3); err != nil {
		t.Fatal("DeleteUserById was not delegated")
	}
	if err := svc.UpdateUser(user); err != nil || repo.updated == nil || !ValidatePassword("new-password", repo.updated.HashPassword) {
		t.Fatalf("UpdateUser err=%v updated=%+v", err, repo.updated)
	}
}

func TestUserServiceRejectsInvalidPasswordLength(t *testing.T) {
	repo := &userRepositoryMock{selectErr: repositories.ErrUserNotFound}
	svc := NewUserService(repo)
	if _, err := svc.AddUser(&datamodels.User{Username: "short", HashPassword: "123"}); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected invalid password error, got %v", err)
	}
	if err := svc.UpdateUser(&datamodels.User{ID: 1, Username: "long", HashPassword: string(make([]byte, 73))}); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected invalid password error, got %v", err)
	}
}
