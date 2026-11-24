package bootstrap

import (
	"context"
	"errors"

	"trivita-fhir/apps/dicomweb/internal/presentation/httpapi"
	"trivita-fhir/pkg/dicomstow"
)

// stower nối presentation với pkg/dicomstow: dùng kho resource đã tách project của người gọi và kho nội dung của Container.
type stower struct{ c *Container }

func (s stower) Stow(ctx context.Context, req httpapi.StowRequest) (httpapi.StowResult, error) {
	cfg := dicomstow.Config{
		Repo: s.c.Repo, Blobs: s.c.Blobs,
		MaxInstanceBytes: int64(s.c.Settings.MaxInstanceBytes), MaxHeaderBytes: int64(s.c.Settings.MaxHeaderBytes),
	}
	out, err := dicomstow.Store(ctx, cfg, dicomstow.Request{ContentType: req.ContentType, Body: req.Body, StudyUID: req.StudyUID, RetrieveBase: req.RetrieveBase})
	var re *dicomstow.RequestError
	if errors.As(err, &re) {
		return httpapi.StowResult{}, &httpapi.StatusError{Status: re.Status, Msg: re.Msg}
	}
	if err != nil {
		return httpapi.StowResult{}, err
	}
	return httpapi.StowResult{Status: out.Status, Body: out.Response}, nil
}
