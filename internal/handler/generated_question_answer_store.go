package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type generatedQuestionAnswerStore interface {
	GetGeneratedQuestionAnswer(ctx context.Context, tenantID uint64, id string) (*types.GeneratedQuestionAnswer, error)
	UpsertGeneratedQuestionAnswer(ctx context.Context, tenantID uint64, answer *types.GeneratedQuestionAnswer) error
}

type generatedQuestionAnswerBatchStore interface {
	UpsertGeneratedQuestionAnswers(ctx context.Context, tenantID uint64, answers []*types.GeneratedQuestionAnswer) error
}

type generatedQuestionAnswerListReader interface {
	ListGeneratedQuestionAnswersByKnowledgeBase(
		ctx context.Context, tenantID uint64, knowledgeBaseID string,
	) ([]*types.GeneratedQuestionAnswer, error)
}

type generatedQuestionAnswerCacheEntry struct {
	answer  string
	status  string
	modelID string
}

type generatedQuestionAnswerIdentity struct {
	TenantID         uint64 `json:"tenant_id"`
	KnowledgeBaseID  string `json:"knowledge_base_id"`
	KnowledgeID      string `json:"knowledge_id"`
	ChunkID          string `json:"chunk_id"`
	QuestionID       string `json:"question_id"`
	QuestionPosition int    `json:"question_position"`
	QuestionRevision *int   `json:"question_revision"`
	Question         string `json:"question"`
	SourceHash       string `json:"source_hash"`
}

type generatedQuestionAnswerListIdentity struct {
	KnowledgeBaseID  string `json:"knowledge_base_id"`
	KnowledgeID      string `json:"knowledge_id"`
	ChunkID          string `json:"chunk_id"`
	QuestionID       string `json:"question_id"`
	QuestionPosition int    `json:"question_position"`
	QuestionRevision *int   `json:"question_revision"`
	Question         string `json:"question"`
}

func generatedQuestionAnswerListKey(identity generatedQuestionAnswerListIdentity) string {
	encoded, _ := json.Marshal(identity)
	return string(encoded)
}

func applyGeneratedQuestionAnswerStates(items []generatedQuestionListItem, answers []*types.GeneratedQuestionAnswer) {
	latestByQuestion := make(map[string]*types.GeneratedQuestionAnswer, len(answers))
	for _, answer := range answers {
		if answer == nil {
			continue
		}
		key := generatedQuestionAnswerListKey(generatedQuestionAnswerListIdentity{
			KnowledgeBaseID: answer.KnowledgeBaseID, KnowledgeID: answer.KnowledgeID,
			ChunkID: answer.ChunkID, QuestionID: answer.QuestionID,
			QuestionPosition: answer.QuestionPosition, QuestionRevision: answer.QuestionRevision,
			Question: answer.Question,
		})
		if current := latestByQuestion[key]; current == nil || answer.UpdatedAt.After(current.UpdatedAt) {
			latestByQuestion[key] = answer
		}
	}
	for index := range items {
		item := &items[index]
		key := generatedQuestionAnswerListKey(generatedQuestionAnswerListIdentity{
			KnowledgeBaseID: item.KnowledgeBaseID, KnowledgeID: item.KnowledgeID,
			ChunkID: item.ChunkID, QuestionID: generatedQuestionStringValue(item.QuestionID),
			QuestionPosition: item.QuestionPosition, QuestionRevision: item.QuestionRevision,
			Question: item.Question,
		})
		answer := latestByQuestion[key]
		if answer == nil {
			item.AnswerState = "not_generated"
			item.AnswerCreatedAt = nil
			item.AnswerUpdatedAt = nil
			item.AnswerModelID = ""
			continue
		}
		switch answer.AnswerStatus {
		case types.GeneratedQuestionAnswerStatusAnswered,
			types.GeneratedQuestionAnswerStatusPartialEvidence,
			types.GeneratedQuestionAnswerStatusInsufficientSource,
			types.GeneratedQuestionAnswerStatusLegacyNotFound:
			item.AnswerState = answer.AnswerStatus
		default:
			item.AnswerState = "not_generated"
		}
		createdAt := answer.CreatedAt
		updatedAt := answer.UpdatedAt
		item.AnswerCreatedAt = &createdAt
		item.AnswerUpdatedAt = &updatedAt
		item.AnswerModelID = answer.AnswerModelID
	}
}

func generatedQuestionAnswerSourceHash(source string) string {
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:])
}

func generatedQuestionAnswerRecordID(identity generatedQuestionAnswerIdentity) (string, error) {
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func generatedQuestionAnswerReuseKey(item generatedQuestionListItem, sourceHash string) string {
	encoded, _ := json.Marshal(struct {
		KnowledgeBaseID string `json:"knowledge_base_id"`
		KnowledgeID     string `json:"knowledge_id"`
		Question        string `json:"question"`
		SourceHash      string `json:"source_hash"`
	}{item.KnowledgeBaseID, item.KnowledgeID, item.Question, sourceHash})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// getOrGenerateGeneratedQuestionAnswer reuses an answer saved for this exact
// question and source revision unless an unresolved result is explicitly
// reanalyzed. New and refreshed results are persisted before they are returned.
func (h *ChunkHandler) getOrGenerateGeneratedQuestionAnswer(
	ctx context.Context,
	item generatedQuestionListItem,
	source string,
	sourceRevisionStatus string,
	requestCache map[string]generatedQuestionAnswerCacheEntry,
	forceReanalysis bool,
) (*types.GeneratedQuestionAnswer, bool, error) {
	if strings.TrimSpace(source) == "" {
		return nil, false, fmt.Errorf("source passage is required")
	}
	if !supportsGeneratedQuestionAnswerExtraction(h.service) {
		return nil, false, fmt.Errorf("question answer extraction is unavailable")
	}
	if h.service == nil || h.service.GetRepository() == nil {
		return nil, false, fmt.Errorf("generated question answer storage is unavailable")
	}
	store, ok := h.service.GetRepository().(generatedQuestionAnswerStore)
	if !ok {
		return nil, false, fmt.Errorf("generated question answer storage is unavailable")
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	sourceHash := generatedQuestionAnswerSourceHash(source)
	identity := generatedQuestionAnswerIdentity{
		TenantID: tenantID, KnowledgeBaseID: item.KnowledgeBaseID,
		KnowledgeID: item.KnowledgeID, ChunkID: item.ChunkID,
		QuestionID:       generatedQuestionStringValue(item.QuestionID),
		QuestionPosition: item.QuestionPosition, QuestionRevision: item.QuestionRevision,
		Question: item.Question, SourceHash: sourceHash,
	}
	recordID, err := generatedQuestionAnswerRecordID(identity)
	if err != nil {
		return nil, false, fmt.Errorf("failed to identify generated question answer: %w", err)
	}

	saved, err := store.GetGeneratedQuestionAnswer(ctx, tenantID, recordID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read saved generated question answer: %w", err)
	}
	if saved != nil && (!forceReanalysis || saved.AnswerStatus == types.GeneratedQuestionAnswerStatusAnswered) {
		if requestCache != nil {
			requestCache[generatedQuestionAnswerReuseKey(item, sourceHash)] = generatedQuestionAnswerCacheEntry{
				answer: saved.Answer, status: saved.AnswerStatus, modelID: saved.AnswerModelID,
			}
		}
		return saved, true, nil
	}

	reuseKey := generatedQuestionAnswerReuseKey(item, sourceHash)
	entry, foundInRequest := requestCache[reuseKey]
	if forceReanalysis {
		foundInRequest = false
		entry = generatedQuestionAnswerCacheEntry{}
	}
	if !foundInRequest {
		result, modelID, extractErr := extractGeneratedQuestionAnswerWithRetry(h.service, ctx, item, source)
		if extractErr != nil {
			return nil, false, extractErr
		}
		entry.answer = strings.TrimSpace(result.Answer)
		entry.modelID = modelID
		entry.status = result.Status
		if entry.status == "" {
			if entry.answer == "" {
				entry.status = types.GeneratedQuestionAnswerStatusInsufficientSource
			} else {
				entry.status = types.GeneratedQuestionAnswerStatusAnswered
			}
		}
	}

	now := time.Now().UTC()
	answer := &types.GeneratedQuestionAnswer{
		ID: recordID, TenantID: tenantID,
		KnowledgeBaseID: item.KnowledgeBaseID, KnowledgeID: item.KnowledgeID,
		ChunkID: item.ChunkID, QuestionID: identity.QuestionID,
		QuestionPosition: item.QuestionPosition, QuestionRevision: item.QuestionRevision,
		Question: item.Question, SourceHash: sourceHash, Answer: entry.answer,
		AnswerStatus: entry.status, AnswerModelID: entry.modelID,
		SourceRevisionStatus: sourceRevisionStatus, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertGeneratedQuestionAnswer(ctx, tenantID, answer); err != nil {
		return nil, false, fmt.Errorf("failed to save generated question answer: %w", err)
	}
	// Read back the canonical row so the response reflects the persisted result,
	// including any concurrent update for this exact question/source identity.
	saved, err = store.GetGeneratedQuestionAnswer(ctx, tenantID, recordID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to confirm saved generated question answer: %w", err)
	}
	if saved == nil {
		return nil, false, fmt.Errorf("saved generated question answer could not be read back")
	}
	if requestCache != nil {
		requestCache[reuseKey] = generatedQuestionAnswerCacheEntry{
			answer: saved.Answer, status: saved.AnswerStatus, modelID: saved.AnswerModelID,
		}
	}
	return saved, false, nil
}
