package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "test-admin-token-1234567890"

func newAuthForTest() *Authenticator {
	return NewAuthenticator(testToken, NewSessionStore(time.Hour))
}

func TestLogin_Success_SetsCookiesAndReturnsCSRF(t *testing.T) {
	a := newAuthForTest()
	body, _ := json.Marshal(LoginRequest{Token: testToken})
	r := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	w := httptest.NewRecorder()

	a.Login(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.CSRFToken == "" {
		t.Error("missing CSRF token in response")
	}
	cookies := w.Result().Cookies()
	sawSession, sawCSRF := false, false
	for _, c := range cookies {
		switch c.Name {
		case SessionCookieName:
			sawSession = true
			if !c.HttpOnly {
				t.Error("session cookie must be HttpOnly")
			}
		case CSRFCookieName:
			sawCSRF = true
			if c.HttpOnly {
				t.Error("CSRF cookie must NOT be HttpOnly (SPA needs to read it)")
			}
			if c.Value != resp.CSRFToken {
				t.Error("CSRF cookie/body mismatch")
			}
		}
	}
	if !sawSession || !sawCSRF {
		t.Errorf("missing cookies, got %d", len(cookies))
	}
}

func TestLogin_BadToken(t *testing.T) {
	a := newAuthForTest()
	body, _ := json.Marshal(LoginRequest{Token: "wrong"})
	r := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Login(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireAuth_NoCookie_Rejected(t *testing.T) {
	a := newAuthForTest()
	called := false
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called {
		t.Error("handler must not be called")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d", w.Code)
	}
}

func TestRequireAuth_ValidSession_Allows(t *testing.T) {
	a := newAuthForTest()
	sess, _ := a.Sessions.Create()
	called := false
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if s, ok := SessionFromContext(r.Context()); !ok || s.ID != sess.ID {
			t.Error("session not in context")
		}
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called {
		t.Error("handler not called")
	}
}

func TestRequireAuth_BearerToken(t *testing.T) {
	a := newAuthForTest()
	called := false
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called {
		t.Errorf("handler not called, status %d body %s", w.Code, w.Body.String())
	}
}

func TestRequireAuth_BadBearer_Rejected(t *testing.T) {
	a := newAuthForTest()
	h := a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d", w.Code)
	}
}

func TestRequireCSRF_PostWithoutHeader_Blocked(t *testing.T) {
	a := newAuthForTest()
	sess, _ := a.Sessions.Create()
	chain := a.RequireAuth(a.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	r := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader("{}"))
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRequireCSRF_PostWithHeader_Allowed(t *testing.T) {
	a := newAuthForTest()
	sess, _ := a.Sessions.Create()
	called := false
	chain := a.RequireAuth(a.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })))
	r := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader("{}"))
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	r.Header.Set(CSRFHeader, sess.CSRFToken)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, r)
	if !called {
		t.Errorf("blocked, code %d", w.Code)
	}
}

func TestRequireCSRF_BearerBypassesCSRF(t *testing.T) {
	a := newAuthForTest()
	called := false
	chain := a.RequireAuth(a.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })))
	r := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, r)
	if !called {
		t.Errorf("bearer should bypass CSRF, code %d", w.Code)
	}
}

func TestLogout_DeletesSession(t *testing.T) {
	a := newAuthForTest()
	sess, _ := a.Sessions.Create()
	r := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	w := httptest.NewRecorder()
	a.Logout(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status %d", w.Code)
	}
	if _, ok := a.Sessions.Get(sess.ID); ok {
		t.Error("session not deleted")
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	store := NewSessionStore(time.Minute)
	fakeNow := time.Now()
	store.now = func() time.Time { return fakeNow }
	sess, _ := store.Create()
	if _, ok := store.Get(sess.ID); !ok {
		t.Error("expected valid session")
	}
	fakeNow = fakeNow.Add(2 * time.Minute)
	if _, ok := store.Get(sess.ID); ok {
		t.Error("expected expired session")
	}
}
