package dicomretrieve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"trivita-fhir/pkg/dicomrender"
	"trivita-fhir/pkg/dicomweb"
)

// maxRenderBytes là trần kích thước dữ liệu một frame được dựng ảnh (đọc hết vào bộ nhớ). Frame lớn hơn: 413.
const maxRenderBytes = 64 << 20

// Rendered là ảnh đã dựng của một frame.
type Rendered struct {
	ContentType string
	Body        []byte
}

// RenderOptions phân tích tham số truy vấn của `rendered`: window=center,width[,linear] và quality=1..100. Các tham số khác (viewport, annotation, iccprofile) bị bỏ qua.
func RenderOptions(accept string, params url.Values) (dicomrender.Options, error) {
	format, ok := dicomrender.Negotiate(accept)
	if !ok {
		return dicomrender.Options{}, &Error{http.StatusNotAcceptable, "Chỉ hỗ trợ image/jpeg và image/png"}
	}
	o := dicomrender.Options{Format: format}
	if v := params.Get("quality"); v != "" {
		q, err := strconv.Atoi(v)
		if err != nil || q < 1 || q > 100 {
			return o, &Error{http.StatusBadRequest, "quality phải từ 1 đến 100"}
		}
		o.Quality = q
	}
	if v := params.Get("window"); v != "" {
		f := strings.Split(v, ",")
		if len(f) < 2 || len(f) > 3 || (len(f) == 3 && f[2] != "linear") {
			return o, &Error{http.StatusBadRequest, "window phải có dạng center,width[,linear]"}
		}
		c, err1 := strconv.ParseFloat(f[0], 64)
		w, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil || w <= 0 {
			return o, &Error{http.StatusBadRequest, "window: center và width phải là số, width > 0"}
		}
		o.Center, o.Width = c, w
	}
	return o, nil
}

func num(ds dicomweb.Dataset, tag string, def float64) float64 {
	el, ok := ds[tag]
	if !ok || len(el.Value) == 0 {
		return def
	}
	switch v := el.Value[0].(type) {
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

// renderParams lấy thuộc tính ảnh từ metadata đã lưu của instance.
func renderParams(metadata string) (dicomrender.Params, error) {
	var ds dicomweb.Dataset
	if err := json.Unmarshal([]byte(metadata), &ds); err != nil {
		return dicomrender.Params{}, err
	}
	return dicomrender.Params{
		Rows: int(num(ds, "00280010", 0)), Cols: int(num(ds, "00280011", 0)), Samples: int(num(ds, "00280002", 1)),
		BitsAllocated: int(num(ds, "00280100", 0)), BitsStored: int(num(ds, "00280101", 0)), PixelRepresentation: int(num(ds, "00280103", 0)),
		PlanarConfiguration: int(num(ds, "00280006", 0)), Photometric: ds.Str("00280004"),
		Slope: num(ds, "00281053", 1), Intercept: num(ds, "00281052", 0), Center: num(ds, "00281050", 0), Width: num(ds, "00281051", 0),
	}, nil
}

// RenderFrame dựng ảnh JPEG hoặc PNG của một frame (số thứ tự từ 1). Chỉ dựng được JPEG baseline và dữ liệu không nén xám 8 hoặc 16 bit, RGB 8 bit; còn lại 406. Dữ liệu một frame
// được đọc hết vào bộ nhớ (tối đa maxRenderBytes).
func RenderFrame(ctx context.Context, c Config, studyUID, seriesUID, instanceUID string, frame int, o dicomrender.Options) (*Rendered, error) {
	_, inst, e := c.instance(ctx, studyUID, seriesUID, instanceUID)
	if e != nil {
		return nil, e
	}
	params, err := renderParams(inst.Metadata)
	if err != nil {
		return nil, internalErr(ctx, "metadata instance", errors.New(inst.ID))
	}
	f, err := OpenFrames(ctx, c, studyUID, seriesUID, instanceUID, []int{frame})
	if err != nil {
		return nil, err
	}
	if f.Length > maxRenderBytes {
		return nil, &Error{http.StatusRequestEntityTooLarge, "Frame quá lớn để dựng ảnh"}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, internalErr(ctx, "đọc frame", err)
	}
	_, mp, _ := mime.ParseMediaType(f.ContentType)
	part, err := multipart.NewReader(&buf, mp["boundary"]).NextPart()
	if err != nil {
		return nil, internalErr(ctx, "đọc phần frame", err)
	}
	data, err := io.ReadAll(part)
	if err != nil {
		return nil, internalErr(ctx, "đọc phần frame", err)
	}
	mt, mtp, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
	var body []byte
	var ct string
	switch {
	case mt == "application/octet-stream" && mtp["transfer-syntax"] == "":
		body, ct, err = dicomrender.FromNative(params, data, o)
	case mt == "image/jpeg" && mtp["transfer-syntax"] == "1.2.840.10008.1.2.4.50":
		body, ct, err = dicomrender.FromJPEG(data, o)
	default:
		return nil, &Error{http.StatusNotAcceptable, "Chưa hỗ trợ dựng ảnh cho dữ liệu này (" + part.Header.Get("Content-Type") + ")"}
	}
	switch {
	case errors.Is(err, dicomrender.ErrUnsupported):
		return nil, &Error{http.StatusNotAcceptable, "Chưa hỗ trợ dựng ảnh cho định dạng điểm ảnh này"}
	case err != nil:
		return nil, internalErr(ctx, "dựng ảnh", err)
	}
	return &Rendered{ContentType: ct, Body: body}, nil
}
