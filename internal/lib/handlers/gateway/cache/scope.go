package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"sync"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// Tenant scoping for every response cache.
//
// The gateway runs one engine (one exact LRU, one semantic index) for every
// tenant it serves. Every entry is therefore owned by the authenticated tenant
// that produced it, and every lookup is confined to the caller's tenant:
//
//   - exact keys hash the tenant ID ahead of the request fingerprint, and the
//     stored entry carries TenantID, which is re-checked on read (xxhash64 is
//     not collision resistant, so the key alone is not the boundary);
//   - semantic entries live in a partition derived from the tenant AND the
//     full request context minus the final user turn (model, system prompt /
//     instructions, prior turns, tools, sampling), so similarity is only
//     measured between queries asked under the same context by the same tenant.
//
// No tenant in context means no caching at all (fail closed), never a shared
// "global" bucket.

// exactKeyVersion is mixed into every exact key. Bumping it makes every entry
// written under an older key format unreachable.
const exactKeyVersion = "evs-exact-v2"

// semanticPartitionVersion plays the same role for semantic partitions.
const semanticPartitionVersion = "evs-semantic-v2"

// TenantFromContext returns the authenticated tenant for cache scoping: the
// tenant the API-key / session middleware installed (never a client header).
// Empty means the request must not read or write any cache.
func TenantFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(contextkeys.ExtractTenantID(ctx))
}

// SemanticPartition derives the semantic-cache partition for a tenant and a
// request context fingerprint. It returns "" when tenantID is empty or the
// fingerprint is nil, which callers must treat as "do not cache". The result is lowercase hex, safe to
// use verbatim as a RediSearch TAG value.
func SemanticPartition(tenantID string, contextFingerprint []byte) string {
	if tenantID == "" || contextFingerprint == nil {
		return ""
	}
	h := sha256.New()
	writeField(h, []byte(semanticPartitionVersion))
	writeField(h, []byte(tenantID))
	writeField(h, contextFingerprint)
	return hex.EncodeToString(h.Sum(nil))
}

type byteWriter interface{ Write([]byte) (int, error) }

// writeField writes a length-prefixed field so adjacent fields can never be
// re-split into a different, colliding sequence.
func writeField(w byteWriter, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = w.Write(n[:])
	_, _ = w.Write(b)
}

// FieldFingerprint is a canonical encoding of the fields exposed by the
// ChatRequest accessors, in message order. Adapters whose underlying request
// carries more output-affecting state (tools, response format, instructions)
// must fingerprint that state themselves instead.
func FieldFingerprint(req ChatRequest) []byte {
	return fieldFingerprint(req, -1)
}

// FieldSemanticContextFingerprint is FieldFingerprint with the content of the
// last user message (the semantic query) left out.
func FieldSemanticContextFingerprint(req ChatRequest) []byte {
	msgs := req.GetMessages()
	skip := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].GetRole() == "user" {
			skip = i
			break
		}
	}
	return fieldFingerprint(req, skip)
}

func fieldFingerprint(req ChatRequest, skipContentAt int) []byte {
	var b strings.Builder
	w := stringsWriter{&b}
	writeField(w, []byte(req.GetModel()))
	for i, msg := range req.GetMessages() {
		writeField(w, []byte(msg.GetRole()))
		if i == skipContentAt {
			writeField(w, []byte("\x00query"))
			continue
		}
		writeField(w, []byte(msg.GetContent()))
	}
	writeField(w, []byte(formatFloat(req.GetTemperature())))
	writeField(w, []byte(formatInt(req.GetMaxTokens())))
	writeField(w, []byte(formatFloat(req.GetTopP())))
	return []byte(b.String())
}

type stringsWriter struct{ b *strings.Builder }

func (s stringsWriter) Write(p []byte) (int, error) { return s.b.Write(p) }

// cloneForStore returns a shallow copy so ownership fields set by one cache
// never race with, or leak into, the copy another cache stores.
func cloneForStore(resp *CachedResponse) *CachedResponse {
	c := *resp
	return &c
}

// FingerprintMemo computes a request's fingerprints at most once. Adapters
// embed it (by value, used through a pointer receiver) so the exact and
// semantic lookups and stores of one request share a single encoding.
type FingerprintMemo struct {
	exactOnce    sync.Once
	semanticOnce sync.Once
	exact        []byte
	semantic     []byte
}

// Exact returns compute() on first use and the cached result afterwards.
func (m *FingerprintMemo) Exact(compute func() []byte) []byte {
	m.exactOnce.Do(func() { m.exact = compute() })
	return m.exact
}

// Semantic returns compute() on first use and the cached result afterwards.
func (m *FingerprintMemo) Semantic(compute func() []byte) []byte {
	m.semanticOnce.Do(func() { m.semantic = compute() })
	return m.semantic
}
