package aiproviders

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type OrderItem struct {
	ID       uuid.UUID `json:"id"`
	Revision int64     `json:"revision"`
}
type orderingRepository interface {
	reorder(context.Context, []Provider, uuid.UUID) error
}

func (s *Service) Reorder(ctx context.Context, order []OrderItem, actor uuid.UUID) error {
	items, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	items = s.providers(items)
	if len(order) != len(items) || len(order) > MaxChain+1 {
		return ErrConflict
	}
	byID := map[uuid.UUID]Provider{}
	for _, p := range items {
		byID[p.ID] = p
	}
	sorted := make([]Provider, 0, len(order))
	for index, item := range order {
		p, ok := byID[item.ID]
		if !ok || p.Revision != item.Revision {
			return ErrConflict
		}
		delete(byID, item.ID)
		p.Priority = (index + 1) * 10
		if p.FromEnv && p.Revision == 0 {
			if s.cipher == nil {
				return ErrEncryption
			}
			p = environmentDisplay(p)
			p.Ciphertext, err = s.cipher.Seal("environment-credential-reference", p.aad())
			if err != nil {
				return err
			}
		}
		sorted = append(sorted, p)
	}
	repository, ok := s.repo.(orderingRepository)
	if !ok {
		return ErrUnavailable
	}
	return repository.reorder(ctx, sorted, actor)
}
func (r *PostgresRepository) reorder(ctx context.Context, items []Provider, actor uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize the complete order with concurrent CRUD; no partial reorder and
	// no unnoticed new/deleted providers between validation and commit.
	if _, err = tx.Exec(ctx, `LOCK TABLE ai_providers IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return storageError(err)
	}
	rows, err := tx.Query(ctx, `SELECT id,revision FROM ai_providers`)
	if err != nil {
		return storageError(err)
	}
	revisions := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var revision int64
		if err = rows.Scan(&id, &revision); err != nil {
			rows.Close()
			return err
		}
		revisions[id] = revision
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	seen := map[uuid.UUID]bool{}
	for _, p := range items {
		seen[p.ID] = true
		if p.ID == EnvProviderID && p.Revision == 0 {
			if _, exists := revisions[p.ID]; exists {
				return ErrConflict
			}
			_, err = tx.Exec(ctx, `INSERT INTO ai_providers(id,name,endpoint,model,speaking_model,scopes,enabled,priority,timeout_seconds,key_ciphertext,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, p.ID, p.Name, p.Endpoint, p.Model, p.SpeakingModel, p.Scopes, p.Enabled, p.Priority, p.TimeoutSeconds, p.Ciphertext, actor)
		} else {
			if revisions[p.ID] != p.Revision {
				return ErrConflict
			}
			_, err = tx.Exec(ctx, `UPDATE ai_providers SET priority=$2,revision=revision+1,updated_at=now() WHERE id=$1`, p.ID, p.Priority)
		}
		if err != nil {
			return storageError(err)
		}
	}
	for id := range revisions {
		if !seen[id] && id != EnvProviderID {
			return ErrConflict
		}
	}
	return tx.Commit(ctx)
}
func (s *Service) Routing(ctx context.Context) (Routing, bool, error) {
	if s.routing == nil {
		return defaultRouting(), true, nil
	}
	p, err := s.routing.Load(ctx)
	if errors.Is(err, ErrMigrationRequired) {
		return defaultRouting(), true, nil
	}
	return p, false, err
}
func (s *Service) SaveRouting(ctx context.Context, p Routing) (Routing, error) {
	if s.routing == nil {
		return p, ErrMigrationRequired
	}
	if p.Mode != "sequential" || p.MaxParallel < 1 || p.MaxParallel > 4 || p.HedgeDelayMS < 0 || p.HedgeDelayMS > 10000 || len(p.ProviderIDs) > MaxChain+1 {
		return p, ErrValidation
	}
	if p.Revision < 1 {
		return p, ErrConflict
	}
	if p.ProviderIDs == nil {
		p.ProviderIDs = []uuid.UUID{}
	}
	items, err := s.List(ctx)
	if err != nil {
		return p, err
	}
	available := map[uuid.UUID]bool{}
	for _, item := range items {
		available[item.ID] = true
	}
	selected := map[uuid.UUID]bool{}
	for _, id := range p.ProviderIDs {
		if !available[id] || selected[id] {
			return p, ErrValidation
		}
		selected[id] = true
	}
	return s.routing.Save(ctx, p)
}
func (s *Service) Stats(ctx context.Context, days int) (Stats, error) {
	if days != 1 && days != 7 && days != 30 {
		return Stats{}, ErrValidation
	}
	empty := Stats{MigrationRequired: true, Models: []ModelStats{}, Recent: []CallMetric{}}
	if s.routing == nil {
		return empty, nil
	}
	result, err := s.routing.Stats(ctx, days)
	if errors.Is(err, ErrMigrationRequired) {
		return empty, nil
	}
	return result, err
}
