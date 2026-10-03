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
	toolFingerprintTTL = 2 * time.Minute
	confirmationKeyTTL = 5 * time.Minute
)

// reserveToolCall uses Redis SET NX EX as a small distributed token bucket:
// one identical tool call can enter a conversation during the TTL window.
// The local map remains the fallback for demo mode and Redis outages.
func (s *Service) reserveToolCall(ctx context.Context, userID int64, conversationID, fingerprint string, local map[string]bool) (bool, error) {
	if local[fingerprint] {
		return false, nil
	}
	if s.redisPool == nil {
		local[fingerprint] = true
		return true, nil
	}

	digest := sha256.Sum256([]byte(fingerprint))
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
