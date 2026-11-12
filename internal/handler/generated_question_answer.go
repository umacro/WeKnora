package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type generatedQuestionAnswerRequest struct {
	ChunkID          string `json:"chunk_id"`
	QuestionID       string `json:"question_id"`
	QuestionPosition int    `json:"question_position"`
}

type generatedQuestionAnswerExtractor interface {
	ExtractGeneratedQuestionAnswer(ctx context.Context, knowledgeBaseID, knowledgeID, question, source string) (string, string, error)
}

type generatedQuestionSourceResolver struct {
	handler           *ChunkHandler
	ctx               context.Context
	revisionCache     map[string][]*types.ChunkRevision
	revisionLoaded    map[string]bool
	revisionLoadFailed map[string]bool
}

func newGeneratedQuestionSourceResolver(ctx context.Context, h *ChunkHandler) *generatedQuestionSourceResolver {
	return &generatedQuestionSourceResolver{
		handler: h, ctx: ctx,
		revisionCache: make(map[string][]*types.ChunkRevision),
		revisionLoaded: make(map[string]bool),
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
	if strings.TrimSpace(item.Question) == "" {
		answerStatus = "missing_question"
	} else if strings.TrimSpace(passage) != "" {
		extractor, ok := h.service.(generatedQuestionAnswerExtractor)
		if !ok {
			c.Error(errors.NewServiceUnavailableError("Question answer extraction is unavailable"))
			return
		}
		answer, modelID, err = extractor.ExtractGeneratedQuestionAnswer(ctx, kbID, item.KnowledgeID, item.Question, passage)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"knowledge_base_id": kbID, "chunk_id": request.ChunkID,
				"question_id": request.QuestionID,
			})
			c.Error(errors.NewServiceUnavailableError("Could not extract an answer from the source text"))
			return
		}
		if strings.TrimSpace(answer) == "" {
			answerStatus = "not_found"
		} else {
			answerStatus = "answered"
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"answer": answer,
			"answer_status": answerStatus,
			"answer_model_id": modelID,
			"source_revision_status": sourceStatus,
			"question_revision": item.QuestionRevision,
		},
	})
}
