// Package featureflag is the runtime feature matrix. The backend exposes
// it at GET /api/v1/features; the frontend hides every entry point whose
// flag is false (in particular all AI entries when no key is configured).
//
// Defaults derive from the build tag tier; a config file / env overrides
// land on top later (small_business admin console writes that config).
package featureflag

import "github.com/supplider/supplider/backend/internal/tier"

// Features is the runtime capability matrix.
type Features struct {
	Tier string `json:"tier"`

	// AI features — false on personal MVP until a provider is configured.
	AIEnabled      bool `json:"ai_enabled"`
	AIOCREntry     bool `json:"ai_ocr_entry"`
	AIDocSearch    bool `json:"ai_doc_search"`
	AINLSearch     bool `json:"ai_nl_search"`
	AIExcelMapping bool `json:"ai_excel_mapping"`

	// Collaboration / admin features.
	VisibilityLevels int  `json:"visibility_levels"` // number of levels enabled: personal = 2 (0/1)
	RBAC             bool `json:"rbac"`
	AuditLog         bool `json:"audit_log"`
	ApprovalFlow     bool `json:"approval_flow"`

	// Infrastructure selection (informational for /features; wiring
	// happens through build-tag factories).
	Storage       string `json:"storage"`        // sqlite | mongodb
	SearchEngine  string `json:"search_engine"`  // fts5 | meilisearch | elasticsearch
	VectorStore   string `json:"vector_store"`   // "" (none) | qdrant-embedded | milvus
	ObjectStorage string `json:"object_storage"` // localfs | minio | s3
	Queue         string `json:"queue"`          // channel | nats
}

// Default returns the feature matrix for the build-time tier.
func Default() Features {
	switch tier.Current() {
	case tier.Enterprise:
		return Features{
			Tier: tier.Enterprise.String(),
			// AI flips to true only when the gateway reports a configured
			// provider — see WithAIState; defaults stay conservative.
			VisibilityLevels: 5,
			RBAC:             true,
			AuditLog:         true,
			ApprovalFlow:     true,
			Storage:          "mongodb",
			SearchEngine:     "elasticsearch",
			VectorStore:      "milvus",
			ObjectStorage:    "s3",
			Queue:            "nats",
		}
	case tier.SmallBusiness:
		return Features{
			Tier:             tier.SmallBusiness.String(),
			VisibilityLevels: 5,
			RBAC:             true,
			AuditLog:         true,
			ApprovalFlow:     true,
			Storage:          "mongodb",
			SearchEngine:     "meilisearch",
			VectorStore:      "qdrant-embedded",
			ObjectStorage:    "minio",
			Queue:            "channel",
		}
	default: // personal
		return Features{
			Tier:             tier.Personal.String(),
			VisibilityLevels: 2, // levels 0 (self) and 1 (named users)
			Storage:          "sqlite",
			SearchEngine:     "fts5", // FTS5 transition; interface allows swap to embedded Meilisearch
			ObjectStorage:    "localfs",
			Queue:            "channel",
		}
	}
}

// WithAIState returns a copy with AI flags flipped from the live gateway:
// the chat-only features (OCR/Excel-map/NL-search) follow `enabled` (a
// provider is configured), while document/semantic search (AIDocSearch)
// additionally requires an embedding model (`canEmbed`) — the Anthropic
// Messages API has no /embeddings endpoint, so semantic search stays hidden
// on that format even though chat works (AI 原生但可降级).
func (f Features) WithAIState(enabled, canEmbed bool) Features {
	f.AIEnabled = enabled
	f.AIOCREntry = enabled
	f.AINLSearch = enabled
	f.AIExcelMapping = enabled
	f.AIDocSearch = canEmbed
	return f
}
