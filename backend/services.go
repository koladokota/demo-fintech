package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"strings"
)

type Service struct {
	repo        *Repository
	demoCredits bool
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func hashToken(token string) string {
	s := sha256.Sum256([]byte(token))
	return hex.EncodeToString(s[:])
}
func (s *Service) Register(ctx context.Context, req AuthRequest) (AuthResponse, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Name == "" || !strings.Contains(req.Email, "@") || len(req.Password) < 8 {
		return AuthResponse{}, errInvalidInput
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return AuthResponse{}, err
	}
	id, err := randomToken()
	if err != nil {
		return AuthResponse{}, err
	}
	u, err := s.repo.CreateUser(ctx, id, req.Name, req.Email, string(hash))
	if err != nil {
		return AuthResponse{}, err
	}
	return s.withSession(ctx, u)
}
func (s *Service) Login(ctx context.Context, req AuthRequest) (AuthResponse, error) {
	u, hash, err := s.repo.FindUserByEmail(ctx, strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return AuthResponse{}, errUnauthorized
	}
	return s.withSession(ctx, u)
}
func (s *Service) withSession(ctx context.Context, u User) (AuthResponse, error) {
	token, err := randomToken()
	if err != nil {
		return AuthResponse{}, err
	}
	if err = s.repo.CreateSession(ctx, u.ID, hashToken(token)); err != nil {
		return AuthResponse{}, err
	}
	return AuthResponse{Token: token, User: u}, nil
}
func (s *Service) User(ctx context.Context, token string) (User, error) {
	return s.repo.UserByToken(ctx, hashToken(token))
}
func (s *Service) AddDemo(ctx context.Context, userID string) (int64, error) {
	if !s.demoCredits {
		return 0, errDemoDisabled
	}
	return s.repo.ChangeBalance(ctx, userID, 1000, "demo-add", "Тестовое начисление")
}
func (s *Service) SpendDemo(ctx context.Context, userID string) (int64, error) {
	return s.repo.ChangeBalance(ctx, userID, -250, "demo-spend", "Тестовое списание")
}

var errInvalidInput = errors.New("invalid input")
var errUnauthorized = errors.New("unauthorized")
var errDemoDisabled = errors.New("demo credits disabled")
