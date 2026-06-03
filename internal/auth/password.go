package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 8
	bcryptCost     = 12
)

// ErrPasswordTooShort is returned when a new password is below the minimum.
var ErrPasswordTooShort = errors.New("password too short")

// HashPassword returns a bcrypt hash for the given password.
func HashPassword(password string) (string, error) {
	if len(password) < minPasswordLen {
		return "", ErrPasswordTooShort
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword reports whether password matches the stored bcrypt hash.
func VerifyPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
