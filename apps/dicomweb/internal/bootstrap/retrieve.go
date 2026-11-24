package bootstrap

import (
	"context"
	"errors"
	"io"

	"trivita-fhir/apps/dicomweb/internal/presentation/httpapi"
	"trivita-fhir/pkg/dicomretrieve"
	"trivita-fhir/pkg/dicomweb"
)

// retriever nối presentation với pkg/dicomretrieve (QIDO-RS, WADO-RS).
type retriever struct{ c *Container }

func (r retriever) cfg() dicomretrieve.Config {
	return dicomretrieve.Config{Repo: r.c.Repo, Blobs: r.c.Blobs}
}

// status đổi lỗi của dịch vụ thành lỗi có mã HTTP cho presentation.
func status(err error) error {
	var e *dicomretrieve.Error
	if errors.As(err, &e) {
		return &httpapi.StatusError{Status: e.Status, Msg: e.Msg}
	}
	return err
}

func (r retriever) SearchStudies(ctx context.Context, limit, offset string) (httpapi.QueryPage, error) {
	l, _ := dicomweb.ParseQueryInt(limit)
	o, _ := dicomweb.ParseQueryInt(offset)
	p, err := dicomretrieve.SearchStudies(ctx, r.cfg(), l, o)
	if err != nil {
		return httpapi.QueryPage{}, status(err)
	}
	return httpapi.QueryPage{Items: p.Items, More: p.More}, nil
}

func (r retriever) SearchSeries(ctx context.Context, studyUID, limit, offset string) (httpapi.QueryPage, error) {
	l, _ := dicomweb.ParseQueryInt(limit)
	o, _ := dicomweb.ParseQueryInt(offset)
	p, err := dicomretrieve.SearchSeries(ctx, r.cfg(), studyUID, l, o)
	if err != nil {
		return httpapi.QueryPage{}, status(err)
	}
	return httpapi.QueryPage{Items: p.Items, More: p.More}, nil
}

func (r retriever) SeriesMetadata(ctx context.Context, studyUID, seriesUID string) (func(io.Writer) error, error) {
	w, err := dicomretrieve.SeriesMetadata(ctx, r.cfg(), studyUID, seriesUID)
	return w, status(err)
}

func (r retriever) Frames(ctx context.Context, studyUID, seriesUID, instanceUID, frames string) (httpapi.FrameStream, error) {
	nums, err := dicomretrieve.ParseFrames(frames)
	if err != nil {
		return httpapi.FrameStream{}, status(err)
	}
	f, err := dicomretrieve.OpenFrames(ctx, r.cfg(), studyUID, seriesUID, instanceUID, nums)
	if err != nil {
		return httpapi.FrameStream{}, status(err)
	}
	return httpapi.FrameStream{ContentType: f.ContentType, Length: f.Length, Write: f.Write}, nil
}
