package types

import "time"

// GeneratedQuestionAnswer stores an answer extracted from the exact source
// revision associated with a generated question. ID is a stable hash of the
// tenant, question identity, revision, question text, and source text hash.
type GeneratedQuestionAnswer struct {
	ID                   string    `gorm:"type:varchar(64);primaryKey"`
	TenantID             uint64    `gorm:"not null;index:idx_generated_question_answers_chunk;index:idx_generated_question_answers_kb_question,priority:1"`
	KnowledgeBaseID      string    `gorm:"type:varchar(36);not null;index:idx_generated_question_answers_kb_question,priority:2"`
	KnowledgeID          string    `gorm:"type:varchar(36);not null"`
	ChunkID              string    `gorm:"type:varchar(36);not null;index:idx_generated_question_answers_chunk;index:idx_generated_question_answers_kb_question,priority:3"`
	QuestionID           string    `gorm:"type:varchar(128);not null;default:''"`
	QuestionPosition     int       `gorm:"not null"`
	QuestionRevision     *int      `gorm:"default:null;index:idx_generated_question_answers_kb_question,priority:4"`
	Question             string    `gorm:"type:text;not null"`
	SourceHash           string    `gorm:"type:varchar(64);not null"`
	Answer               string    `gorm:"type:text;not null;default:''"`
	AnswerStatus         string    `gorm:"type:varchar(32);not null"`
	AnswerModelID        string    `gorm:"type:varchar(255);not null;default:''"`
	SourceRevisionStatus string    `gorm:"type:varchar(32);not null"`
	CreatedAt            time.Time `gorm:"not null"`
	UpdatedAt            time.Time `gorm:"not null"`
}

// TableName specifies the persistence table for generated question answers.
func (GeneratedQuestionAnswer) TableName() string {
	return "generated_question_answers"
}
