package bootstrap

import (
	"context"

	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/dicomretrieve"
)

// Reindex mở container (cùng cấu hình với server: từ chối nếu schema chưa migrate) và lập chỉ mục frame cho mọi project. Dùng kho chưa tách project vì đây là công cụ vận hành
// chạy bằng thông tin kết nối cơ sở dữ liệu, không qua token.
func Reindex(ctx context.Context, s config.Settings, o dicomretrieve.ReindexOptions) (dicomretrieve.ReindexReport, error) {
	c, err := New(ctx, s)
	if err != nil {
		return dicomretrieve.ReindexReport{}, err
	}
	defer func() { _ = c.Stop(context.Background()) }()
	return dicomretrieve.Reindex(ctx, dicomretrieve.Config{Repo: c.Store, Blobs: c.Blobs}, o)
}
