package bootstrap

import (
	"context"

	"go.opentelemetry.io/otel/metric"
)

// Tên instrument (OTLP, đẩy qua trivita-observability-go khi OTEL_METRICS_ENABLED=true; mặc định thư viện tắt metric).
const (
	MetricConnections = "ws_gateway.connections"      // kết nối websocket đang mở
	MetricBindings    = "ws_gateway.bindings"         // lượt bind Subscription đang hiệu lực
	MetricSubscribed  = "ws_gateway.subscriptions"    // Subscription đang nghe kênh Redis
	MetricSent        = "ws_gateway.messages.sent"    // thông báo đã xếp vào hàng đợi gửi (tích lũy)
	MetricSlow        = "ws_gateway.connections.slow" // kết nối bị ngắt vì người nhận quá chậm (tích lũy)
	MetricDenied      = "ws_gateway.messages.denied"  // thông báo bị bỏ vì người nhận không đủ quyền đọc hoặc hết hạn (tích lũy)
	MetricReceived    = "ws_gateway.events.received"  // sự kiện nhận từ Redis và đã giao cho Hub (tích lũy)
	MetricMalformed   = "ws_gateway.events.malformed" // thông điệp Redis hỏng hoặc sai kênh, bị bỏ (tích lũy)
	MetricRedisUp     = "ws_gateway.redis.up"         // 1 nếu Redis dùng được ở lần đọc gần nhất
)

// RegisterMetrics đăng ký các instrument quan sát (đọc Stats của Hub và Feed khi thư viện thu thập, không có đường nóng nào bị
// chậm đi vì metric). meter thường là observability.Meter(...); trả hàm hủy đăng ký.
func (c *Container) RegisterMetrics(meter metric.Meter) (unregister func() error, err error) {
	gauge := func(name, desc string) (metric.Int64ObservableGauge, error) {
		return meter.Int64ObservableGauge(name, metric.WithDescription(desc), metric.WithUnit("{item}"))
	}
	counter := func(name, desc string) (metric.Int64ObservableCounter, error) {
		return meter.Int64ObservableCounter(name, metric.WithDescription(desc), metric.WithUnit("{item}"))
	}
	var (
		conns, bindings, subscribed, redisUp metric.Int64ObservableGauge
		sent, slow, denied, received, bad    metric.Int64ObservableCounter
	)
	for _, step := range []func() error{
		func() (e error) { conns, e = gauge(MetricConnections, "Kết nối websocket đang mở"); return },
		func() (e error) {
			bindings, e = gauge(MetricBindings, "Lượt bind Subscription đang hiệu lực")
			return
		},
		func() (e error) {
			subscribed, e = gauge(MetricSubscribed, "Subscription đang nghe kênh Redis")
			return
		},
		func() (e error) { redisUp, e = gauge(MetricRedisUp, "1 nếu Redis dùng được"); return },
		func() (e error) {
			sent, e = counter(MetricSent, "Thông báo đã xếp vào hàng đợi gửi")
			return
		},
		func() (e error) {
			slow, e = counter(MetricSlow, "Kết nối bị ngắt vì người nhận quá chậm")
			return
		},
		func() (e error) {
			denied, e = counter(MetricDenied, "Thông báo bị bỏ vì người nhận không đủ quyền")
			return
		},
		func() (e error) { received, e = counter(MetricReceived, "Sự kiện nhận từ Redis"); return },
		func() (e error) {
			bad, e = counter(MetricMalformed, "Thông điệp Redis hỏng hoặc sai kênh")
			return
		},
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	reg, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		hs, fs := c.Hub.Stats(), c.Feed.Stats()
		o.ObserveInt64(conns, int64(hs.Conns))
		o.ObserveInt64(bindings, int64(hs.Bindings))
		o.ObserveInt64(subscribed, int64(fs.Subscribed))
		o.ObserveInt64(sent, hs.Sent)
		o.ObserveInt64(slow, hs.Slow)
		o.ObserveInt64(denied, hs.Denied)
		o.ObserveInt64(received, fs.Received)
		o.ObserveInt64(bad, fs.Malformed)
		pctx, cancel := context.WithTimeout(ctx, c.Settings.ReadinessTimeout)
		defer cancel()
		up := int64(0)
		if c.Redis.Ping(pctx) == nil {
			up = 1
		}
		o.ObserveInt64(redisUp, up)
		return nil
	}, conns, bindings, subscribed, redisUp, sent, slow, denied, received, bad)
	if err != nil {
		return nil, err
	}
	return reg.Unregister, nil
}
