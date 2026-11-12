package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type generatedQuestionAnswerBatchSnapshot struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Status          string `json:"status"`
	Total           int    `json:"total"`
	Done            int    `json:"done"`
	Answered        int    `json:"answered"`
	PartialEvidence int    `json:"partial_evidence"`
	Insufficient    int    `json:"insufficient"`
	Unavailable     int    `json:"unavailable"`
	Failed          int    `json:"failed"`
	Error           string `json:"error,omitempty"`
}

type generatedQuestionAnswerBatchTask struct {
	mu       sync.RWMutex
	snapshot generatedQuestionAnswerBatchSnapshot
	tenantID uint64
	cancel   context.CancelFunc
	started  time.Time
}

type generatedQuestionAnswerBatchManager struct {
	mu     sync.Mutex
	tasks  map[string]*generatedQuestionAnswerBatchTask
	active map[string]string
}

type generatedQuestionAnswerChunkBatch struct {
	chunkID string
	items   []generatedQuestionListItem
}

func newGeneratedQuestionAnswerBatchManager() *generatedQuestionAnswerBatchManager {
	return &generatedQuestionAnswerBatchManager{
		tasks:  make(map[string]*generatedQuestionAnswerBatchTask),
		active: make(map[string]string),
	}
}

func generatedQuestionAnswerBatchKey(tenantID uint64, knowledgeBaseID string) string {
	return fmt.Sprintf("%d:%s", tenantID, knowledgeBaseID)
}

func (m *generatedQuestionAnswerBatchManager) create(
	ctx context.Context, tenantID uint64, knowledgeBaseID string,
) (*generatedQuestionAnswerBatchTask, bool, context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := generatedQuestionAnswerBatchKey(tenantID, knowledgeBaseID)
	if taskID := m.active[key]; taskID != "" {
		if task := m.tasks[taskID]; task != nil {
			return task, false, nil
		}
		delete(m.active, key)
	}
	now := time.Now()
	for id, task := range m.tasks {
		task.mu.RLock()
		terminal := task.snapshot.Status == "completed" || task.snapshot.Status == "stopped" || task.snapshot.Status == "failed"
		old := now.Sub(task.started) > 24*time.Hour
		task.mu.RUnlock()
		if terminal && old {
			delete(m.tasks, id)
		}
	}
	workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	task := &generatedQuestionAnswerBatchTask{
		tenantID: tenantID,
		cancel:   cancel,
		started:  now,
		snapshot: generatedQuestionAnswerBatchSnapshot{
			ID: uuid.NewString(), KnowledgeBaseID: knowledgeBaseID, Status: "preparing",
		},
	}
	m.tasks[task.snapshot.ID] = task
	m.active[key] = task.snapshot.ID
	return task, true, workCtx
}

func (m *generatedQuestionAnswerBatchManager) get(taskID string) *generatedQuestionAnswerBatchTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[taskID]
}

func (m *generatedQuestionAnswerBatchManager) current(tenantID uint64, knowledgeBaseID string) *generatedQuestionAnswerBatchTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[m.active[generatedQuestionAnswerBatchKey(tenantID, knowledgeBaseID)]]
}

func (m *generatedQuestionAnswerBatchManager) finish(task *generatedQuestionAnswerBatchTask) {
	if task == nil {
		return
	}
	task.mu.RLock()
	key := generatedQuestionAnswerBatchKey(task.tenantID, task.snapshot.KnowledgeBaseID)
	task.mu.RUnlock()
	m.mu.Lock()
	if m.active[key] == task.snapshot.ID {
		delete(m.active, key)
	}
	m.mu.Unlock()
}

func (t *generatedQuestionAnswerBatchTask) snapshotValue() generatedQuestionAnswerBatchSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshot
}

func (t *generatedQuestionAnswerBatchTask) setStatus(status string) {
	t.mu.Lock()
	t.snapshot.Status = status
	t.mu.Unlock()
}

func (t *generatedQuestionAnswerBatchTask) setTotal(total int) {
	t.mu.Lock()
	t.snapshot.Total = total
	t.mu.Unlock()
}

func (t *generatedQuestionAnswerBatchTask) record(status string) {
	t.mu.Lock()
	t.snapshot.Done++
	switch status {
	case "answered":
		t.snapshot.Answered++
	case "partial_evidence":
		t.snapshot.PartialEvidence++
	case "insufficient_source", "not_found":
		t.snapshot.Insufficient++
	case "source_unavailable", "missing_question":
		t.snapshot.Unavailable++
	default:
		t.snapshot.Failed++
	}
	t.mu.Unlock()
}

func (t *generatedQuestionAnswerBatchTask) fail(err error) {
	t.mu.Lock()
	t.snapshot.Status = "failed"
	if err != nil {
		t.snapshot.Error = "Unable to prepare generated question answers"
	}
	t.mu.Unlock()
}

func (t *generatedQuestionAnswerBatchTask) stop() {
	t.mu.RLock()
	cancel := t.cancel
	t.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (h *ChunkHandler) StartGeneratedQuestionAnswerBatch(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := strings.TrimSpace(c.Param("id"))
	if kbID == "" || len(kbID) > 128 {
		c.Error(errors.NewBadRequestError("Invalid knowledge base ID"))
		return
	}
	if h.service == nil || h.service.GetRepository() == nil || h.answerBatches == nil {
		c.Error(errors.NewInternalServerError("Generated question answer processing is unavailable"))
		return
	}
	query, err := parseGeneratedQuestionListQuery(c)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	task, created, workCtx := h.answerBatches.create(ctx, tenantID, kbID)
	if created {
		go h.runGeneratedQuestionAnswerBatch(workCtx, tenantID, kbID, query, task)
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": task.snapshotValue()})
}

func (h *ChunkHandler) CurrentGeneratedQuestionAnswerBatch(c *gin.Context) {
	kbID := strings.TrimSpace(c.Param("id"))
	tenantID := types.MustTenantIDFromContext(c.Request.Context())
	if h.answerBatches == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": nil})
		return
	}
	task := h.answerBatches.current(tenantID, kbID)
	if task == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task.snapshotValue()})
}

func (h *ChunkHandler) GetGeneratedQuestionAnswerBatch(c *gin.Context) {
	task := h.answerBatchTaskForRequest(c)
	if task == nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task.snapshotValue()})
}

func (h *ChunkHandler) StopGeneratedQuestionAnswerBatch(c *gin.Context) {
	task := h.answerBatchTaskForRequest(c)
	if task == nil {
		return
	}
	task.stop()
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": task.snapshotValue()})
}

func (h *ChunkHandler) answerBatchTaskForRequest(c *gin.Context) *generatedQuestionAnswerBatchTask {
	if h.answerBatches == nil {
		c.Error(errors.NewNotFoundError("Generated question answer task not found"))
		return nil
	}
	task := h.answerBatches.get(strings.TrimSpace(c.Param("task_id")))
	if task == nil || task.tenantID != types.MustTenantIDFromContext(c.Request.Context()) ||
		task.snapshotValue().KnowledgeBaseID != strings.TrimSpace(c.Param("id")) {
		c.Error(errors.NewNotFoundError("Generated question answer task not found"))
		return nil
	}
	return task
}

func (h *ChunkHandler) runGeneratedQuestionAnswerBatch(
	workCtx context.Context, tenantID uint64, kbID string,
	query generatedQuestionListQuery, task *generatedQuestionAnswerBatchTask,
) {
	defer h.answerBatches.finish(task)
	defer task.stop()
	// Reattach the authorized tenant after detaching from the short-lived HTTP request.
	ctx := types.WithExecutionTenant(workCtx, tenantID)
	if err := h.prepareAndRunGeneratedQuestionAnswerBatch(ctx, tenantID, kbID, query, task); err != nil {
		if ctx.Err() != nil {
			task.setStatus("stopped")
		} else {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{"knowledge_base_id": kbID, "batch_id": task.snapshotValue().ID})
			task.fail(err)
		}
		return
	}
	if ctx.Err() != nil {
		task.setStatus("stopped")
		return
	}
	task.setStatus("completed")
}

func (h *ChunkHandler) prepareAndRunGeneratedQuestionAnswerBatch(
	ctx context.Context, tenantID uint64, kbID string,
	query generatedQuestionListQuery, task *generatedQuestionAnswerBatchTask,
) error {
	chunks, err := h.service.GetRepository().ListDocumentChunksWithGeneratedQuestions(ctx, tenantID, kbID, false)
	if err != nil {
		return fmt.Errorf("failed to read saved question list: %w", err)
	}
	knowledgeTitles := make(map[string]string)
	if h.kgService != nil {
		knowledges, listErr := h.kgService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
		if listErr != nil {
			return fmt.Errorf("failed to read question source documents: %w", listErr)
		}
		for _, knowledge := range knowledges {
			if knowledge == nil || knowledge.KnowledgeBaseID != kbID {
				continue
			}
			title := strings.TrimSpace(knowledge.Title)
			if title == "" {
				title = strings.TrimSpace(knowledge.FileName)
			}
			if title != "" {
				knowledgeTitles[knowledge.ID] = title
			}
		}
	}
	items, _ := collectGeneratedQuestionItems(kbID, chunks, knowledgeTitles)
	chunks = nil
	knowledgeTitles = nil
	if answerReader, ok := h.service.GetRepository().(generatedQuestionAnswerListReader); ok {
		answers, listErr := answerReader.ListGeneratedQuestionAnswersByKnowledgeBase(ctx, tenantID, kbID)
		if listErr != nil {
			return fmt.Errorf("failed to read saved answer states: %w", listErr)
		}
		applyGeneratedQuestionAnswerStates(items, answers)
		answers = nil
	}
	selectedState := query.AnswerState
	query.AnswerState = "all"
	items = filterGeneratedQuestionItems(items, query)
	targetStates := []string{"not_generated", "not_found", "partial_evidence", "insufficient_source"}
	if selectedState == "answered" {
		targetStates = nil
	} else if selectedState != "all" {
		targetStates = []string{selectedState}
	}
	stateSet := make(map[string]struct{}, len(targetStates))
	for _, state := range targetStates {
		stateSet[state] = struct{}{}
	}
	if query.DuplicateMode != "all" {
		assignGeneratedQuestionBatchDuplicates(kbID, items)
	}
	bestByDuplicate := make(map[string]int)
	if query.DuplicateMode == "unique" {
		for index, item := range items {
			if _, ok := stateSet[item.AnswerState]; !ok || item.DuplicateGroupKey == nil {
				continue
			}
			key := item.AnswerState + ":" + *item.DuplicateGroupKey
			if previous, exists := bestByDuplicate[key]; !exists || canonicalQuestionLess(item, items[previous]) {
				bestByDuplicate[key] = index
			}
		}
	}
	selected := items[:0]
	for index, item := range items {
		if _, ok := stateSet[item.AnswerState]; !ok {
			continue
		}
		if query.DuplicateMode == "duplicates" && !item.IsDuplicate {
			continue
		}
		if query.DuplicateMode == "unique" && item.DuplicateGroupKey != nil {
			key := item.AnswerState + ":" + *item.DuplicateGroupKey
			if bestByDuplicate[key] != index {
				continue
			}
		}
		selected = append(selected, item)
	}
	items = selected
	totalTargets := len(items)
	groups := make([]generatedQuestionAnswerChunkBatch, 0)
	for _, item := range items {
		if len(groups) == 0 || groups[len(groups)-1].chunkID != item.ChunkID {
			groups = append(groups, generatedQuestionAnswerChunkBatch{chunkID: item.ChunkID})
		}
		last := len(groups) - 1
		groups[last].items = append(groups[last].items, item)
	}
	selected = nil
	items = nil
	task.setTotal(totalTargets)
	task.setStatus("running")
	if len(groups) == 0 {
		return nil
	}
	jobs := make(chan generatedQuestionAnswerChunkBatch, 3)
	var workers sync.WaitGroup
	workers.Add(3)
	for workerIndex := 0; workerIndex < 3; workerIndex++ {
		go func() {
			defer workers.Done()
			for group := range jobs {
				if ctx.Err() != nil {
					return
				}
				h.processGeneratedQuestionAnswerChunk(ctx, kbID, group, task)
			}
		}()
	}
	for index := range groups {
		if ctx.Err() != nil {
			break
		}
		group := groups[index]
		groups[index].items = nil
		select {
		case jobs <- group:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)
	workers.Wait()
	return ctx.Err()
}

func assignGeneratedQuestionBatchDuplicates(kbID string, items []generatedQuestionListItem) {
	counts := make(map[string]int)
	keys := make([]string, len(items))
	for index, item := range items {
		normalized := normalizedQuestionText(item.Question)
		if normalized == "" {
			continue
		}
		digest := generatedQuestionDuplicateDigest(kbID, normalized)
		key := item.AnswerState + ":" + digest
		keys[index] = key
		counts[key]++
	}
	for index := range items {
		items[index].DuplicateCount = 1
		if keys[index] == "" {
			continue
		}
		key := strings.TrimPrefix(keys[index], items[index].AnswerState+":")
		group := "qdup-v1:" + key
		items[index].DuplicateGroupKey = &group
		items[index].DuplicateCount = counts[keys[index]]
		items[index].IsDuplicate = counts[keys[index]] > 1
	}
}

func generatedQuestionDuplicateDigest(kbID, normalized string) string {
	digest := sha256.Sum256([]byte("question-text-v1\x00" + kbID + "\x00" + normalized))
	return hex.EncodeToString(digest[:])
}

func (h *ChunkHandler) processGeneratedQuestionAnswerChunk(
	ctx context.Context, kbID string,
	group generatedQuestionAnswerChunkBatch, task *generatedQuestionAnswerBatchTask,
) {
	if ctx.Err() != nil {
		return
	}
	chunk, err := h.service.GetChunkByID(ctx, group.chunkID)
	if err != nil || chunk == nil || chunk.KnowledgeBaseID != kbID {
		if ctx.Err() != nil {
			return
		}
		for range group.items {
			task.record("source_unavailable")
		}
		return
	}
	resolver := newGeneratedQuestionSourceResolver(ctx, h)
	requestCache := make(map[string]generatedQuestionAnswerCacheEntry)
	for _, item := range group.items {
		if ctx.Err() != nil {
			return
		}
		passage, sourceStatus, _ := resolver.resolve(item, chunk)
		if strings.TrimSpace(item.Question) == "" {
			task.record("missing_question")
			continue
		}
		if strings.TrimSpace(passage) == "" {
			task.record("source_unavailable")
			continue
		}
		answer, _, answerErr := h.getOrGenerateGeneratedQuestionAnswer(
			ctx, item, passage, sourceStatus, requestCache, item.AnswerState != "not_generated",
		)
		if answerErr != nil {
			if ctx.Err() != nil {
				return
			}
			logger.ErrorWithFields(ctx, answerErr, map[string]interface{}{
				"knowledge_base_id": kbID, "chunk_id": item.ChunkID,
				"question_id": generatedQuestionStringValue(item.QuestionID),
			})
			task.record("failed")
			continue
		}
		if answer == nil {
			task.record("failed")
			continue
		}
		task.record(answer.AnswerStatus)
	}
}
