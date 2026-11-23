package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"trivita-fhir/apps/ws-gateway/internal/bootstrap"
	"trivita-fhir/pkg/fhirauth"
)

// collect đọc giá trị hiện tại của mọi instrument (kiểu int64, gauge hoặc sum) theo tên.
func collect(t *testing.T, r *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, p := range d.DataPoints {
					out[m.Name] = p.Value
				}
			case metricdata.Sum[int64]:
				for _, p := range d.DataPoints {
					out[m.Name] = p.Value
				}
			}
		}
	}
	return out
}

func TestGatewayMetricsReportConnectionsBindingsAndEvents(t *testing.T) {
	mr := newRedisT(t)
	f := newFhirSide(t, mr, signingKey)
	g := newGateway(t, settingsFor(mr, nil))
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	unregister, err := g.c.RegisterMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}

	got := collect(t, reader)
	for _, name := range []string{bootstrap.MetricConnections, bootstrap.MetricBindings, bootstrap.MetricSubscribed, bootstrap.MetricSent,
		bootstrap.MetricSlow, bootstrap.MetricDenied, bootstrap.MetricReceived, bootstrap.MetricMalformed, bootstrap.MetricRedisUp} {
		if _, ok := got[name]; !ok {
			t.Errorf("thiếu instrument %s: %v", name, got)
		}
	}
	if got[bootstrap.MetricConnections] != 0 || got[bootstrap.MetricRedisUp] != 1 {
		t.Fatalf("ban đầu: %v", got)
	}

	f.subscription("s1", "Observation")
	f.subscription("s2", "Observation")
	alice := &fhirauth.Principal{Subject: "alice"}
	c1 := g.bound(t, f.token("s1", alice))
	// cùng một kết nối bind thêm s2: số kết nối (3), số bind (4) và số Subscription (2) đều khác nhau để phân biệt ba gauge
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c1.Write(ctx, websocket.MessageText, bindMsg(f.token("s2", alice))); err != nil {
		t.Fatal(err)
	}
	readMsg(t, c1) // handshake s2
	denied := g.bound(t, f.token("s1", &fhirauth.Principal{Subject: "bob", Grants: fhirauth.Grants{"Patient": fhirauth.PermRead}}))
	carol := g.bound(t, f.token("s1", &fhirauth.Principal{Subject: "carol"}))
	f.create(obs("o1")) // khớp cả s1 và s2: c1 nhận hai thông báo, carol một, bob (chỉ bind s1) bị từ chối
	readMsg(t, c1)
	readMsg(t, c1)
	readMsg(t, carol)

	got = collect(t, reader)
	if got[bootstrap.MetricConnections] != 3 || got[bootstrap.MetricBindings] != 4 || got[bootstrap.MetricSubscribed] != 2 {
		t.Errorf("gauge: %v", got)
	}
	if got[bootstrap.MetricReceived] != 2 {
		t.Errorf("received = %d, muốn 2 (một thông điệp Redis cho mỗi Subscription khớp)", got[bootstrap.MetricReceived])
	}
	if got[bootstrap.MetricDenied] != 1 {
		t.Errorf("denied = %d, muốn 1 (bob không đọc được Observation)", got[bootstrap.MetricDenied])
	}
	if got[bootstrap.MetricSent] < 7 { // 4 handshake + 3 event
		t.Errorf("sent = %d, muốn >= 7", got[bootstrap.MetricSent])
	}
	if got[bootstrap.MetricSlow] != 0 || got[bootstrap.MetricMalformed] != 0 {
		t.Errorf("slow/malformed: %v", got)
	}

	_ = c1.Close(websocket.StatusNormalClosure, "")
	_ = denied.Close(websocket.StatusNormalClosure, "")
	_ = carol.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, "kết nối được gỡ khỏi gauge", func() bool { return collect(t, reader)[bootstrap.MetricConnections] == 0 })

	// sau khi hủy đăng ký không còn dữ liệu của các instrument này
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	if after := collect(t, reader); len(after) != 0 {
		t.Errorf("sau unregister vẫn còn dữ liệu: %v", after)
	}
}

func TestGatewayMetricRedisUpFollowsRedis(t *testing.T) {
	mr := newRedisT(t)
	g := newGateway(t, settingsFor(mr, nil))
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if _, err := g.c.RegisterMetrics(provider.Meter("test")); err != nil {
		t.Fatal(err)
	}
	if got := collect(t, reader)[bootstrap.MetricRedisUp]; got != 1 {
		t.Fatalf("redis.up = %d, muốn 1", got)
	}
	mr.ri.Stop(t)
	if got := collect(t, reader)[bootstrap.MetricRedisUp]; got != 0 {
		t.Errorf("Redis chết: redis.up = %d, muốn 0", got)
	}
}
