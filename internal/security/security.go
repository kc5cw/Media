package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"

	"golang.org/x/crypto/pbkdf2"
)

const (
	iterations = 210000
	saltSize   = 16
	keySize    = 32
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,64}$`)

func ValidateUsername(username string) bool {
	return usernameRegex.MatchString(username)
}

func ValidatePassword(password string) error {
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	return nil
}

func HashPassword(password string) (hash, salt []byte, err error) {
	salt = make([]byte, saltSize)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, err
	}
	hash = pbkdf2.Key([]byte(password), salt, iterations, keySize, sha256.New)
	return hash, salt, nil
}

func VerifyPassword(password string, hash, salt []byte) bool {
	derived := pbkdf2.Key([]byte(password), salt, iterations, keySize, sha256.New)
	if len(hash) != len(derived) {
		return false
	}
	return subtle.ConstantTimeCompare(hash, derived) == 1
}

func NewSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
