package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

type ctxKey int

const (
	ctxKeySession ctxKey = iota
	ctxKeyBearer
)

type Authenticator struct {
	AdminToken string
	Sessions   *SessionStore

	mu           sync.RWMutex
	passwordHash string
	// persist saves a new password hash durably. May be nil in tests.
	persist func(hash string) error

	// Secure controls whether issued cookies have the Secure attribute. Set
	// true when the request was served over TLS.
	SecureCookies func(*http.Request) bool
}

func NewAuthenticator(adminToken string, store *SessionStore) *Authenticator {
	return &Authenticator{
		AdminToken: adminToken,
		Sessions:   store,
		SecureCookies: func(r *http.Request) bool {
			return r.TLS != nil
		},
	}
}

// SetPasswordHash installs the current password hash (loaded from config) and
// the persistence callback used when the password changes.
func (a *Authenticator) SetPasswordHash(hash string, persist func(string) error) {
	a.mu.Lock()
	a.passwordHash = hash
	a.persist = persist
	a.mu.Unlock()
}

func (a *Authenticator) PasswordSet() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.passwordHash != ""
}

func (a *Authenticator) currentHash() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.passwordHash
}

// LoginRequest is the JSON body for POST /api/login. Either a password (the
// normal browser flow) or the admin token (recovery / API) is accepted.
type LoginRequest struct {
	Password string `json:"password"`
	Token    string `json:"token"`
}

// LoginResponse returns the CSRF token so SPA clients can echo it back in
// the X-CSRF-Token header on mutating requests.
type LoginResponse struct {
	CSRFToken string `json:"csrf_token"`
	ExpiresAt int64  `json:"expires_at"`
}

// AuthStatus reports whether a password has been set, so the SPA can show
// either the "set password" screen or the login screen. Public (no auth).
func (a *Authenticator) AuthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"password_set": a.PasswordSet()})
}

func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req LoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	ok := false
	switch {
	case req.Password != "" && VerifyPassword(a.currentHash(), req.Password):
		ok = true
	case req.Token != "" && ConstantTimeCompare(req.Token, a.AdminToken):
		ok = true // token recovery / API path
	case req.Password != "" && ConstantTimeCompare(req.Password, a.AdminToken):
		ok = true // allow pasting the token into the password field
	}
	if !ok {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	sess, err := a.Sessions.Create()
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	a.setSessionCookies(w, r, sess)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LoginResponse{
		CSRFToken: sess.CSRFToken,
		ExpiresAt: sess.Expires.Unix(),
	})
}

// SetPasswordRequest is the JSON body for POST /api/password.
type SetPasswordRequest struct {
	NewPassword     string `json:"new_password"`
	CurrentPassword string `json:"current_password"`
}

// SetPassword sets or changes the admin password.
//   - If no password is set yet (first run), it is allowed without auth
//     (trust-on-first-use over the LAN); the SPA shows this as the initial
//     setup screen.
//   - If a password is already set, the caller must present a valid session
//     plus the current password, or authenticate with the admin token (Bearer).
func (a *Authenticator) SetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SetPasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	if a.PasswordSet() {
		// Changing an existing password requires proof of identity.
		viaBearer := false
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			viaBearer = ConstantTimeCompare(strings.TrimPrefix(auth, "Bearer "), a.AdminToken)
		}
		if !viaBearer {
			if !VerifyPassword(a.currentHash(), req.CurrentPassword) {
				http.Error(w, "current password incorrect", http.StatusForbidden)
				return
			}
		}
	}

	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	persist := a.persist
	a.mu.Unlock()
	if persist != nil {
		if err := persist(hash); err != nil {
			http.Error(w, "failed to persist password", http.StatusInternalServerError)
			return
		}
	}
	a.mu.Lock()
	a.passwordHash = hash
	a.mu.Unlock()

	// Issue a session so the user is logged in right after first setup.
	sess, err := a.Sessions.Create()
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	a.setSessionCookies(w, r, sess)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LoginResponse{
		CSRFToken: sess.CSRFToken,
		ExpiresAt: sess.Expires.Unix(),
	})
}

func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil {
		a.Sessions.Delete(c.Value)
	}
	a.clearSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Authenticator) setSessionCookies(w http.ResponseWriter, r *http.Request, s *Session) {
	secure := a.SecureCookies(r)
	maxAge := int(s.Expires.Sub(s.Created).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    s.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
	// CSRF cookie is intentionally NOT HttpOnly so the SPA can read it and
	// echo it back in the X-CSRF-Token header (double-submit pattern).
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    s.CSRFToken,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

func (a *Authenticator) clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := a.SecureCookies(r)
	for _, name := range []string{SessionCookieName, CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == SessionCookieName,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
}

// RequireAuth allows the request through if either a valid session cookie
// is present or the Authorization: Bearer header carries the admin token.
// On success, places the session (cookie path) or "bearer" marker on the
// context.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if ConstantTimeCompare(token, a.AdminToken) {
				ctx := context.WithValue(r.Context(), ctxKeyBearer, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		c, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sess, ok := a.Sessions.Get(c.Value)
		if !ok {
			http.Error(w, "session expired", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeySession, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireCSRF enforces double-submit CSRF on mutating methods. Bearer auth
// is exempt (clients are not browsers, no cookie attack surface).
func (a *Authenticator) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutating(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if v, _ := r.Context().Value(ctxKeyBearer).(bool); v {
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := r.Context().Value(ctxKeySession).(*Session)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		hdr := r.Header.Get(CSRFHeader)
		if hdr == "" || !ConstantTimeCompare(hdr, sess.CSRFToken) {
			http.Error(w, "csrf mismatch", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// SessionFromContext returns the session attached by RequireAuth, if any.
func SessionFromContext(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(ctxKeySession).(*Session)
	return s, ok
}
