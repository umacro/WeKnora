package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	generatedQuestionDefaultPageSize = 20
	generatedQuestionMaxPageSize     = 100
	generatedQuestionMaxExportRows   = 5000
	generatedQuestionMaxExportBytes  = 10 << 20
	generatedQuestionMaxTrainingBytes = 50 << 20
)

type generatedQuestionListItem struct {
	RowKey               string  `json:"row_key"`
	QuestionID           *string `json:"question_id"`
	Question             string  `json:"question"`
	KnowledgeBaseID      string  `json:"knowledge_base_id"`
	WikiID               *string `json:"wiki_id"`
	KnowledgeID          string  `json:"knowledge_id"`
	KnowledgeTitle       *string `json:"knowledge_title"`
	TaskID               *string `json:"task_id"`
	ChunkID              string  `json:"chunk_id"`
	ChunkIndex           int     `json:"chunk_index"`
	QuestionPosition     int     `json:"question_position"`
	GeneratedAt          *string `json:"generated_at"`
	QuestionRevision     *int    `json:"question_revision"`
	CurrentChunkRevision int     `json:"current_chunk_revision"`
	RevisionState        string  `json:"revision_state"`
	ChunkAvailability    string  `json:"chunk_availability"`
	IndexStatus          string  `json:"index_status"`
	TaskState            string  `json:"task_state"`
	QuestionDataState    string  `json:"question_data_state"`
	IsDuplicate          bool    `json:"is_duplicate"`
	DuplicateGroupKey    *string `json:"duplicate_group_key"`
	DuplicateCount       int     `json:"duplicate_count"`
}

type generatedQuestionListQuery struct {
	Page          int
	PageSize      int
	KnowledgeID   string
	ChunkID       string
	Keyword       string
	SortBy        string
	SortOrder     string
	DuplicateMode string
	Format        string
}

type generatedQuestionTrainingRecord struct {
	Query                string                           `json:"query"`
	Answer               string                           `json:"answer"`
	AnswerStatus         string                           `json:"answer_status"`
	PositivePassage      string                           `json:"positive_passage,omitempty"`
	Label                int                              `json:"label"`
	TrainingReady        bool                             `json:"training_ready"`
	TrainingExclusion    []string                         `json:"training_exclusion_reasons,omitempty"`
	SourceRevisionStatus string                           `json:"source_revision_status"`
	Metadata             generatedQuestionTrainingMetadata `json:"metadata"`
}

type generatedQuestionTrainingMetadata struct {
	KnowledgeBaseID      string  `json:"knowledge_base_id"`
	KnowledgeID          string  `json:"knowledge_id"`
	KnowledgeTitle       *string `json:"knowledge_title"`
	ChunkID              string  `json:"chunk_id"`
	ChunkIndex           int     `json:"chunk_index"`
	QuestionID           *string `json:"question_id"`
	QuestionPosition     int     `json:"question_position"`
	QuestionRevision     *int    `json:"question_revision"`
	CurrentChunkRevision int     `json:"current_chunk_revision"`
	RevisionState        string  `json:"revision_state"`
	ChunkAvailability    string  `json:"chunk_availability"`
	IndexStatus          string  `json:"index_status"`
	IsDuplicate          bool    `json:"is_duplicate"`
	DuplicateCount       int     `json:"duplicate_count"`
	AnswerModelID        string  `json:"answer_model_id,omitempty"`
}

// ListGeneratedQuestions returns the questions still stored in readable
// document chunks under the already-authorized knowledge base. It does not
// trigger generation and does not infer a Wiki page, task, or generation time.
func (h *ChunkHandler) ListGeneratedQuestions(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := strings.TrimSpace(c.Param("id"))
	if kbID == "" || len(kbID) > 128 {
		c.Error(errors.NewBadRequestError("Invalid knowledge base ID"))
		return
	}

	query, err := parseGeneratedQuestionListQuery(c)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if h.service == nil || h.service.GetRepository() == nil {
		c.Error(errors.NewInternalServerError("Generated question listing is unavailable"))
		return
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	includeContent := query.Format == "training_jsonl"
	chunks, err := h.service.GetRepository().ListDocumentChunksWithGeneratedQuestions(ctx, tenantID, kbID, includeContent)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"knowledge_base_id": kbID})
		c.Error(errors.NewInternalServerError("Failed to read generated questions"))
		return
	}

	knowledgeTitles := make(map[string]string)
	if h.kgService != nil {
		knowledges, listErr := h.kgService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
		if listErr != nil {
			logger.ErrorWithFields(ctx, listErr, map[string]interface{}{"knowledge_base_id": kbID})
			c.Error(errors.NewInternalServerError("Failed to read question source documents"))
			return
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

	items, warnings := collectGeneratedQuestionItems(kbID, chunks, knowledgeTitles)
	filtered := filterGeneratedQuestionItems(items, query)
	rawTotal := len(filtered)
	assignGeneratedQuestionDuplicates(kbID, filtered)
	filtered = applyGeneratedQuestionDuplicateMode(filtered, query.DuplicateMode)
	sortGeneratedQuestionItems(filtered, query.SortBy, query.SortOrder)

	if query.Format != "list" {
		if len(filtered) > generatedQuestionMaxExportRows {
			c.Error(errors.NewBadRequestError("Export is limited to 5,000 questions; narrow the filters and retry"))
			return
		}
		if query.Format == "training_jsonl" {
			h.writeGeneratedQuestionTrainingExport(c, filtered, chunks)
			return
		}
		h.writeGeneratedQuestionExport(c, query.Format, filtered, rawTotal, warnings)
		return
	}

	total := len(filtered)
	offset := (query.Page - 1) * query.PageSize
	if offset > total {
		offset = total
	}
	end := offset + query.PageSize
	if end > total {
		end = total
	}
	pageItems := append([]generatedQuestionListItem{}, filtered[offset:end]...)
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"data":          pageItems,
		"total":         total,
		"raw_total":     rawTotal,
		"data_warnings": warnings,
		"page":          query.Page,
		"page_size":     query.PageSize,
	})
}

func parseGeneratedQuestionListQuery(c *gin.Context) (generatedQuestionListQuery, error) {
	query := generatedQuestionListQuery{
		Page:          1,
		PageSize:      generatedQuestionDefaultPageSize,
		SortBy:        "chunk_index",
		SortOrder:     "asc",
		DuplicateMode: "all",
		Format:        "list",
	}
	for key, target := range map[string]*int{"page": &query.Page, "page_size": &query.PageSize} {
		value := c.Query(key)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			return query, fmt.Errorf("%s must be a positive integer", key)
		}
		*target = parsed
	}
	if query.Page > 1_000_000 {
		return query, fmt.Errorf("page exceeds the supported limit")
	}
	if query.PageSize > generatedQuestionMaxPageSize {
		return query, fmt.Errorf("page_size must be at most %d", generatedQuestionMaxPageSize)
	}
	query.KnowledgeID = strings.TrimSpace(c.Query("knowledge_id"))
	query.ChunkID = strings.TrimSpace(c.Query("chunk_id"))
	query.Keyword = strings.TrimSpace(c.Query("keyword"))
	if len(query.KnowledgeID) > 128 || len(query.ChunkID) > 128 {
		return query, fmt.Errorf("knowledge_id and chunk_id must be at most 128 characters")
	}
	if utf8.RuneCountInString(query.Keyword) > 200 {
		return query, fmt.Errorf("keyword must be at most 200 characters")
	}
	if value := c.Query("sort_by"); value != "" {
		query.SortBy = value
	}
	if query.SortBy != "chunk_index" && query.SortBy != "question_id" {
		return query, fmt.Errorf("sort_by must be chunk_index or question_id")
	}
	if value := c.Query("sort_order"); value != "" {
		query.SortOrder = value
	}
	if query.SortOrder != "asc" && query.SortOrder != "desc" {
		return query, fmt.Errorf("sort_order must be asc or desc")
	}
	if value := c.Query("duplicate_mode"); value != "" {
		query.DuplicateMode = value
	}
	if query.DuplicateMode != "all" && query.DuplicateMode != "duplicates" && query.DuplicateMode != "unique" {
		return query, fmt.Errorf("duplicate_mode must be all, duplicates, or unique")
	}
	if value := c.Query("format"); value != "" {
		query.Format = value
	}
	if query.Format != "list" && query.Format != "text" && query.Format != "csv" && query.Format != "json" && query.Format != "training_jsonl" {
		return query, fmt.Errorf("format must be list, text, csv, json, or training_jsonl")
	}
	return query, nil
}

func collectGeneratedQuestionItems(kbID string, chunks []*types.Chunk, knowledgeTitles map[string]string) ([]generatedQuestionListItem, int) {
	items := make([]generatedQuestionListItem, 0)
	warnings := 0
	for _, chunk := range chunks {
		if chunk == nil || chunk.KnowledgeBaseID != kbID {
			continue
		}
		var metadata map[string]json.RawMessage
		if err := json.Unmarshal([]byte(chunk.Metadata), &metadata); err != nil || metadata == nil {
			warnings++
			continue
		}
		var questions []json.RawMessage
		if err := json.Unmarshal(metadata["generated_questions"], &questions); err != nil {
			warnings++
			continue
		}
		var metadataRevision *int
		if rawRevision, exists := metadata["generated_questions_revision"]; exists && string(rawRevision) != "null" {
			var revision int
			if err := json.Unmarshal(rawRevision, &revision); err != nil {
				warnings++
			} else {
				metadataRevision = &revision
			}
		}

		for position, rawQuestion := range questions {
			var question types.GeneratedQuestion
			if err := json.Unmarshal(rawQuestion, &question); err != nil {
				warnings++
				continue
			}
			questionID := (*string)(nil)
			if question.ID != "" {
				id := question.ID
				questionID = &id
			}
			questionRevision := question.ContentRevision
			if questionRevision == nil && metadataRevision != nil {
				revision := *metadataRevision
				questionRevision = &revision
			}

			revisionState := "unknown"
			if questionRevision != nil {
				if *questionRevision == chunk.ContentRevision {
					revisionState = "current"
				} else {
					revisionState = "stale"
				}
			}
			availability := "disabled"
			if chunk.IsEnabled {
				availability = "available"
			}
			questionDataState := "available"
			if question.Question == "" {
				questionDataState = "missing_text"
				warnings++
			}

			var title *string
			if value := knowledgeTitles[chunk.KnowledgeID]; value != "" {
				copyValue := value
				title = &copyValue
			}
			items = append(items, generatedQuestionListItem{
				RowKey:               fmt.Sprintf("%s:%d", chunk.ID, position+1),
				QuestionID:           questionID,
				Question:             question.Question,
				KnowledgeBaseID:      kbID,
				KnowledgeID:          chunk.KnowledgeID,
				KnowledgeTitle:       title,
				ChunkID:              chunk.ID,
				ChunkIndex:           chunk.ChunkIndex,
				QuestionPosition:     position + 1,
				QuestionRevision:     questionRevision,
				CurrentChunkRevision: chunk.ContentRevision,
				RevisionState:        revisionState,
				ChunkAvailability:    availability,
				IndexStatus:          chunk.IndexStatus,
				TaskState:            "unknown",
				QuestionDataState:    questionDataState,
			})
		}
	}
	return items, warnings
}

func filterGeneratedQuestionItems(items []generatedQuestionListItem, query generatedQuestionListQuery) []generatedQuestionListItem {
	filtered := make([]generatedQuestionListItem, 0, len(items))
	keyword := foldedQuestionText(query.Keyword)
	for _, item := range items {
		if query.KnowledgeID != "" && item.KnowledgeID != query.KnowledgeID {
			continue
		}
		if query.ChunkID != "" && item.ChunkID != query.ChunkID {
			continue
		}
		if keyword != "" && !strings.Contains(foldedQuestionText(item.Question), keyword) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func assignGeneratedQuestionDuplicates(kbID string, items []generatedQuestionListItem) {
	counts := make(map[string]int)
	keys := make([]string, len(items))
	for index, item := range items {
		normalized := normalizedQuestionText(item.Question)
		if normalized == "" {
			continue
		}
		digest := sha256.Sum256([]byte("question-text-v1\x00" + kbID + "\x00" + normalized))
		key := "qdup-v1:" + hex.EncodeToString(digest[:])
		keys[index] = key
		counts[key]++
	}
	for index := range items {
		key := keys[index]
		items[index].DuplicateCount = 1
		if key == "" {
			continue
		}
		items[index].DuplicateGroupKey = &key
		items[index].DuplicateCount = counts[key]
		items[index].IsDuplicate = counts[key] > 1
	}
}

func applyGeneratedQuestionDuplicateMode(items []generatedQuestionListItem, mode string) []generatedQuestionListItem {
	if mode == "all" {
		return items
	}
	canonical := append([]generatedQuestionListItem{}, items...)
	sort.SliceStable(canonical, func(i, j int) bool { return canonicalQuestionLess(canonical[i], canonical[j]) })
	if mode == "duplicates" {
		filtered := make([]generatedQuestionListItem, 0)
		for _, item := range canonical {
			if item.IsDuplicate {
				filtered = append(filtered, item)
			}
		}
		return filtered
	}
	seen := make(map[string]struct{})
	unique := make([]generatedQuestionListItem, 0, len(canonical))
	for _, item := range canonical {
		if item.DuplicateGroupKey == nil {
			unique = append(unique, item)
			continue
		}
		if _, exists := seen[*item.DuplicateGroupKey]; exists {
			continue
		}
		seen[*item.DuplicateGroupKey] = struct{}{}
		unique = append(unique, item)
	}
	return unique
}

func sortGeneratedQuestionItems(items []generatedQuestionListItem, sortBy, sortOrder string) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if sortBy == "question_id" {
			leftID, rightID := generatedQuestionStringValue(left.QuestionID), generatedQuestionStringValue(right.QuestionID)
			if leftID == "" && rightID != "" {
				return false
			}
			if leftID != "" && rightID == "" {
				return true
			}
			if leftID != rightID {
				if sortOrder == "desc" {
					return leftID > rightID
				}
				return leftID < rightID
			}
		} else if left.ChunkIndex != right.ChunkIndex {
			if sortOrder == "desc" {
				return left.ChunkIndex > right.ChunkIndex
			}
			return left.ChunkIndex < right.ChunkIndex
		}
		return canonicalQuestionLess(left, right)
	})
}

func canonicalQuestionLess(left, right generatedQuestionListItem) bool {
	if left.ChunkIndex != right.ChunkIndex {
		return left.ChunkIndex < right.ChunkIndex
	}
	if left.KnowledgeID != right.KnowledgeID {
		return left.KnowledgeID < right.KnowledgeID
	}
	if left.ChunkID != right.ChunkID {
		return left.ChunkID < right.ChunkID
	}
	if left.QuestionPosition != right.QuestionPosition {
		return left.QuestionPosition < right.QuestionPosition
	}
	return generatedQuestionStringValue(left.QuestionID) < generatedQuestionStringValue(right.QuestionID)
}

func normalizedQuestionText(value string) string {
	value = norm.NFC.String(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	return cases.Fold().String(value)
}

func foldedQuestionText(value string) string {
	return cases.Fold().String(norm.NFC.String(value))
}

func generatedQuestionStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// writeGeneratedQuestionTrainingExport emits question, answer, and
// question-to-positive-passage training examples. Answers are extracted from
// the exact saved source revision; unavailable source text is never replaced
// with the current chunk body.
func (h *ChunkHandler) writeGeneratedQuestionTrainingExport(
	c *gin.Context,
	items []generatedQuestionListItem,
	chunks []*types.Chunk,
) {
	chunkByID := make(map[string]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			chunkByID[chunk.ID] = chunk
		}
	}

	resolver := newGeneratedQuestionSourceResolver(c.Request.Context(), h)
	extractor, canExtractAnswers := h.service.(generatedQuestionAnswerExtractor)
	type cachedAnswer struct {
		answer  string
		modelID string
	}
	answerCache := make(map[string]cachedAnswer)
	var body strings.Builder

	for _, item := range items {
		record := generatedQuestionTrainingRecord{
			Query: item.Question,
			Label: 1,
			Metadata: generatedQuestionTrainingMetadata{
				KnowledgeBaseID: item.KnowledgeBaseID, KnowledgeID: item.KnowledgeID,
				KnowledgeTitle: item.KnowledgeTitle, ChunkID: item.ChunkID,
				ChunkIndex: item.ChunkIndex, QuestionID: item.QuestionID,
				QuestionPosition: item.QuestionPosition, QuestionRevision: item.QuestionRevision,
				CurrentChunkRevision: item.CurrentChunkRevision, RevisionState: item.RevisionState,
				ChunkAvailability: item.ChunkAvailability, IndexStatus: item.IndexStatus,
				IsDuplicate: item.IsDuplicate, DuplicateCount: item.DuplicateCount,
			},
		}
		reasons := make([]string, 0, 4)
		chunk := chunkByID[item.ChunkID]
		sourceContent, sourceStatus, sourceReasons := resolver.resolve(item, chunk)
		record.PositivePassage = sourceContent
		reasons = append(reasons, sourceReasons...)
		if strings.TrimSpace(item.Question) == "" {
			reasons = append(reasons, "question_missing")
		}
		if strings.TrimSpace(record.PositivePassage) == "" {
			reasons = append(reasons, "source_text_empty")
		}
		switch {
		case strings.TrimSpace(item.Question) == "":
			record.AnswerStatus = "missing_question"
		case strings.TrimSpace(record.PositivePassage) == "":
			record.AnswerStatus = "source_unavailable"
		default:
			if !canExtractAnswers {
				c.Error(errors.NewServiceUnavailableError("Question answer extraction is unavailable"))
				return
			}
			cacheDigest := sha256.Sum256([]byte(item.KnowledgeBaseID + "\x00" + item.KnowledgeID + "\x00" + item.Question + "\x00" + record.PositivePassage))
			cacheKey := hex.EncodeToString(cacheDigest[:])
			cached, exists := answerCache[cacheKey]
			if !exists {
				answer, modelID, err := extractor.ExtractGeneratedQuestionAnswer(
					c.Request.Context(), item.KnowledgeBaseID, item.KnowledgeID, item.Question, record.PositivePassage,
				)
				if err != nil {
					logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
						"knowledge_base_id": item.KnowledgeBaseID,
						"chunk_id":          item.ChunkID,
						"question_id":       generatedQuestionStringValue(item.QuestionID),
					})
					c.Error(errors.NewServiceUnavailableError("Could not extract all training answers"))
					return
				}
				cached = cachedAnswer{answer: strings.TrimSpace(answer), modelID: modelID}
				answerCache[cacheKey] = cached
			}
			record.Answer = cached.answer
			record.Metadata.AnswerModelID = cached.modelID
			if cached.answer == "" {
				record.AnswerStatus = "not_found"
				reasons = append(reasons, "answer_not_found")
			} else {
				record.AnswerStatus = "answered"
			}
		}
		record.SourceRevisionStatus = sourceStatus
		record.TrainingExclusion = reasons
		record.TrainingReady = len(reasons) == 0

		line, err := json.Marshal(record)
		if err != nil {
			c.Error(errors.NewInternalServerError("Failed to encode training corpus"))
			return
		}
		if body.Len()+len(line)+1 > generatedQuestionMaxTrainingBytes {
			c.Error(errors.NewBadRequestError("Training export exceeds 50 MiB; narrow the filters and retry"))
			return
		}
		body.Write(line)
		body.WriteByte('\n')
	}

	c.Header("Content-Disposition", `attachment; filename="weknora-retrieval-training.jsonl"`)
	c.Data(http.StatusOK, "application/x-ndjson; charset=utf-8", []byte(body.String()))
}

func (h *ChunkHandler) writeGeneratedQuestionExport(
	c *gin.Context,
	format string,
	items []generatedQuestionListItem,
	rawTotal int,
	warnings int,
) {
	if format == "json" {
		c.JSON(http.StatusOK, gin.H{
			"success":       true,
			"data":          items,
			"total":         len(items),
			"raw_total":     rawTotal,
			"data_warnings": warnings,
			"page":          1,
			"page_size":     len(items),
		})
		return
	}

	var body []byte
	contentType := "text/plain; charset=utf-8"
	filename := "generated-questions.txt"
	if format == "csv" {
		contentType = "text/csv; charset=utf-8"
		filename = "generated-questions.csv"
		var buffer bytes.Buffer
		buffer.Write([]byte{0xEF, 0xBB, 0xBF})
		writer := csv.NewWriter(&buffer)
		_ = writer.Write([]string{
			"question", "question_id", "knowledge_base_id", "knowledge_id", "knowledge_title",
			"wiki_id", "task_id", "generated_at", "chunk_id", "chunk_index", "question_position",
			"question_revision", "current_chunk_revision", "revision_state", "chunk_availability",
			"index_status", "task_state", "question_data_state", "is_duplicate", "duplicate_group_key", "duplicate_count",
		})
		for _, item := range items {
			questionRevision := ""
			if item.QuestionRevision != nil {
				questionRevision = strconv.Itoa(*item.QuestionRevision)
			}
			if err := writer.Write([]string{
				safeCSVCell(item.Question), safeCSVCell(generatedQuestionStringValue(item.QuestionID)), safeCSVCell(item.KnowledgeBaseID),
				safeCSVCell(item.KnowledgeID), safeCSVCell(generatedQuestionStringValue(item.KnowledgeTitle)), safeCSVCell(generatedQuestionStringValue(item.WikiID)),
				safeCSVCell(generatedQuestionStringValue(item.TaskID)), safeCSVCell(generatedQuestionStringValue(item.GeneratedAt)), safeCSVCell(item.ChunkID),
				strconv.Itoa(item.ChunkIndex), strconv.Itoa(item.QuestionPosition), questionRevision,
				strconv.Itoa(item.CurrentChunkRevision), item.RevisionState, item.ChunkAvailability, item.IndexStatus,
				item.TaskState, item.QuestionDataState, strconv.FormatBool(item.IsDuplicate),
				safeCSVCell(generatedQuestionStringValue(item.DuplicateGroupKey)), strconv.Itoa(item.DuplicateCount),
			}); err != nil {
				c.Error(errors.NewInternalServerError("Failed to encode generated question export"))
				return
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			c.Error(errors.NewInternalServerError("Failed to encode generated question export"))
			return
		}
		body = buffer.Bytes()
	} else {
		filename = "generated-questions.txt"
		var builder strings.Builder
		for _, item := range items {
			doc := generatedQuestionStringValue(item.KnowledgeTitle)
			if doc == "" {
				doc = item.KnowledgeID
			}
			fmt.Fprintf(&builder, "%s\n[document=%s; knowledge_id=%s; chunk_index=%d; chunk_id=%s; question_id=%s]\n\n",
				item.Question, doc, item.KnowledgeID, item.ChunkIndex, item.ChunkID, generatedQuestionStringValue(item.QuestionID))
		}
		body = []byte(builder.String())
	}
	if len(body) > generatedQuestionMaxExportBytes {
		c.Error(errors.NewBadRequestError("Export exceeds 10 MiB; narrow the filters and retry"))
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, contentType, body)
}

func safeCSVCell(value string) string {
	for _, r := range value {
		if unicode.IsSpace(r) {
			continue
		}
		if r == '=' || r == '+' || r == '-' || r == '@' {
			return "'" + value
		}
		break
	}
	return value
}
