package rules

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

type Service struct {
	validator *Validator
	repo      *Repository
	accountID string
}

func NewService(validator *Validator, repo *Repository, accountID string) *Service {
	return &Service{validator: validator, repo: repo, accountID: accountID}
}

func (s *Service) Validate(raw []byte) (ValidationResult, error) {
	return s.validator.ParseAndValidate(raw)
}

func (s *Service) Create(ctx context.Context, raw []byte) (Version, error) {
	result, err := s.Validate(raw)
	if err != nil {
		return Version{}, err
	}
	return s.repo.Save(ctx, s.accountID, raw, result)
}

func (s *Service) Bootstrap(
	ctx context.Context,
	path, handle, timezone string,
) (Version, error) {
	if err := s.repo.EnsureAccount(ctx, s.accountID, handle, timezone); err != nil {
		return Version{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Version{}, fmt.Errorf("read bootstrap rules: %w", err)
	}
	result, err := s.Validate(raw)
	if err != nil {
		return Version{}, fmt.Errorf("validate bootstrap rules: %w", err)
	}
	existing, err := s.repo.ByHash(ctx, s.accountID, result.ConfigHash)
	switch {
	case err == nil:
		if _, err := s.repo.ActivateDue(ctx, time.Now()); err != nil {
			return Version{}, err
		}
		if existing.State == "validated" {
			if err := s.repo.Activate(ctx, existing.ID, existing.EffectiveAt); err != nil {
				return Version{}, err
			}
		}
		versions, err := s.repo.List(ctx, s.accountID)
		if err != nil {
			return Version{}, err
		}
		for _, version := range versions {
			if version.ID == existing.ID {
				return version, nil
			}
		}
		return existing, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Version{}, fmt.Errorf("find bootstrap rule: %w", err)
	}

	version, err := s.repo.Save(ctx, s.accountID, raw, result)
	if err != nil {
		return Version{}, err
	}
	if err := s.repo.Activate(ctx, version.ID, version.EffectiveAt); err != nil {
		return Version{}, err
	}
	versions, err := s.repo.List(ctx, s.accountID)
	if err != nil {
		return Version{}, err
	}
	for _, current := range versions {
		if current.ID == version.ID {
			return current, nil
		}
	}
	return version, nil
}

func (s *Service) Activate(ctx context.Context, versionID string, at time.Time) error {
	return s.repo.Activate(ctx, versionID, at)
}

func (s *Service) Rollback(ctx context.Context, versionID string) error {
	return s.repo.Activate(ctx, versionID, time.Now())
}

func (s *Service) List(ctx context.Context) ([]Version, error) {
	return s.repo.List(ctx, s.accountID)
}

func (s *Service) Active(ctx context.Context) (Version, error) {
	return s.repo.Active(ctx, s.accountID)
}
