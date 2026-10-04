package attempts

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// gradingCacheTTL keeps a loaded material version around for an hour. A
// reading test costs several round trips to load, the mistakes page is visited
// repeatedly in one session, and a published material version is immutable:
// updating a material always inserts a new version row, which the cache keys on.
const gradingCacheTTL = time.Hour

// gradingCacheSize bounds the memory used by the cache; the oldest arbitrary
// entries are dropped once it is full.
const gradingCacheSize = 256

type gradingCacheKey struct {
	materialType string
	materialID   uuid.UUID
	versionID    uuid.UUID
}

type gradingCacheEntry struct {
	material  GradingMaterial
	expiresAt time.Time
}

// gradingCache memoizes immutable material versions so repeated views of the
// mistakes page do not pay for the same structure again. GradingMaterial is
// only read after loading, never mutated.
type gradingCache struct {
	mu      sync.Mutex
	entries map[gradingCacheKey]gradingCacheEntry
}

func newGradingCache() *gradingCache {
	return &gradingCache{entries: map[gradingCacheKey]gradingCacheEntry{}}
}

func (c *gradingCache) get(materialType string, ref MaterialRef) (GradingMaterial, bool) {
	if c == nil {
		return GradingMaterial{}, false
	}
	key := gradingCacheKey{materialType: materialType, materialID: ref.MaterialID, versionID: ref.VersionID}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return GradingMaterial{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return GradingMaterial{}, false
	}
	return entry.material, true
}

func (c *gradingCache) put(materialType string, ref MaterialRef, material GradingMaterial) {
	if c == nil {
		return
	}
	key := gradingCacheKey{materialType: materialType, materialID: ref.MaterialID, versionID: ref.VersionID}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= gradingCacheSize {
		c.evict()
	}
	c.entries[key] = gradingCacheEntry{material: material, expiresAt: time.Now().Add(gradingCacheTTL)}
}

// evict drops expired entries first and, if the cache is still full, one
// arbitrary entry. It runs only when the cache is at its size limit.
func (c *gradingCache) evict() {
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < gradingCacheSize {
		return
	}
	for key := range c.entries {
		delete(c.entries, key)
		break
	}
}
