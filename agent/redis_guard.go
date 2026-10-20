package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mediocregopher/radix/v3"
)

const (
	toolFingerprintTTL        = 2 * time.Minute
	confirmationKeyTTL        = 5 * time.Minute
	toolBucketCapacity        = 5
	toolBucketRefillPerSecond = 1
)

func sessionStateKey(userID int64, conversationID string) string {
	return fmt.Sprintf("agent:session-state:%d:%s", userID, conversationID)
}

func (s *Service) persistSessionState(userID int64, conversationID string, state SessionState) {
	if s.redisPool == nil || conversationID == "" {
		return
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return
	}
	// Compare the version inside Redis before replacing the snapshot. This
	// prevents a slower request from overwriting a newer state update.
	const script = `local current=redis.call('GET',KEYS[1]); if current then local old=cjson.decode(current); if tonumber(old.version or 0)>tonumber(ARGV[2]) then return 0 end end; redis.call('SET',KEYS[1],ARGV[1],'EX',ARGV[3]); return 1`
	_ = s.redisPool.Do(radix.Cmd(nil, "EVAL", script, "1", sessionStateKey(userID, conversationID), string(encoded), strconv.FormatInt(state.Version, 10), strconv.Itoa(int(time.Hour/time.Second))))
}

func (s *Service) loadSessionState(userID int64, conversationID string) (SessionState, bool) {
	if s.redisPool == nil || conversationID == "" {
		return SessionState{}, false
	}
	var encoded string
	if err := s.redisPool.Do(radix.Cmd(&encoded, "GET", sessionStateKey(userID, conversationID))); err != nil || encoded == "" {
		return SessionState{}, false
	}
	var state SessionState
	if json.Unmarshal([]byte(encoded), &state) != nil {
		return SessionState{}, false
	}
	return state, true
}

const tokenBucketScript = `
local values = redis.call('HMGET', KEYS[1], 'tokens', 'updated')
local capacity = tonumber(ARGV[1])
local refill = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local tokens = tonumber(values[1]) or capacity
local updated = tonumber(values[2]) or now
tokens = math.min(capacity, tokens + math.max(0, now - updated) * refill / 1000)
if tokens < 1 then
  redis.call('HSET', KEYS[1], 'tokens', tokens, 'updated', now)
  redis.call('EXPIRE', KEYS[1], 30)
  return 0
end
redis.call('HSET', KEYS[1], 'tokens', tokens - 1, 'updated', now)
redis.call('EXPIRE', KEYS[1], 30)
return 1
`

// reserveToolCall first consumes one Redis token from the per-conversation
// bucket, then records the exact tool fingerprint with SET NX EX. The local
// map remains the fallback for demo mode and Redis outages.
func (s *Service) reserveToolCall(ctx context.Context, userID int64, conversationID, fingerprint string, local map[string]bool) (bool, error) {
	if local[fingerprint] {
		return false, nil
	}
	if s.redisPool == nil {
		local[fingerprint] = true
		return true, nil
	}

	digest := sha256.Sum256([]byte(fingerprint))
	bucketKey := fmt.Sprintf("agent:bucket:%d:%s", userID, conversationID)
	var allowed int
	now := time.Now().UnixMilli()
	if err := s.redisPool.Do(radix.Cmd(&allowed, "EVAL", tokenBucketScript, "1", bucketKey,
		strconv.Itoa(toolBucketCapacity), strconv.Itoa(toolBucketRefillPerSecond), strconv.FormatInt(now, 10))); err != nil {
		return false, err
	}
	if allowed != 1 {
		return false, nil
	}
	key := fmt.Sprintf("agent:tool:%d:%s:%s", userID, conversationID, hex.EncodeToString(digest[:]))
	var reply string
	err := s.redisPool.Do(radix.Cmd(&reply, "SET", key, "1", "NX", "EX", strconv.Itoa(int(toolFingerprintTTL/time.Second))))
	if err != nil {
		return false, err
	}
	return strings.EqualFold(reply, "OK"), nil
}

func confirmationKey(token string) string {
	return "agent:confirmation:" + token
}

func (s *Service) storeConfirmation(token string, action pendingAction) error {
	if s.redisPool == nil {
		return nil
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return err
	}
	return s.redisPool.Do(radix.Cmd(nil, "SET", confirmationKey(token), string(encoded), "NX", "EX", strconv.Itoa(int(confirmationKeyTTL/time.Second))))
}

// consumeConfirmation atomically reads and deletes a token in Redis. Lua is
// used so two API instances cannot both observe the same valid token.
func (s *Service) consumeConfirmation(ctx context.Context, token string) (pendingAction, bool) {
	if s.redisPool != nil {
		const script = "local value=redis.call('GET',KEYS[1]); if not value then return '' end; redis.call('DEL',KEYS[1]); return value"
		var encoded string
		if err := s.redisPool.Do(radix.Cmd(&encoded, "EVAL", script, "1", confirmationKey(token))); err != nil {
			// Do not fall back after an ambiguous Redis result: the script may
			// already have consumed the token before the connection failed.
			return pendingAction{}, false
		} else if encoded != "" {
			var action pendingAction
			if json.Unmarshal([]byte(encoded), &action) == nil {
				return action, true
			}
			return pendingAction{}, false
		}
		return pendingAction{}, false
	}

	s.mu.Lock()
	action, ok := s.pending[token]
	if ok {
		delete(s.pending, token)
	}
	s.mu.Unlock()
	return action, ok
}

func (s *Service) deleteConfirmation(token string) {
	if s.redisPool != nil {
		_ = s.redisPool.Do(radix.Cmd(nil, "DEL", confirmationKey(token)))
	}
}
