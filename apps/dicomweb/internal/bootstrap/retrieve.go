package bootstrap

import (
	"context"
	"errors"
	"io"
	"net/url"

	"trivita-fhir/apps/dicomweb/internal/presentation/httpapi"
	"trivita-fhir/pkg/dicomretrieve"
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

func (r retriever) Search(ctx context.Context, level, studyUID, seriesUID string, params url.Values) (httpapi.QueryPage, error) {
	q, err := dicomretrieve.ParseQIDO(params)
	if err != nil {
		return httpapi.QueryPage{}, status(err)
	}
	lv := dicomretrieve.LevelStudy
	switch level {
	case "series":
		lv = dicomretrieve.LevelSeries
	case "instance":
		lv = dicomretrieve.LevelInstance
	}
	p, err := dicomretrieve.Search(ctx, r.cfg(), lv, dicomretrieve.Scope{StudyUID: studyUID, SeriesUID: seriesUID}, q)
	if err != nil {
		return httpapi.QueryPage{}, status(err)
	}
	out := httpapi.QueryPage{Items: p.Items, More: p.More, Ignored: p.Ignored}
	if lv == dicomretrieve.LevelInstance {
		out.Items = p.Raw
	}
	return out, nil
}

func (r retriever) Metadata(ctx context.Context, studyUID, seriesUID, instanceUID string) (func(io.Writer) error, error) {
	var (
		w   func(io.Writer) error
		err error
	)
	switch {
	case instanceUID != "":
		w, err = dicomretrieve.InstanceMetadata(ctx, r.cfg(), studyUID, seriesUID, instanceUID)
	case seriesUID != "":
		w, err = dicomretrieve.SeriesMetadata(ctx, r.cfg(), studyUID, seriesUID)
	default:
		w, err = dicomretrieve.StudyMetadata(ctx, r.cfg(), studyUID)
	}
	if err != nil {
		return nil, status(err)
	}
	return w, nil
}

func (r retriever) Download(ctx context.Context, studyUID, seriesUID, instanceUID string) (httpapi.Download, error) {
	d, err := dicomretrieve.OpenInstances(ctx, r.cfg(), studyUID, seriesUID, instanceUID)
	if err != nil {
		return httpapi.Download{}, status(err)
	}
	return httpapi.Download{ContentType: d.ContentType, Write: d.Write}, nil
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

func (r retriever) Rendered(ctx context.Context, studyUID, seriesUID, instanceUID, frame, accept string, params url.Values) (httpapi.Rendered, error) {
	n := 1
	if frame != "" {
		nums, err := dicomretrieve.ParseFrames(frame)
		if err != nil || len(nums) != 1 {
			return httpapi.Rendered{}, &httpapi.StatusError{Status: 400, Msg: "Invalid frame number"}
		}
		n = nums[0]
	}
	o, err := dicomretrieve.RenderOptions(accept, params)
	if err != nil {
		return httpapi.Rendered{}, status(err)
	}
	out, err := dicomretrieve.RenderFrame(ctx, r.cfg(), studyUID, seriesUID, instanceUID, n, o)
	if err != nil {
		return httpapi.Rendered{}, status(err)
	}
	return httpapi.Rendered{ContentType: out.ContentType, Body: out.Body}, nil
}
