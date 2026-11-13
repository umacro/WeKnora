package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetGeneratedQuestionAnswer returns a saved answer scoped to its tenant.
func (r *chunkRepository) GetGeneratedQuestionAnswer(
	ctx context.Context, tenantID uint64, id string,
) (*types.GeneratedQuestionAnswer, error) {
	var answer types.GeneratedQuestionAnswer
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&answer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &answer, nil
}

// ListGeneratedQuestionAnswersByKnowledgeBase returns the saved answer
// metadata needed to decorate a question list without invoking a model.
func (r *chunkRepository) ListGeneratedQuestionAnswersByKnowledgeBase(
	ctx context.Context, tenantID uint64, knowledgeBaseID string,
) ([]*types.GeneratedQuestionAnswer, error) {
	var answers []*types.GeneratedQuestionAnswer
	err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "knowledge_base_id", "knowledge_id", "chunk_id", "question_id", "question_position", "question_revision", "question", "answer_status", "answer_model_id", "created_at", "updated_at").
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Order("updated_at DESC").
		Find(&answers).Error
	if err != nil {
		return nil, err
	}
	return answers, nil
}

// UpsertGeneratedQuestionAnswer creates the first answer or refreshes its
// result for the same stable question/source identity after explicit reanalysis.
func (r *chunkRepository) UpsertGeneratedQuestionAnswer(
	ctx context.Context, tenantID uint64, answer *types.GeneratedQuestionAnswer,
) error {
	if answer == nil || answer.TenantID != tenantID || answer.ID == "" {
		return errors.New("invalid generated question answer")
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"answer", "answer_status", "answer_model_id", "source_revision_status", "updated_at",
			}),
		}).
		Create(answer).Error
}

// UpsertGeneratedQuestionAnswers persists one model response in a single
// database operation instead of opening a write transaction for every answer.
func (r *chunkRepository) UpsertGeneratedQuestionAnswers(
	ctx context.Context, tenantID uint64, answers []*types.GeneratedQuestionAnswer,
) error {
	if len(answers) == 0 {
		return nil
	}
	for _, answer := range answers {
		if answer == nil || answer.TenantID != tenantID || answer.ID == "" {
			return errors.New("invalid generated question answer batch")
		}
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"answer", "answer_status", "answer_model_id", "source_revision_status", "updated_at",
			}),
		}).
		CreateInBatches(answers, 20).Error
}
