package fhircast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore là Store trên Redis, dùng được cho nhiều instance. Không dùng Lua (so sánh-rồi-đặt bằng WATCH/MULTI/EXEC, thử
// lại có giới hạn). Mọi khóa có hạn; project luôn nằm trong khóa; topic được băm.
type RedisStore struct {
	c      redis.UniversalClient
	opt    StoreOptions
	prefix string
}

const casRetries = 8

// NewRedisStore tạo kho; prefix mặc định "fhircast". opt.Now không dùng (Redis tự tính hạn).
func NewRedisStore(c redis.UniversalClient, prefix string, opt StoreOptions) *RedisStore {
	opt.defaults()
	if prefix == "" {
		prefix = "fhircast"
	}
	return &RedisStore{c: c, opt: opt, prefix: prefix}
}

func (s *RedisStore) k(parts ...string) string {
	k := s.prefix
	for _, p := range parts {
		k += ":" + p
	}
	return k
}
func (s *RedisStore) subKey(ep string) string      { return s.k("ep", ep) }
func (s *RedisStore) ctxKey(p, t string) string    { return s.k("p", p, "t", TopicHash(t), "latest") }
func (s *RedisStore) ctxsKey(p, t string) string   { return s.k("p", p, "t", TopicHash(t), "ctxs") }
func (s *RedisStore) userTopicKey(u string) string { return s.k("user-topic", u) }

func (s *RedisStore) PutSubscription(ctx context.Context, endpoint string, sub Subscription, ttl time.Duration) error {
	if ttl <= 0 || ttl > s.opt.Lease {
		ttl = s.opt.Lease
	}
	b, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	return s.c.Set(ctx, s.subKey(endpoint), b, ttl).Err()
}

func (s *RedisStore) GetSubscription(ctx context.Context, endpoint string) (Subscription, bool, error) {
	b, err := s.c.Get(ctx, s.subKey(endpoint)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Subscription{}, false, nil
	}
	if err != nil {
		return Subscription{}, false, err
	}
	sub, ok := DecodeSubscription(b)
	return sub, ok, nil
}

func (s *RedisStore) DeleteSubscription(ctx context.Context, endpoint string) error {
	return s.c.Del(ctx, s.subKey(endpoint)).Err()
}

func (s *RedisStore) GetContext(ctx context.Context, p, t string) (CurrentContext, bool, error) {
	b, err := s.c.Get(ctx, s.ctxKey(p, t)).Bytes()
	if errors.Is(err, redis.Nil) {
		return CurrentContext{}, false, nil
	}
	if err != nil {
		return CurrentContext{}, false, err
	}
	cc, ok := DecodeContext(b)
	return cc, ok, nil
}

func (s *RedisStore) SetContext(ctx context.Context, p, t string, cc CurrentContext) error {
	b, err := s.opt.encodeContext(cc)
	if err != nil {
		return err
	}
	return s.c.Set(ctx, s.ctxKey(p, t), b, s.opt.ContextTTL).Err()
}

func (s *RedisStore) CompareAndSetContext(ctx context.Context, p, t, expected string, cc CurrentContext) (bool, error) {
	b, err := s.opt.encodeContext(cc)
	if err != nil {
		return false, err
	}
	key := s.ctxKey(p, t)
	for i := 0; i < casRetries; i++ {
		replaced := false
		err := s.c.Watch(ctx, func(tx *redis.Tx) error {
			cur, err := tx.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				return nil
			}
			if err != nil {
				return err
			}
			old, ok := DecodeContext(cur)
			if !ok || old.VersionID != expected {
				return nil
			}
			_, err = tx.TxPipelined(ctx, func(pl redis.Pipeliner) error {
				pl.Set(ctx, key, b, s.opt.ContextTTL)
				return nil
			})
			if err == nil {
				replaced = true
			}
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue // có người ghi giữa lúc đọc và ghi: đọc lại và so lại
		}
		return replaced, err
	}
	return false, fmt.Errorf("fhircast: tranh chấp ghi ngữ cảnh quá %d lần", casRetries)
}

func (s *RedisStore) ClearContext(ctx context.Context, p, t string) error {
	return s.c.Del(ctx, s.ctxKey(p, t)).Err()
}

func (s *RedisStore) SaveReportContext(ctx context.Context, p, t, id string, cc CurrentContext) error {
	b, err := s.opt.encodeContext(cc)
	if err != nil {
		return err
	}
	key := s.ctxsKey(p, t)
	for i := 0; i < casRetries; i++ {
		err := s.c.Watch(ctx, func(tx *redis.Tx) error {
			exists, err := tx.HExists(ctx, key, id).Result()
			if err != nil {
				return err
			}
			if !exists {
				n, err := tx.HLen(ctx, key).Result()
				if err != nil {
					return err
				}
				if int(n) >= s.opt.MaxReportContexts {
					return ErrTooManyContexts
				}
			}
			_, err = tx.TxPipelined(ctx, func(pl redis.Pipeliner) error {
				pl.HSet(ctx, key, id, b)
				pl.Expire(ctx, key, s.opt.ContextTTL)
				return nil
			})
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return fmt.Errorf("fhircast: tranh chấp ghi ngữ cảnh đã lưu quá %d lần", casRetries)
}

func (s *RedisStore) GetReportContext(ctx context.Context, p, t, id string) (CurrentContext, bool, error) {
	b, err := s.c.HGet(ctx, s.ctxsKey(p, t), id).Bytes()
	if errors.Is(err, redis.Nil) {
		return CurrentContext{}, false, nil
	}
	if err != nil {
		return CurrentContext{}, false, err
	}
	cc, ok := DecodeContext(b)
	return cc, ok, nil
}

func (s *RedisStore) DeleteReportContext(ctx context.Context, p, t, id string) error {
	return s.c.HDel(ctx, s.ctxsKey(p, t), id).Err()
}

func (s *RedisStore) DeleteReportContexts(ctx context.Context, p, t string) error {
	return s.c.Del(ctx, s.ctxsKey(p, t)).Err()
}

// UserTopic: SET NX, GET, EXPIRE trong một MULTI (như Medplum): topic ổn định chừng nào còn xin ít nhất mỗi lease.
func (s *RedisStore) UserTopic(ctx context.Context, userID string) (string, error) {
	key := s.userTopicKey(userID)
	var get *redis.StringCmd
	_, err := s.c.TxPipelined(ctx, func(pl redis.Pipeliner) error {
		pl.SetNX(ctx, key, NewID(), s.opt.Lease)
		get = pl.Get(ctx, key)
		pl.Expire(ctx, key, s.opt.Lease)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fhircast: không lấy được topic người dùng: %w", err)
	}
	return get.Val(), nil
}
