package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// wikiResumeCheckpointVersion gates the shape and semantics of durable Wiki
// post-processing artifacts stored in knowledge_processing_spans.output.
// Bump it whenever a checkpoint is no longer safe to reuse.
const wikiResumeCheckpointVersion = 1

type wikiCheckpointSpanReader interface {
	CheckpointSpans(context.Context, string) []types.KnowledgeProcessingSpan
}

type wikiCheckpointHistory struct {
	spans []types.KnowledgeProcessingSpan
}

func (h *wikiCheckpointHistory) find(spanName, fingerprint string) types.JSONMap {
	if h == nil {
		return nil
	}
	for i := len(h.spans) - 1; i >= 0; i-- {
		row := h.spans[i]
		if row.Name == spanName && row.Status == types.SpanStatusDone && wikiCheckpointMatches(row.Output, fingerprint) {
			return row.Output
		}
	}
	return nil
}

func wikiCheckpointFingerprint(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		// The values passed here are JSON-compatible structs and strings. Keep a
		// deterministic failure result rather than disabling persistence.
		payload = []byte(fmt.Sprintf("checkpoint-marshal-error:%T", value))
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:])
}

func wikiCheckpointOutput(fingerprint string, data types.JSONMap) types.JSONMap {
	out := types.JSONMap{
		"checkpoint_version":     wikiResumeCheckpointVersion,
		"checkpoint_fingerprint": fingerprint,
	}
	for key, value := range data {
		out[key] = value
	}
	return out
}

func wikiCheckpointMatches(output types.JSONMap, fingerprint string) bool {
	if output == nil || output["checkpoint_fingerprint"] != fingerprint {
		return false
	}
	var version int
	if !decodeWikiCheckpointValue(output["checkpoint_version"], &version) {
		return false
	}
	return version == wikiResumeCheckpointVersion
}

func decodeWikiCheckpointValue(value any, target any) bool {
	if value == nil {
		return false
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return json.Unmarshal(payload, target) == nil
}

func slugItemsFromExtracted(entities, concepts []extractedItem) map[string]extractedItem {
	out := make(map[string]extractedItem, len(entities)+len(concepts))
	for _, item := range entities {
		if item.Slug != "" && item.Name != "" {
			out[item.Slug] = item
		}
	}
	for _, item := range concepts {
		if item.Slug != "" && item.Name != "" {
			out[item.Slug] = item
		}
	}
	return out
}

func wikiPageCheckpointDigest(page *types.WikiPage) string {
	if page == nil {
		return ""
	}
	return wikiCheckpointFingerprint(struct {
		Slug       string
		Title      string
		PageType   string
		Status     string
		Content    string
		Summary    string
		Aliases    types.StringArray
		FolderID   string
		SourceRefs types.StringArray
		ChunkRefs  types.StringArray
		OutLinks   types.StringArray
		Version    int
	}{page.Slug, page.Title, page.PageType, page.Status, page.Content, page.Summary,
		page.Aliases, page.FolderID, page.SourceRefs, page.ChunkRefs, page.OutLinks, page.Version})
}

// findWikiCheckpoint returns the newest completed checkpoint matching both
// stage name and input fingerprint. Legacy trace rows and partial/in-flight
// spans are intentionally ignored.
func (s *wikiIngestService) findWikiCheckpoint(
	ctx context.Context,
	knowledgeID, spanName, fingerprint string,
) types.JSONMap {
	return s.loadWikiCheckpointHistory(ctx, knowledgeID).find(spanName, fingerprint)
}

func (s *wikiIngestService) loadWikiCheckpointHistory(ctx context.Context, knowledgeID string) *wikiCheckpointHistory {
	reader, ok := s.tracker().(wikiCheckpointSpanReader)
	if !ok || knowledgeID == "" {
		return &wikiCheckpointHistory{}
	}
	rows := reader.CheckpointSpans(ctx, knowledgeID)
	return &wikiCheckpointHistory{spans: rows}
}
