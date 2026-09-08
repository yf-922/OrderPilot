package common

import "testing"

func TestIssueAndVerifyToken(t *testing.T) {
	token := IssueToken(42)
	if got, ok := UserIDFromToken(token); !ok || got != 42 {
		t.Fatalf("token verification failed: got=%d ok=%v", got, ok)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	token := IssueToken(42) + "x"
	if _, ok := UserIDFromToken(token); ok {
		t.Fatal("tampered token should be rejected")
	}
}
