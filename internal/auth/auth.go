package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
)

type contextKey string

const (
	cookieName     = "bulk_loader_session"
	cookieMaxAge   = 24 * 60 * 60
	apiKeyHeader   = "X-API-Key"
	contextUserKey = contextKey("authenticated")
)

var (
	ErrNotConfigured     = errors.New("passphrase not configured")
	ErrInvalidPassword   = errors.New("invalid passphrase")
	ErrAlreadyConfigured = errors.New("passphrase already configured")
)

type Service struct {
	db                     *database.DB
	cfg                    *config.Config
	encryptionKey          []byte
	encryptionSalt         []byte
	onCredentialsReady     func()
	credentialsReadyCalled bool
}

func (s *Service) cookieSecure() bool {
	return !s.cfg.DevMode
}

func (s *Service) OnCredentialsReady(callback func()) {
	s.onCredentialsReady = callback
	if s.encryptionKey != nil && !s.credentialsReadyCalled {
		s.credentialsReadyCalled = true
		callback()
	}
}

func New(db *database.DB, cfg *config.Config) *Service {
	s := &Service{db: db, cfg: cfg}
	if cfg.Passphrase != "" {
		_ = s.setupFromEnv()
	}
	_ = s.loadEncryptionKey()
	return s
}

func (s *Service) setupFromEnv() error {
	saltStr, err := s.db.GetSetting(database.SettingPassphraseSalt)
	var salt []byte
	if err != nil {
		salt, err = GenerateSalt()
		if err != nil {
			return err
		}
		saltStr = base64.StdEncoding.EncodeToString(salt)
		if err := s.db.SetSetting(database.SettingPassphraseSalt, saltStr); err != nil {
			return err
		}
	} else {
		salt, _ = base64.StdEncoding.DecodeString(saltStr)
	}

	hash := HashPassphrase(s.cfg.Passphrase, salt)
	if err := s.db.SetSetting(database.SettingPassphraseHash, hash); err != nil {
		return err
	}

	_, err = s.db.GetSetting(database.SettingEncryptionSalt)
	if err != nil {
		encSalt, err := GenerateSalt()
		if err != nil {
			return err
		}
		if err := s.db.SetSetting(database.SettingEncryptionSalt, base64.StdEncoding.EncodeToString(encSalt)); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) loadEncryptionKey() error {
	if s.cfg.Passphrase == "" {
		return ErrNotConfigured
	}
	return s.loadEncryptionKeyFromPassphrase(s.cfg.Passphrase)
}

func (s *Service) loadEncryptionKeyFromPassphrase(passphrase string) error {
	saltStr, err := s.db.GetSetting(database.SettingEncryptionSalt)
	if err != nil {
		return err
	}
	salt, err := base64.StdEncoding.DecodeString(saltStr)
	if err != nil {
		return err
	}
	s.encryptionSalt = salt
	s.encryptionKey = DeriveKey(passphrase, salt)
	return nil
}

func (s *Service) IsConfigured() bool {
	return s.db.HasSetting(database.SettingPassphraseHash)
}

func (s *Service) Setup(passphrase string) error {
	if s.IsConfigured() {
		return ErrAlreadyConfigured
	}

	salt, err := GenerateSalt()
	if err != nil {
		return err
	}

	if err := s.db.SetSetting(database.SettingPassphraseSalt, base64.StdEncoding.EncodeToString(salt)); err != nil {
		return err
	}

	if err := s.db.SetSetting(database.SettingPassphraseHash, HashPassphrase(passphrase, salt)); err != nil {
		return err
	}

	encSalt, err := GenerateSalt()
	if err != nil {
		return err
	}
	if err := s.db.SetSetting(database.SettingEncryptionSalt, base64.StdEncoding.EncodeToString(encSalt)); err != nil {
		return err
	}

	s.encryptionSalt = encSalt
	s.encryptionKey = DeriveKey(passphrase, encSalt)
	return nil
}

func (s *Service) Validate(passphrase string) bool {
	if s.cfg.Passphrase != "" {
		return subtle.ConstantTimeCompare([]byte(passphrase), []byte(s.cfg.Passphrase)) == 1
	}

	saltStr, err := s.db.GetSetting(database.SettingPassphraseSalt)
	if err != nil {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(saltStr)
	if err != nil {
		return false
	}
	storedHash, err := s.db.GetSetting(database.SettingPassphraseHash)
	if err != nil {
		return false
	}
	return VerifyPassphrase(passphrase, salt, storedHash)
}

func (s *Service) Login(w http.ResponseWriter, passphrase string) error {
	if !s.Validate(passphrase) {
		return ErrInvalidPassword
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    base64.StdEncoding.EncodeToString([]byte(passphrase)),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge,
	})
	return nil
}

func (s *Service) Logout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Public routes that don't require authentication
		path := r.URL.Path
		if path == "/api/health" || path == "/api/auth/status" || path == "/api/auth/setup" || path == "/api/auth/login" {
			next.ServeHTTP(w, r)
			return
		}

		if apiKey := r.Header.Get(apiKeyHeader); apiKey != "" {
			if s.Validate(apiKey) {
				s.ensureEncryptionKey(apiKey)
				ctx := context.WithValue(r.Context(), contextUserKey, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		cookie, err := r.Cookie(cookieName)
		if err == nil {
			passphrase, err := base64.StdEncoding.DecodeString(cookie.Value)
			if err == nil && s.Validate(string(passphrase)) {
				s.ensureEncryptionKey(string(passphrase))
				ctx := context.WithValue(r.Context(), contextUserKey, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

func (s *Service) ensureEncryptionKey(passphrase string) {
	if s.encryptionKey == nil {
		if err := s.loadEncryptionKeyFromPassphrase(passphrase); err == nil {
			if s.onCredentialsReady != nil && !s.credentialsReadyCalled {
				s.credentialsReadyCalled = true
				s.onCredentialsReady()
			}
		}
	}
}

func IsAuthenticated(ctx context.Context) bool {
	auth, ok := ctx.Value(contextUserKey).(bool)
	return ok && auth
}

func (s *Service) CheckAuthentication(r *http.Request) bool {
	if apiKey := r.Header.Get(apiKeyHeader); apiKey != "" && s.Validate(apiKey) {
		return true
	}
	cookie, err := r.Cookie(cookieName)
	if err == nil {
		passphrase, err := base64.StdEncoding.DecodeString(cookie.Value)
		if err == nil && s.Validate(string(passphrase)) {
			return true
		}
	}
	return false
}

func (s *Service) EncryptCredentials(plaintext []byte) ([]byte, error) {
	if s.encryptionKey == nil {
		return nil, ErrNotConfigured
	}
	return Encrypt(plaintext, s.encryptionKey)
}

func (s *Service) DecryptCredentials(ciphertext []byte) ([]byte, error) {
	if s.encryptionKey == nil {
		return nil, ErrNotConfigured
	}
	return Decrypt(ciphertext, s.encryptionKey)
}
