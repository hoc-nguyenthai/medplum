package ratelimit

import "github.com/redis/go-redis/v9"

// NewRedisClient mở client Redis từ URL (redis:// hoặc rediss://) cho NewRedis. Gọi Close khi dừng.
func NewRedisClient(url string) (*redis.Client, error) {
	o, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(o), nil
}
