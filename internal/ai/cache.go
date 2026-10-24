package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EkeMinusYou/gelf/internal/config"
)

const commitCacheMaxAge = time.Hour
const commitCacheMaxBytes = 128 * 1024

// CommitCache stores only the most recent successful initial generation per worktree.
// Cache failures are misses, so unavailable storage never prevents generation.
type CommitCache struct {
	Dir string
	Now func() time.Time
}

func NewCommitCache() *CommitCache {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	return &CommitCache{Dir: filepath.Join(dir, "gelf", "commits")}
}

type commitCacheScope struct {
	Repository, Project, Location, APIVersion, Model, Thinking, Endpoint, FullDiffHash string
}

type commitCacheEntry struct {
	Key       string
	CreatedAt time.Time
	Message   string
}

type cachedCommitClient struct {
	Client
	cache *CommitCache
	scope commitCacheScope
}

func (c *CommitCache) Wrap(client Client, cfg *config.Config, model, root, fullDiff string) Client {
	if c == nil || c.Dir == "" || root == "" {
		return client
	}
	return &cachedCommitClient{Client: client, cache: c, scope: commitCacheScope{
		Repository: root, Project: cfg.ProjectID, Location: cfg.Location,
		APIVersion: cfg.APIVersion, Model: model, Thinking: cfg.CommitThinking,
		Endpoint: os.Getenv("GOOGLE_VERTEX_BASE_URL"), FullDiffHash: cacheHash(fullDiff),
	}}
}

func (c *cachedCommitClient) GenerateCommitMessage(ctx context.Context, input CommitInput) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prompt := initialCommitPrompt(input)
	identity, _ := json.Marshal(struct {
		Version int
		Scope   commitCacheScope
		Prompt  string
	}{1, c.scope, prompt})
	key := cacheHash(string(identity))
	path := filepath.Join(c.cache.Dir, cacheHash(c.scope.Repository)+".json")
	if message, ok := c.cache.load(path, key); ok {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if input.OnProgress != nil {
			input.OnProgress(message)
		}
		return message, nil
	}
	message, err := c.Client.GenerateCommitMessage(ctx, input)
	if err == nil && ctx.Err() == nil && strings.TrimSpace(message) != "" && utf8.ValidString(message) {
		c.cache.store(path, commitCacheEntry{Key: key, CreatedAt: c.cache.now(), Message: message})
	}
	return message, err
}

func (c *CommitCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func cacheHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func (c *CommitCache) load(path, key string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, commitCacheMaxBytes+1))
	if err != nil || len(data) > commitCacheMaxBytes {
		return "", false
	}
	var entry commitCacheEntry
	if json.Unmarshal(data, &entry) != nil {
		return "", false
	}
	age := c.now().Sub(entry.CreatedAt)
	if age < 0 || age > commitCacheMaxAge {
		_ = os.Remove(path)
		return "", false
	}
	if entry.Key != key || strings.TrimSpace(entry.Message) == "" || !utf8.ValidString(entry.Message) {
		return "", false
	}
	return entry.Message, true
}

func (c *CommitCache) store(path string, entry commitCacheEntry) {
	data, err := json.Marshal(entry)
	if err != nil || len(data) > commitCacheMaxBytes {
		return
	}
	if os.MkdirAll(c.Dir, 0700) != nil {
		return
	}
	f, err := os.CreateTemp(c.Dir, ".commit-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr == nil && closeErr == nil {
		_ = os.Rename(f.Name(), path)
	}
}
