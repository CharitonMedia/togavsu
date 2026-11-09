package identity

import (
	"context"
	"strings"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Memory is the in-process store behind the identity repositories.
type Memory struct {
	mu        sync.Mutex
	users     map[domain.ID]domain.User
	sessions  map[domain.ID]domain.Session
	passwords map[domain.ID]domain.PasswordCredential
	totp      map[domain.ID]domain.TOTPSecret
	recovery  map[domain.ID]domain.RecoveryCode
	tokens    map[domain.ID]domain.APIToken
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{
		users:     map[domain.ID]domain.User{},
		sessions:  map[domain.ID]domain.Session{},
		passwords: map[domain.ID]domain.PasswordCredential{},
		totp:      map[domain.ID]domain.TOTPSecret{},
		recovery:  map[domain.ID]domain.RecoveryCode{},
		tokens:    map[domain.ID]domain.APIToken{},
	}
}

func (m *Memory) Users() repository.Users                   { return userStore{m} }
func (m *Memory) Sessions() repository.Sessions             { return sessionStore{m} }
func (m *Memory) Passwords() repository.PasswordCredentials { return passwordStore{m} }
func (m *Memory) TOTP() repository.TOTPSecrets              { return totpStore{m} }
func (m *Memory) Recovery() repository.RecoveryCodes        { return recoveryStore{m} }
func (m *Memory) Tokens() repository.APITokens              { return tokenStore{m} }

type userStore struct{ *Memory }
type sessionStore struct{ *Memory }
type passwordStore struct{ *Memory }
type totpStore struct{ *Memory }
type recoveryStore struct{ *Memory }
type tokenStore struct{ *Memory }

func (s userStore) Insert(_ context.Context, row domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, have := range s.users {
		if strings.EqualFold(have.Email, row.Email) {
			return repository.ErrConflict
		}
	}
	s.users[row.ID] = row
	return nil
}

func (s userStore) Update(_ context.Context, row domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[row.ID]; !ok {
		return repository.ErrNotFound
	}
	s.users[row.ID] = row
	return nil
}

func (s userStore) Get(_ context.Context, id domain.ID) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.users[id]
	if !ok {
		return domain.User{}, repository.ErrNotFound
	}
	return row, nil
}

func (s userStore) GetByEmail(_ context.Context, email string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.users {
		if strings.EqualFold(row.Email, email) {
			return row, nil
		}
	}
	return domain.User{}, repository.ErrNotFound
}

func (s userStore) List(_ context.Context, _ repository.Filter) (repository.Page[domain.User], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []domain.User
	for _, row := range s.users {
		items = append(items, row)
	}
	return repository.Page[domain.User]{Items: items}, nil
}

func (s sessionStore) Insert(_ context.Context, row domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[row.ID] = row
	return nil
}

func (s sessionStore) Update(_ context.Context, row domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[row.ID]; !ok {
		return repository.ErrNotFound
	}
	s.sessions[row.ID] = row
	return nil
}

func (s sessionStore) Get(_ context.Context, id domain.ID) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[id]
	if !ok {
		return domain.Session{}, repository.ErrNotFound
	}
	return row, nil
}

func (s sessionStore) GetByHash(_ context.Context, hash string) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.sessions {
		if row.TokenHash == hash {
			return row, nil
		}
	}
	return domain.Session{}, repository.ErrNotFound
}

func (s sessionStore) List(_ context.Context, filter repository.Filter) (repository.Page[domain.Session], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []domain.Session
	for _, row := range s.sessions {
		if filter.UserID != "" && row.UserID != filter.UserID {
			continue
		}
		items = append(items, row)
	}
	return repository.Page[domain.Session]{Items: items}, nil
}

func (s passwordStore) Insert(_ context.Context, row domain.PasswordCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passwords[row.UserID] = row
	return nil
}

func (s passwordStore) Update(_ context.Context, row domain.PasswordCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.passwords[row.UserID]; !ok {
		return repository.ErrNotFound
	}
	s.passwords[row.UserID] = row
	return nil
}

func (s passwordStore) GetByUser(_ context.Context, userID domain.ID) (domain.PasswordCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.passwords[userID]
	if !ok {
		return domain.PasswordCredential{}, repository.ErrNotFound
	}
	return row, nil
}

func (s totpStore) Insert(_ context.Context, row domain.TOTPSecret) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totp[row.UserID] = row
	return nil
}

func (s totpStore) Update(_ context.Context, row domain.TOTPSecret) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.totp[row.UserID]; !ok {
		return repository.ErrNotFound
	}
	s.totp[row.UserID] = row
	return nil
}

func (s totpStore) GetByUser(_ context.Context, userID domain.ID) (domain.TOTPSecret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.totp[userID]
	if !ok {
		return domain.TOTPSecret{}, repository.ErrNotFound
	}
	return row, nil
}

func (s recoveryStore) Insert(_ context.Context, row domain.RecoveryCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recovery[row.ID] = row
	return nil
}

func (s recoveryStore) Update(_ context.Context, row domain.RecoveryCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.recovery[row.ID]; !ok {
		return repository.ErrNotFound
	}
	s.recovery[row.ID] = row
	return nil
}

func (s recoveryStore) List(_ context.Context, filter repository.Filter) (repository.Page[domain.RecoveryCode], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []domain.RecoveryCode
	for _, row := range s.recovery {
		if filter.UserID != "" && row.UserID != filter.UserID {
			continue
		}
		items = append(items, row)
	}
	return repository.Page[domain.RecoveryCode]{Items: items}, nil
}

func (s tokenStore) Insert(_ context.Context, row domain.APIToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[row.ID] = row
	return nil
}

func (s tokenStore) Update(_ context.Context, row domain.APIToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[row.ID]; !ok {
		return repository.ErrNotFound
	}
	s.tokens[row.ID] = row
	return nil
}

func (s tokenStore) Get(_ context.Context, id domain.ID) (domain.APIToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.tokens[id]
	if !ok {
		return domain.APIToken{}, repository.ErrNotFound
	}
	return row, nil
}

func (s tokenStore) GetByHash(_ context.Context, hash string) (domain.APIToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.tokens {
		if row.TokenHash == hash {
			return row, nil
		}
	}
	return domain.APIToken{}, repository.ErrNotFound
}

func (s tokenStore) List(_ context.Context, filter repository.Filter) (repository.Page[domain.APIToken], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []domain.APIToken
	for _, row := range s.tokens {
		if filter.UserID != "" && row.UserID != filter.UserID {
			continue
		}
		items = append(items, row)
	}
	return repository.Page[domain.APIToken]{Items: items}, nil
}
