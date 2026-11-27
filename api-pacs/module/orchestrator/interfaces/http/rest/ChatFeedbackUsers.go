package rest

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// ChatFeedbackUser is how the admin panel names a voter.
type ChatFeedbackUser struct {
	Name  string
	Email string
}

// ChatFeedbackUserDirectory lists a workspace's users by id (Firebase UID),
// the ids the chat backend records on votes.
type ChatFeedbackUserDirectory interface {
	Users(ctx context.Context, tenantID string) (map[string]ChatFeedbackUser, error)
}

// ChatFeedbackUserLister reads a workspace's users (the Members page source).
type ChatFeedbackUserLister func(ctx context.Context, tenantID string) (map[string]ChatFeedbackUser, error)

const chatFeedbackUsersTTL = 5 * time.Minute

type cachedChatFeedbackUsers struct {
	users   map[string]ChatFeedbackUser
	fetched time.Time
}

// CachedChatFeedbackUsers keeps each workspace's directory for five minutes:
// listing users walks the identity provider, too slow for every page load.
type CachedChatFeedbackUsers struct {
	List  ChatFeedbackUserLister
	mu    sync.Mutex
	cache map[string]cachedChatFeedbackUsers
	now   func() time.Time
}

func (directory *CachedChatFeedbackUsers) clock() time.Time {
	if directory.now != nil {
		return directory.now()
	}
	return time.Now()
}

// Users returns the workspace's users, from the cache when fresh.
func (directory *CachedChatFeedbackUsers) Users(ctx context.Context, tenantID string) (map[string]ChatFeedbackUser, error) {
	directory.mu.Lock()
	entry, ok := directory.cache[tenantID]
	directory.mu.Unlock()
	if ok && directory.clock().Sub(entry.fetched) < chatFeedbackUsersTTL {
		return entry.users, nil
	}
	users, err := directory.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	directory.mu.Lock()
	if directory.cache == nil {
		directory.cache = map[string]cachedChatFeedbackUsers{}
	}
	directory.cache[tenantID] = cachedChatFeedbackUsers{users: users, fetched: directory.clock()}
	directory.mu.Unlock()
	return users, nil
}

// nameChatFeedbackUsers adds user_name and user_email next to each user_id of
// a chat backend answer: the list ({"items": [...]}) or one vote ({...}). The
// body is returned unchanged when the directory is missing or fails, or when
// the body is not the expected shape: names are a convenience, not a gate.
func (controller *OrchestratorController) nameChatFeedbackUsers(ctx context.Context, tenantID string, body json.RawMessage) json.RawMessage {
	if controller.ChatFeedbackUsers == nil || tenantID == "" || len(body) == 0 {
		return body
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(body, &doc); err != nil {
		return body
	}
	var targets []map[string]interface{}
	if items, ok := doc["items"].([]interface{}); ok {
		for _, raw := range items {
			if item, ok := raw.(map[string]interface{}); ok {
				targets = append(targets, item)
			}
		}
	} else if _, ok := doc["user_id"]; ok {
		targets = append(targets, doc)
	}
	if len(targets) == 0 {
		return body
	}
	users, err := controller.ChatFeedbackUsers.Users(ctx, tenantID)
	if err != nil {
		log.Printf("chat feedback: user directory unavailable: %v", err)
		return body
	}
	for _, item := range targets {
		id, _ := item["user_id"].(string)
		if user, ok := users[id]; ok && id != "" {
			if user.Name != "" {
				item["user_name"] = user.Name
			}
			if user.Email != "" {
				item["user_email"] = user.Email
			}
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}
