package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHashVerifyPassword(t *testing.T) {
	if _, err := HashPassword("short"); err != ErrPasswordTooShort {
		t.Errorf("expected too-short error, got %v", err)
	}
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse") {
		t.Error("expected match")
	}
	if VerifyPassword(hash, "wrong") {
		t.Error("unexpected match")
	}
	if VerifyPassword("", "anything") {
		t.Error("empty hash must never match")
	}
}

func authWithPassword(t *testing.T, password string) *Authenticator {
	t.Helper()
	a := NewAuthenticator(testToken, NewSessionStore(time.Hour))
	var stored string
	a.SetPasswordHash("", func(h string) error { stored = h; return nil })
	if password != "" {
		hash, _ := HashPassword(password)
		a.SetPasswordHash(hash, func(h string) error { stored = h; return nil })
	}
	_ = stored
	return a
}

func TestAuthStatus(t *testing.T) {
	a := authWithPassword(t, "")
	r := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	w := httptest.NewRecorder()
	a.AuthStatus(w, r)
	var s map[string]bool
	json.Unmarshal(w.Body.Bytes(), &s)
	if s["password_set"] {
		t.Error("expected password_set=false")
	}

	a2 := authWithPassword(t, "hunter2hunter")
	w2 := httptest.NewRecorder()
	a2.AuthStatus(w2, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	json.Unmarshal(w2.Body.Bytes(), &s)
	if !s["password_set"] {
		t.Error("expected password_set=true")
	}
}

func TestSetPassword_FirstRun_NoAuthNeeded(t *testing.T) {
	a := authWithPassword(t, "")
	body, _ := json.Marshal(SetPasswordRequest{NewPassword: "my-strong-pass"})
	r := httptest.NewRequest(http.MethodPost, "/api/password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.SetPassword(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !a.PasswordSet() {
		t.Error("password should be set now")
	}
	// session cookie issued
	if len(w.Result().Cookies()) == 0 {
		t.Error("expected session cookie after first set")
	}
}

func TestSetPassword_FirstRun_TooShort(t *testing.T) {
	a := authWithPassword(t, "")
	body, _ := json.Marshal(SetPasswordRequest{NewPassword: "short"})
	w := httptest.NewRecorder()
	a.SetPassword(w, httptest.NewRequest(http.MethodPost, "/api/password", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSetPassword_Change_RequiresCurrent(t *testing.T) {
	a := authWithPassword(t, "old-password-x")
	// Wrong current → 403
	body, _ := json.Marshal(SetPasswordRequest{NewPassword: "new-password-y", CurrentPassword: "nope"})
	w := httptest.NewRecorder()
	a.SetPassword(w, httptest.NewRequest(http.MethodPost, "/api/password", bytes.NewReader(body)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	// Correct current → ok
	body2, _ := json.Marshal(SetPasswordRequest{NewPassword: "new-password-y", CurrentPassword: "old-password-x"})
	w2 := httptest.NewRecorder()
	a.SetPassword(w2, httptest.NewRequest(http.MethodPost, "/api/password", bytes.NewReader(body2)))
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w2.Code, w2.Body.String())
	}
	if !VerifyPassword(a.currentHash(), "new-password-y") {
		t.Error("password not updated")
	}
}

func TestSetPassword_Change_BearerBypassesCurrent(t *testing.T) {
	a := authWithPassword(t, "old-password-x")
	body, _ := json.Marshal(SetPasswordRequest{NewPassword: "new-password-z"})
	r := httptest.NewRequest(http.MethodPost, "/api/password", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	a.SetPassword(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 via bearer, got %d", w.Code)
	}
}

func TestLogin_ByPassword(t *testing.T) {
	a := authWithPassword(t, "login-with-this")
	body, _ := json.Marshal(LoginRequest{Password: "login-with-this"})
	w := httptest.NewRecorder()
	a.Login(w, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
}

func TestLogin_TokenRecovery(t *testing.T) {
	a := authWithPassword(t, "some-password")
	// Login with token in the token field
	body, _ := json.Marshal(LoginRequest{Token: testToken})
	w := httptest.NewRecorder()
	a.Login(w, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("token recovery failed: %d", w.Code)
	}
	// Login with token pasted into password field
	body2, _ := json.Marshal(LoginRequest{Password: testToken})
	w2 := httptest.NewRecorder()
	a.Login(w2, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body2)))
	if w2.Code != http.StatusOK {
		t.Fatalf("token-as-password failed: %d", w2.Code)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	a := authWithPassword(t, "right-password")
	body, _ := json.Marshal(LoginRequest{Password: "wrong-password"})
	w := httptest.NewRecorder()
	a.Login(w, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body)))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
