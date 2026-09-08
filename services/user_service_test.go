package services

import "testing"

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
