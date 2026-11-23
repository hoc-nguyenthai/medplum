package messaging

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Kênh động và sổ hiện diện trên Redis: PUBLISH/SUBSCRIBE (không bền) và ZSET có điểm là thời điểm hết hạn.

func (b *Redis) channelName(c string) string { return b.prefix + ":ch:" + c }
func (b *Redis) presenceKey(k string) string { return b.prefix + ":presence:" + k }

// PublishChannel: xem Channels.
func (b *Redis) PublishChannel(ctx context.Context, channel string, payload []byte) error {
	return b.client.Publish(ctx, b.channelName(channel), payload).Err()
}

// SubscribeChannel: xem Channels. Chờ Redis xác nhận đăng ký rồi mới trả về nên thông điệp phát sau đó không bị lọt.
// go-redis tự nối lại và đăng ký lại khi mất kết nối; thông điệp phát trong lúc đứt bị mất (không bền).
func (b *Redis) SubscribeChannel(ctx context.Context, channel string, h ChannelHandler) (func(), error) {
	ps := b.client.Subscribe(ctx, b.channelName(channel))
	if _, err := ps.Receive(ctx); err != nil {
		_ = ps.Close()
		return nil, fmt.Errorf("messaging: đăng ký kênh %s: %w", channel, err)
	}
	msgs := ps.Channel(redis.WithChannelSize(256))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for m := range msgs {
			callChannel(h, []byte(m.Payload))
		}
	}()
	b.chMu.Lock()
	id := b.chNext
	b.chNext++
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			_ = ps.Close()
			b.chMu.Lock()
			delete(b.chSubs, id)
			b.chMu.Unlock()
		})
		<-done
	}
	b.chSubs[id] = cancel
	b.chMu.Unlock()
	return cancel, nil
}

func (b *Redis) closeChannels() {
	b.chMu.Lock()
	cs := make([]func(), 0, len(b.chSubs))
	for _, c := range b.chSubs {
		cs = append(cs, c)
	}
	b.chMu.Unlock()
	for _, c := range cs {
		c()
	}
}

// Lua dùng đồng hồ của Redis (TIME) để mọi instance so cùng một đồng hồ, không phụ thuộc lệch giờ giữa các máy.
var (
	presenceMark = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
local cur = redis.call('PTTL', KEYS[1])
if cur < tonumber(ARGV[2]) then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
return 1`)
	presencePresent = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
return redis.call('ZCOUNT', KEYS[1], '(' .. now, '+inf')`)
)

// Mark: xem Presence. ttl < 1ms được làm tròn lên 1ms.
func (b *Redis) Mark(ctx context.Context, key, member string, ttl time.Duration) error {
	ms := ttl.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return presenceMark.Run(ctx, b.client, []string{b.presenceKey(key)}, member, strconv.FormatInt(ms, 10)).Err()
}

// Unmark: xem Presence.
func (b *Redis) Unmark(ctx context.Context, key, member string) error {
	return b.client.ZRem(ctx, b.presenceKey(key), member).Err()
}

// Present: xem Presence.
func (b *Redis) Present(ctx context.Context, key string) (bool, error) {
	n, err := presencePresent.Run(ctx, b.client, []string{b.presenceKey(key)}).Int64()
	return n > 0, err
}
