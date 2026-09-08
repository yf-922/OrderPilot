package repositories

import (
	"errors"
	"regexp"
	"testing"

	"product/datamodels"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
)

func TestUserRepositorySelectAndNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewUserRepository("ignored", db)
	query := regexp.QuoteMeta("SELECT * FROM `User` WHERE Username = ?")
	mock.ExpectQuery(query).WithArgs("demo").WillReturnRows(sqlmock.NewRows([]string{"ID", "NickName", "Username", "HashPassword"}).AddRow(1, "Demo", "demo", "hash"))
	user, err := repo.Select("demo")
	if err != nil || user.ID != 1 || user.Username != "demo" {
		t.Fatalf("Select got=%+v err=%v", user, err)
	}
	mock.ExpectQuery(query).WithArgs("missing").WillReturnRows(sqlmock.NewRows([]string{"ID", "NickName", "Username", "HashPassword"}))
	if _, err := repo.Select("missing"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserRepositoryMapsDuplicateAndMissingUpdate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewUserRepository("ignored", db)
	user := &datamodels.User{ID: 2, NickName: "Name", Username: "taken", HashPassword: "hash"}
	insert := regexp.QuoteMeta("INSERT INTO `User` (NickName,Username,HashPassword) VALUES (?, ?, ?)")
	mock.ExpectExec(insert).WithArgs(user.NickName, user.Username, user.HashPassword).WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate"})
	if _, err := repo.Insert(user); !errors.Is(err, ErrUserNameTaken) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	update := regexp.QuoteMeta("UPDATE `User` SET NickName = ?,Username = ?,HashPassword = ? WHERE ID = ?")
	mock.ExpectExec(update).WithArgs(user.NickName, user.Username, user.HashPassword, user.ID).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.Update(user); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected missing user error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
