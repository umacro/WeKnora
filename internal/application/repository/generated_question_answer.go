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

// CreateGeneratedQuestionAnswerIfAbsent saves an answer once. The stable ID
// makes concurrent requests idempotent and keeps answers for different source
// revisions separate.
func (r *chunkRepository) CreateGeneratedQuestionAnswerIfAbsent(
	ctx context.Context, tenantID uint64, answer *types.GeneratedQuestionAnswer,
) error {
	if answer == nil || answer.TenantID != tenantID || answer.ID == "" {
		return errors.New("invalid generated question answer")
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoNothing: true,
		}).
		Create(answer).Error
}
