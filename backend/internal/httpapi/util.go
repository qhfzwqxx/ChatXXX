package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

func randomID() string {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return strconv.FormatInt(int64(len(bytes)), 10)
	}
	return hex.EncodeToString(bytes[:])
}

func hashPassword(password string) (string, error) {
	out, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(out), err
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func paramID(r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, name))
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}
