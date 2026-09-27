package attempts

import (
	"context"

	"github.com/google/uuid"
)

// Mistakes returns all submitted attempts relevant to the mistakes page in one
// API response. It keeps the detailed grading and AI feedback private to the
// server, avoiding an HTTP request per historical attempt in the client.
//
// The page needs the review of every submitted attempt at once, so this path
// deliberately uses bulk lookups: answers, material structures and AI feedback
// are each loaded with one query per material type (plus one query per nesting
// level of a reading test) instead of a fan-out of queries per attempt.
func (s *Service) Mistakes(ctx context.Context, userID uuid.UUID) ([]MistakeReport, error) {
	attempts, err := s.repository.ListByUser(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	submitted := make([]Summary, 0, len(attempts))
	for _, attempt := range attempts {
		// An attempt that is still in progress has no review to show yet.
		if attempt.Status == StatusSubmitted {
			submitted = append(submitted, attempt)
		}
	}
	if len(submitted) == 0 {
		return []MistakeReport{}, nil
	}
	attemptIDs := make([]uuid.UUID, 0, len(submitted))
	for _, attempt := range submitted {
		attemptIDs = append(attemptIDs, attempt.ID)
	}
	answers, err := s.repository.ListAnswersByAttempts(ctx, attemptIDs)
	if err != nil {
		return nil, err
	}
	materials, err := s.gradingMaterials(ctx, submitted)
	if err != nil {
		return nil, err
	}
	writing, err := s.writingEvaluations(ctx, submitted)
	if err != nil {
		return nil, err
	}
	speaking, err := s.speakingEvaluations(ctx, submitted)
	if err != nil {
		return nil, err
	}
	reports := make([]MistakeReport, 0, len(submitted))
	for _, attempt := range submitted {
		report := MistakeReport{
			Attempt:            attempt,
			WritingEvaluation:  writing[attempt.ID],
			SpeakingEvaluation: speaking[attempt.ID],
		}
		for _, item := range reviewFromMaterial(materials[attempt.ID], answers[attempt.ID]) {
			if !item.IsCorrect {
				report.Review = append(report.Review, item)
			}
		}
		if len(report.Review) > 0 || report.WritingEvaluation != nil || report.SpeakingEvaluation != nil {
			reports = append(reports, report)
		}
	}
	return reports, nil
}

// objectiveMaterial reports whether mistakes of this skill come from a
// question-by-question review. Writing and Speaking are reviewed through their
// AI evaluation instead and never load a grading structure, so a missing
// provider for them must not fail the page.
func objectiveMaterial(materialType string) bool {
	return materialType == MaterialListening || materialType == MaterialReading
}

// gradingMaterials loads the review material of every attempt, fetching each
// distinct pinned version once and reusing versions loaded moments ago.
func (s *Service) gradingMaterials(ctx context.Context, attempts []Summary) (map[uuid.UUID]GradingMaterial, error) {
	type materialKey struct {
		materialType string
		ref          MaterialRef
	}
	byKey := map[materialKey][]uuid.UUID{}
	for _, attempt := range attempts {
		if !objectiveMaterial(attempt.MaterialType) {
			continue
		}
		key := materialKey{
			materialType: attempt.MaterialType,
			ref:          MaterialRef{MaterialID: attempt.MaterialID, VersionID: attempt.MaterialVersionID},
		}
		byKey[key] = append(byKey[key], attempt.ID)
	}
	byType := map[string]map[MaterialRef][]uuid.UUID{}
	for key, attemptIDs := range byKey {
		if byType[key.materialType] == nil {
			byType[key.materialType] = map[MaterialRef][]uuid.UUID{}
		}
		byType[key.materialType][key.ref] = attemptIDs
	}
	result := make(map[uuid.UUID]GradingMaterial, len(attempts))
	for materialType, refs := range byType {
		provider, err := s.provider(materialType)
		if err != nil {
			return nil, err
		}
		loaded := make(map[MaterialRef]GradingMaterial, len(refs))
		missing := make([]MaterialRef, 0, len(refs))
		for ref := range refs {
			if material, ok := s.gradingCache.get(materialType, ref); ok {
				loaded[ref] = material
				continue
			}
			missing = append(missing, ref)
		}
		if len(missing) > 0 {
			fetched, err := s.loadGradingMaterials(ctx, provider, missing)
			if err != nil {
				return nil, err
			}
			for ref, material := range fetched {
				s.gradingCache.put(materialType, ref, material)
				loaded[ref] = material
			}
		}
		for ref, material := range loaded {
			for _, attemptID := range refs[ref] {
				result[attemptID] = material
			}
		}
	}
	return result, nil
}

// loadGradingMaterials prefers a module's bulk loader and falls back to one
// call per version for modules that only implement the single-version API.
func (s *Service) loadGradingMaterials(
	ctx context.Context,
	provider MaterialProvider,
	refs []MaterialRef,
) (map[MaterialRef]GradingMaterial, error) {
	bulk, ok := provider.(BulkMaterialProvider)
	if !ok {
		loaded := make(map[MaterialRef]GradingMaterial, len(refs))
		for _, ref := range refs {
			material, err := provider.GradingStructure(ctx, ref.MaterialID, ref.VersionID)
			if err != nil {
				return nil, err
			}
			loaded[ref] = material
		}
		return loaded, nil
	}
	loaded, err := bulk.GradingStructures(ctx, refs)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if _, ok := loaded[ref]; ok {
			continue
		}
		// A version the bulk query did not return is a missing material; let
		// the single-version call report the module-specific not-found error.
		material, err := provider.GradingStructure(ctx, ref.MaterialID, ref.VersionID)
		if err != nil {
			return nil, err
		}
		loaded[ref] = material
	}
	return loaded, nil
}

// writingEvaluations loads the AI feedback of the submitted Writing attempts in
// one round trip when the repository supports it.
func (s *Service) writingEvaluations(ctx context.Context, attempts []Summary) (map[uuid.UUID]*WritingEvaluation, error) {
	result := map[uuid.UUID]*WritingEvaluation{}
	attemptIDs := []uuid.UUID{}
	for _, attempt := range attempts {
		if attempt.MaterialType == MaterialWriting {
			attemptIDs = append(attemptIDs, attempt.ID)
		}
	}
	if len(attemptIDs) == 0 {
		return result, nil
	}
	if repository, ok := s.repository.(interface {
		GetWritingEvaluations(context.Context, []uuid.UUID) (map[uuid.UUID]WritingEvaluation, error)
	}); ok {
		loaded, err := repository.GetWritingEvaluations(ctx, attemptIDs)
		if err != nil {
			return nil, err
		}
		for attemptID, evaluation := range loaded {
			result[attemptID] = &evaluation
		}
		return result, nil
	}
	repository, ok := s.repository.(interface {
		GetWritingEvaluation(context.Context, uuid.UUID) (WritingEvaluation, error)
	})
	if !ok {
		return nil, ErrNotFound
	}
	for _, attemptID := range attemptIDs {
		evaluation, err := repository.GetWritingEvaluation(ctx, attemptID)
		if err != nil {
			return nil, err
		}
		result[attemptID] = &evaluation
	}
	return result, nil
}

// speakingEvaluations loads the AI feedback of the submitted Speaking attempts
// in one round trip when the repository supports it.
func (s *Service) speakingEvaluations(ctx context.Context, attempts []Summary) (map[uuid.UUID]*SpeakingEvaluation, error) {
	result := map[uuid.UUID]*SpeakingEvaluation{}
	attemptIDs := []uuid.UUID{}
	for _, attempt := range attempts {
		if attempt.MaterialType == MaterialSpeaking {
			attemptIDs = append(attemptIDs, attempt.ID)
		}
	}
	if len(attemptIDs) == 0 {
		return result, nil
	}
	if repository, ok := s.repository.(interface {
		GetSpeakingEvaluations(context.Context, []uuid.UUID) (map[uuid.UUID]SpeakingEvaluation, error)
	}); ok {
		loaded, err := repository.GetSpeakingEvaluations(ctx, attemptIDs)
		if err != nil {
			return nil, err
		}
		for attemptID, evaluation := range loaded {
			result[attemptID] = &evaluation
		}
		return result, nil
	}
	repository, ok := s.repository.(interface {
		GetSpeakingEvaluation(context.Context, uuid.UUID) (SpeakingEvaluation, error)
	})
	if !ok {
		return nil, ErrNotFound
	}
	for _, attemptID := range attemptIDs {
		evaluation, err := repository.GetSpeakingEvaluation(ctx, attemptID)
		if err != nil {
			return nil, err
		}
		result[attemptID] = &evaluation
	}
	return result, nil
}
