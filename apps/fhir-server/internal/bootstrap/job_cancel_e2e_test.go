package bootstrap_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// FHIR_JOB_CANCEL_POLL tới được router: job đang chạy dừng trong chưa tới 1 giây sau khi bị hủy ở nơi khác, dù lease 10 phút (nhịp gia hạn 200 giây).
func TestJobCancelPollSettingReachesTheRouter(t *testing.T) {
	c, _ := newServer(t, settings(withAdmin, func(s *config.Settings) {
		s.JobLease, s.JobCancelPoll = 10*time.Minute, 30*time.Millisecond
	}))
	started, stopped := make(chan struct{}), make(chan struct{})
	c.Router.RegisterJob("longcancel", func(ctx context.Context, _ fhirrouter.Repository, _ *fhirrouter.Input) (*fhir.Parameters, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	job, err := c.Router.StartJob(context.Background(), "longcancel", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("job không bắt đầu")
	}
	cur, err := c.Store.ReadResource(context.Background(), "AsyncJob", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	cj := cur.(*fhir.AsyncJob)
	cj.Status = "cancelled" // như replica khác xử lý DELETE: chỉ ghi vào kho, không gọi được hàm hủy trong tiến trình này
	if _, err := c.Store.UpdateResource(context.Background(), cj, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second): // thăm dò 30ms; nếu cấu hình không tới router thì mặc định 2 giây sẽ không kịp
		t.Fatal("job không dừng trong 1 giây sau khi bị hủy (FHIR_JOB_CANCEL_POLL không tới router?)")
	}
}
