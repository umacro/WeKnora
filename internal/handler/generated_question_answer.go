package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type generatedQuestionAnswerRequest struct {
	ChunkID          string `json:"chunk_id"`
	QuestionID       string `json:"question_id"`
	QuestionPosition int    `json:"question_position"`
	ForceReanalysis  bool   `json:"force_reanalysis,omitempty"`
}

type generatedQuestionAnswerExtractor interface {
	ExtractGeneratedQuestionAnswer(ctx context.Context, knowledgeBaseID, question, source string) (types.GeneratedQuestionAnswerExtraction, string, error)
}

type generatedQuestionAnswerKnowledgeExtractor interface {
	ExtractGeneratedQuestionAnswer(ctx context.Context, knowledgeBaseID, knowledgeID, question, source string) (types.GeneratedQuestionAnswerExtraction, string, error)
}

type generatedQuestionAnswerBatchExtractor interface {
	ExtractGeneratedQuestionAnswerBatch(
		ctx context.Context, knowledgeBaseID, knowledgeID string, questions []string, source string,
	) ([]types.GeneratedQuestionAnswerExtraction, string, error)
}

func supportsGeneratedQuestionAnswerExtraction(service interface{}) bool {
	if _, ok := service.(generatedQuestionAnswerExtractor); ok {
		return true
	}
	_, ok := service.(generatedQuestionAnswerKnowledgeExtractor)
	return ok
}

func extractGeneratedQuestionAnswer(
	service interface{}, ctx context.Context, item generatedQuestionListItem, source string,
) (types.GeneratedQuestionAnswerExtraction, string, error) {
	if extractor, ok := service.(generatedQuestionAnswerKnowledgeExtractor); ok {
		return extractor.ExtractGeneratedQuestionAnswer(
			ctx, item.KnowledgeBaseID, item.KnowledgeID, item.Question, source,
		)
	}
	if extractor, ok := service.(generatedQuestionAnswerExtractor); ok {
		return extractor.ExtractGeneratedQuestionAnswer(ctx, item.KnowledgeBaseID, item.Question, source)
	}
	return types.GeneratedQuestionAnswerExtraction{}, "", errors.NewServiceUnavailableError("Question answer extraction is unavailable")
}

func extractGeneratedQuestionAnswerWithRetry(
	service interface{}, ctx context.Context, item generatedQuestionListItem, source string,
) (types.GeneratedQuestionAnswerExtraction, string, error) {
	var result types.GeneratedQuestionAnswerExtraction
	var modelID string
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		result, modelID, err = extractGeneratedQuestionAnswer(service, ctx, item, source)
		if err == nil || !waitGeneratedQuestionAnswerRetry(ctx, attempt, err) {
			return result, modelID, err
		}
	}
	return result, modelID, err
}

func extractGeneratedQuestionAnswerBatchWithRetry(
	service interface{}, ctx context.Context, knowledgeBaseID, knowledgeID string, questions []string, source string,
) ([]types.GeneratedQuestionAnswerExtraction, string, error) {
	extractor, ok := service.(generatedQuestionAnswerBatchExtractor)
	if !ok {
		return nil, "", fmt.Errorf("batched question answer extraction is unavailable")
	}
	var results []types.GeneratedQuestionAnswerExtraction
	var modelID string
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		results, modelID, err = extractor.ExtractGeneratedQuestionAnswerBatch(
			ctx, knowledgeBaseID, knowledgeID, questions, source,
		)
		if err == nil || !waitGeneratedQuestionAnswerRetry(ctx, attempt, err) {
			return results, modelID, err
		}
	}
	return results, modelID, err
}

func waitGeneratedQuestionAnswerRetry(ctx context.Context, attempt int, err error) bool {
	if ctx.Err() != nil || err == nil || !isTransientGeneratedQuestionAnswerError(err) || attempt >= 2 {
		return false
	}
	delay := time.Duration(1<<attempt) * 500 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func isTransientGeneratedQuestionAnswerError(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"failed to connect to validated addresses", "connection refused", "connection reset",
		"connection timed out", "i/o timeout", "deadline exceeded", "temporarily unavailable",
		"too many requests", "status code 429", "status 429", "status code 500", "status 500",
		"status code 502", "status 502", "status code 503", "status 503", "status code 504", "status 504",
		"unexpected eof", "server closed idle connection",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type generatedQuestionSourceResolver struct {
	handler            *ChunkHandler
	ctx                context.Context
	revisionCache      map[string][]*types.ChunkRevision
	revisionLoaded     map[string]bool
	revisionLoadFailed map[string]bool
}

func newGeneratedQuestionSourceResolver(ctx context.Context, h *ChunkHandler) *generatedQuestionSourceResolver {
	return &generatedQuestionSourceResolver{
		handler: h, ctx: ctx,
		revisionCache:      make(map[string][]*types.ChunkRevision),
		revisionLoaded:     make(map[string]bool),
		revisionLoadFailed: make(map[string]bool),
	}
}

func (r *generatedQuestionSourceResolver) resolve(
	item generatedQuestionListItem, chunk *types.Chunk,
) (string, string, []string) {
	reasons := make([]string, 0, 3)
	if chunk == nil {
		return "", "missing_chunk", []string{"source_chunk_missing"}
	}

	if item.QuestionRevision == nil {
		reasons = append(reasons, "source_revision_unknown")
		if !chunk.IsEnabled {
			reasons = append(reasons, "chunk_disabled")
		}
		return chunk.Content, "revision_unknown", reasons
	}
	if *item.QuestionRevision == chunk.ContentRevision {
		if !chunk.IsEnabled {
			reasons = append(reasons, "chunk_disabled")
		}
		return chunk.Content, "current_revision", reasons
	}

	if !r.revisionLoaded[chunk.ID] {
		revisions, err := r.handler.service.ListChunkRevisions(r.ctx, chunk.ID)
		r.revisionLoaded[chunk.ID] = true
		if err != nil {
			r.revisionLoadFailed[chunk.ID] = true
		} else {
			r.revisionCache[chunk.ID] = revisions
		}
	}
	if r.revisionLoadFailed[chunk.ID] {
		reasons = append(reasons, "source_revision_history_unavailable")
		return "", "history_read_failed", reasons
	}
	for _, revision := range r.revisionCache[chunk.ID] {
		if revision != nil && revision.Revision == *item.QuestionRevision {
			if !chunk.IsEnabled {
				reasons = append(reasons, "chunk_disabled")
			}
			return revision.Content, "historical_revision", reasons
		}
	}
	reasons = append(reasons, "source_revision_not_retained")
	return "", "revision_unavailable", reasons
}

// AnswerGeneratedQuestion extracts an answer from the exact chunk revision
// associated with the saved question. It never answers from a substituted
// current revision when the original source revision is unavailable.
func (h *ChunkHandler) AnswerGeneratedQuestion(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := strings.TrimSpace(c.Param("id"))
	if kbID == "" || len(kbID) > 128 {
		c.Error(errors.NewBadRequestError("Invalid knowledge base ID"))
		return
	}

	var request generatedQuestionAnswerRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(errors.NewBadRequestError("Invalid generated question answer request"))
		return
	}
	request.ChunkID = strings.TrimSpace(request.ChunkID)
	request.QuestionID = strings.TrimSpace(request.QuestionID)
	if request.ChunkID == "" || len(request.ChunkID) > 128 || request.QuestionPosition < 1 || request.QuestionPosition > 10000 {
		c.Error(errors.NewBadRequestError("chunk_id and a valid question_position are required"))
		return
	}

	chunk, err := h.service.GetChunkByID(ctx, request.ChunkID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": kbID, "chunk_id": request.ChunkID,
		})
		c.Error(errors.NewNotFoundError("Generated question source chunk not found"))
		return
	}
	if chunk == nil || chunk.KnowledgeBaseID != kbID {
		c.Error(errors.NewNotFoundError("Generated question source chunk not found"))
		return
	}

	items, _ := collectGeneratedQuestionItems(kbID, []*types.Chunk{chunk}, nil)
	var item *generatedQuestionListItem
	for i := range items {
		candidate := &items[i]
		if candidate.QuestionPosition != request.QuestionPosition {
			continue
		}
		if request.QuestionID != "" && (candidate.QuestionID == nil || *candidate.QuestionID != request.QuestionID) {
			continue
		}
		item = candidate
		break
	}
	if item == nil {
		c.Error(errors.NewNotFoundError("Generated question not found"))
		return
	}

	resolver := newGeneratedQuestionSourceResolver(ctx, h)
	passage, sourceStatus, _ := resolver.resolve(*item, chunk)
	answerStatus := "source_unavailable"
	answer := ""
	modelID := ""
	answerCached := false
	var answerRecord *types.GeneratedQuestionAnswer
	if strings.TrimSpace(item.Question) == "" {
		answerStatus = "missing_question"
	} else if strings.TrimSpace(passage) != "" {
		if !supportsGeneratedQuestionAnswerExtraction(h.service) {
			c.Error(errors.NewServiceUnavailableError("Question answer extraction is unavailable"))
			return
		}
		answerRecord, answerCached, err = h.getOrGenerateGeneratedQuestionAnswer(
			ctx, *item, passage, sourceStatus, nil, request.ForceReanalysis,
		)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"knowledge_base_id": kbID, "chunk_id": request.ChunkID,
				"question_id": request.QuestionID,
			})
			c.Error(errors.NewServiceUnavailableError("Could not generate and save an answer from the source text"))
			return
		}
		answer = answerRecord.Answer
		answerStatus = answerRecord.AnswerStatus
		modelID = answerRecord.AnswerModelID
	}

	data := gin.H{
		"answer":                 answer,
		"answer_status":          answerStatus,
		"answer_model_id":        modelID,
		"answer_cached":          answerCached,
		"source_revision_status": sourceStatus,
		"question_revision":      item.QuestionRevision,
	}
	if answerRecord != nil {
		data["answer_record_id"] = answerRecord.ID
		data["answer_created_at"] = answerRecord.CreatedAt
		data["answer_updated_at"] = answerRecord.UpdatedAt
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}
