package dicomweb

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"sort"
	"strconv"
	"strings"
)

// Phần này dựng tệp DICOM Part 10 từ metadata DICOM JSON kèm bulkdata (STOW-RS dạng metadata, PS3.18 10.5.1.2): phần đầu của yêu cầu là DICOM JSON, các phần sau là bulkdata
// (PixelData và nhị phân lớn) được tham chiếu bằng BulkDataURI. Kết quả luôn là Explicit VR Little Endian (dữ liệu điểm ảnh nén thì đóng gói một fragment mỗi frame), để đi qua cùng
// đường lưu như STOW tệp thường (quét, đọc metadata, chỉ mục frame).

// ErrEncode là lỗi dữ liệu đầu vào không dựng được thành DICOM (giá trị sai kiểu, thiếu UID, thiếu bulkdata...).
var ErrEncode = errors.New("dicomweb: không dựng được DICOM từ metadata")

func encErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrEncode, fmt.Sprintf(format, a...))
}

// Bulk là một bulkdata đã nhận (thường là tệp tạm): R đọc đúng Size byte.
type Bulk struct {
	R           io.Reader
	Size        int64
	ContentType string // Content-Type của phần chứa nó; quyết định transfer syntax khi là PixelData
}

// BulkResolver tìm bulkdata theo BulkDataURI của phần tử; ok=false nếu không có phần nào khớp. Mỗi bulkdata chỉ được dùng một lần (R là luồng).
type BulkResolver func(uri string) (*Bulk, bool)

// ImplementationClassUID đặt vào file meta của tệp dựng ra.
const ImplementationClassUID = "2.25.282475249025813012874183962307329182731"

const (
	tsExplicitLE = "1.2.840.10008.1.2.1"
	tsJPEGBase   = "1.2.840.10008.1.2.4.50"
	tsJPEGLS     = "1.2.840.10008.1.2.4.80"
	tsJPEG2K     = "1.2.840.10008.1.2.4.90"
	tsHTJ2K      = "1.2.840.10008.1.2.4.201"
)

var longForm = map[string]bool{"OB": true, "OD": true, "OF": true, "OL": true, "OV": true, "OW": true, "SQ": true, "UC": true, "UR": true, "UT": true, "UN": true}

// TransferSyntaxOfBulk suy ra transfer syntax của PixelData từ Content-Type của phần bulkdata (PS3.18 Table 8.7.3-5): tham số transfer-syntax nếu có, không thì theo media type
// (image/jpeg là JPEG baseline...), application/octet-stream và rỗng là Explicit VR Little Endian. Implicit VR Little Endian được dựng thành Explicit (cùng byte điểm ảnh). Big Endian và
// Deflated không hỗ trợ.
func TransferSyntaxOfBulk(contentType string) (string, error) {
	if strings.TrimSpace(contentType) == "" {
		return tsExplicitLE, nil
	}
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", encErr("Content-Type của bulkdata sai: %q", contentType)
	}
	if ts := strings.TrimSpace(params["transfer-syntax"]); ts != "" {
		switch ts {
		case tsImplicitLE, tsExplicitLE:
			return tsExplicitLE, nil
		case tsExplicitBE, tsDeflated:
			return "", encErr("transfer syntax %s không hỗ trợ", ts)
		}
		if !validUID(ts) {
			return "", encErr("transfer-syntax sai: %q", ts)
		}
		return ts, nil
	}
	switch mt {
	case "application/octet-stream":
		return tsExplicitLE, nil
	case "image/jpeg":
		return tsJPEGBase, nil
	case "image/jls":
		return tsJPEGLS, nil
	case "image/jp2":
		return tsJPEG2K, nil
	case "image/jphc":
		return tsHTJ2K, nil
	}
	return "", encErr("Content-Type %q của PixelData không hỗ trợ", mt)
}

// validUID: UID DICOM hợp lệ (số nối bằng dấu chấm, tối đa 64 ký tự).
func validUID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	prev := byte('.')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if prev == '.' {
				return false
			}
		} else if c < '0' || c > '9' {
			return false
		}
		prev = c
	}
	return prev != '.'
}

// ParseMetadataJSON đọc phần metadata của yêu cầu: mảng dataset (mỗi phần tử một instance) hoặc một dataset đơn.
func ParseMetadataJSON(raw []byte) ([]Dataset, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, encErr("metadata rỗng")
	}
	var out []Dataset
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, encErr("metadata không phải mảng DICOM JSON hợp lệ: %v", err)
		}
	} else {
		var one Dataset
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, encErr("metadata không phải DICOM JSON hợp lệ: %v", err)
		}
		out = []Dataset{one}
	}
	if len(out) == 0 {
		return nil, encErr("metadata không có instance nào")
	}
	return out, nil
}

// BuildPart10 dựng tệp DICOM Part 10 của một instance từ dataset DICOM JSON. Trả các đoạn nối nhau thành tệp (đoạn bulkdata là chính R của Bulk, không nạp vào bộ nhớ) và
// transfer syntax đã chọn. Cần SOPClassUID (0008,0016) và SOPInstanceUID (0008,0018). Các phần tử nhóm 0002 trong JSON bị bỏ (file meta do hàm dựng).
func BuildPart10(ds Dataset, resolve BulkResolver) (segs []io.Reader, transferSyntax string, err error) {
	classUID, sopUID := ds.Str("00080016"), ds.Str("00080018")
	if !validUID(classUID) || !validUID(sopUID) {
		return nil, "", encErr("thiếu hoặc sai SOPClassUID (0008,0016) hoặc SOPInstanceUID (0008,0018)")
	}
	e := &encoder{resolve: resolve}
	// transfer syntax: từ bulkdata PixelData nếu có
	transferSyntax = tsExplicitLE
	var pixel *Bulk
	if el, ok := ds[tagPixelData]; ok && el.BulkDataURI != "" {
		b, found := resolve(el.BulkDataURI)
		if !found {
			return nil, "", encErr("không có bulkdata cho PixelData (%s)", el.BulkDataURI)
		}
		pixel = b
		if transferSyntax, err = TransferSyntaxOfBulk(b.ContentType); err != nil {
			return nil, "", err
		}
	}
	e.encapsulated = transferSyntax != tsExplicitLE
	e.pixel = pixel

	if hasNonASCII(ds, 0) && len(ds["00080005"].Value) == 0 {
		ds = copyWith(ds, "00080005", Element{VR: "CS", Value: []any{"ISO_IR 192"}})
	}

	var body bytes.Buffer
	if err := e.dataset(&body, ds, 0, true); err != nil {
		return nil, "", err
	}
	meta := new(bytes.Buffer)
	writeElem(meta, 0x0002, 0x0001, "OB", []byte{0, 1})
	writeElem(meta, 0x0002, 0x0002, "UI", padUID(classUID))
	writeElem(meta, 0x0002, 0x0003, "UI", padUID(sopUID))
	writeElem(meta, 0x0002, 0x0010, "UI", padUID(transferSyntax))
	writeElem(meta, 0x0002, 0x0012, "UI", padUID(ImplementationClassUID))
	head := new(bytes.Buffer)
	head.Write(make([]byte, 128))
	head.WriteString("DICM")
	writeElem(head, 0x0002, 0x0000, "UL", binary.LittleEndian.AppendUint32(nil, uint32(meta.Len())))
	head.Write(meta.Bytes())

	segs = append(segs, head, &body)
	segs = append(segs, e.tail...)
	return segs, transferSyntax, nil
}

func copyWith(ds Dataset, tag string, el Element) Dataset {
	out := make(Dataset, len(ds)+1)
	for k, v := range ds {
		out[k] = v
	}
	out[tag] = el
	return out
}

// hasNonASCII: có chuỗi nào chứa byte ngoài ASCII (cần Specific Character Set ISO_IR 192 vì JSON là UTF-8).
func hasNonASCII(ds Dataset, depth int) bool {
	for _, el := range ds {
		for _, v := range el.Value {
			switch x := v.(type) {
			case string:
				for i := 0; i < len(x); i++ {
					if x[i] > 0x7F {
						return true
					}
				}
			case map[string]any:
				if el.VR == "SQ" && depth < MaxSQDepth {
					if sub, err := toDataset(x); err == nil && hasNonASCII(sub, depth+1) {
						return true
					}
				} else {
					for _, s := range x {
						if str, ok := s.(string); ok && strings.ContainsFunc(str, func(r rune) bool { return r > 0x7F }) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func toDataset(m map[string]any) (Dataset, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var ds Dataset
	if err := json.Unmarshal(b, &ds); err != nil {
		return nil, err
	}
	return ds, nil
}

type encoder struct {
	resolve      BulkResolver
	encapsulated bool
	pixel        *Bulk
	tail         []io.Reader // phần đứng sau bộ đệm chính (bulkdata PixelData và đoạn đóng): luôn là cuối tệp vì PixelData là phần tử cuối
}

func padUID(s string) []byte {
	b := []byte(s)
	if len(b)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func writeElem(w *bytes.Buffer, group, element uint16, vr string, value []byte) {
	w.Write(binary.LittleEndian.AppendUint16(nil, group))
	w.Write(binary.LittleEndian.AppendUint16(nil, element))
	w.WriteString(vr)
	if longForm[vr] {
		w.Write([]byte{0, 0})
		w.Write(binary.LittleEndian.AppendUint32(nil, uint32(len(value))))
	} else {
		w.Write(binary.LittleEndian.AppendUint16(nil, uint16(len(value))))
	}
	w.Write(value)
}

func parseTag(k string) (g, e uint16, err error) {
	if len(k) != 8 {
		return 0, 0, encErr("tag %q phải có 8 chữ số hex", k)
	}
	v, perr := strconv.ParseUint(k, 16, 32)
	if perr != nil {
		return 0, 0, encErr("tag %q không phải hex", k)
	}
	return uint16(v >> 16), uint16(v), nil
}

// dataset ghi các phần tử theo thứ tự tag tăng dần. top: cấp ngoài cùng (cho phép BulkDataURI và PixelData).
func (e *encoder) dataset(w *bytes.Buffer, ds Dataset, depth int, top bool) error {
	if depth > MaxSQDepth {
		return encErr("SQ lồng quá %d lớp", MaxSQDepth)
	}
	keys := make([]string, 0, len(ds))
	for k := range ds {
		keys = append(keys, strings.ToUpper(k))
	}
	sort.Strings(keys)
	norm := make(map[string]Element, len(ds))
	for k, v := range ds {
		norm[strings.ToUpper(k)] = v
	}
	for _, k := range keys {
		g, el, err := parseTag(k)
		if err != nil {
			return err
		}
		if g == 0x0002 {
			continue
		}
		if top && len(e.tail) > 0 {
			return encErr("phần tử %s đứng sau PixelData không hỗ trợ", k)
		}
		elem := norm[k]
		if k == tagPixelData {
			if !top {
				return encErr("PixelData không được nằm trong SQ")
			}
			if err := e.pixelData(w, elem); err != nil {
				return err
			}
			continue
		}
		if err := e.element(w, g, el, elem, depth, top); err != nil {
			return fmt.Errorf("%w (tag %s)", err, k)
		}
	}
	return nil
}

func (e *encoder) pixelData(w *bytes.Buffer, el Element) error {
	vr := el.VR
	if vr == "" {
		vr = "OW"
	}
	if vr != "OB" && vr != "OW" {
		return encErr("PixelData phải có VR OB hoặc OW")
	}
	if el.BulkDataURI == "" {
		// PixelData nhỏ gửi inline: không nén
		if el.InlineBinary == "" {
			return encErr("PixelData không có BulkDataURI hay InlineBinary")
		}
		data, err := base64.StdEncoding.DecodeString(el.InlineBinary)
		if err != nil {
			return encErr("PixelData InlineBinary không phải base64")
		}
		if e.encapsulated {
			return encErr("PixelData inline không đóng gói được")
		}
		if len(data)%2 == 1 {
			data = append(data, 0)
		}
		writeElem(w, 0x7FE0, 0x0010, vr, data)
		return nil
	}
	b := e.pixel
	if b == nil {
		return encErr("không có bulkdata cho PixelData")
	}
	w.Write([]byte{0xE0, 0x7F, 0x10, 0x00})
	w.WriteString(vr)
	w.Write([]byte{0, 0})
	if !e.encapsulated {
		size := b.Size
		odd := size%2 == 1
		if odd {
			size++
		}
		w.Write(binary.LittleEndian.AppendUint32(nil, uint32(size)))
		e.tail = append(e.tail, b.R)
		if odd {
			e.tail = append(e.tail, bytes.NewReader([]byte{0}))
		}
		return nil
	}
	// đóng gói: bảng offset rỗng, một fragment (độ dài chẵn), Sequence Delimitation
	w.Write(binary.LittleEndian.AppendUint32(nil, 0xFFFFFFFF))
	w.Write([]byte{0xFE, 0xFF, 0x00, 0xE0, 0, 0, 0, 0}) // Item BOT rỗng
	size := b.Size
	odd := size%2 == 1
	if odd {
		size++
	}
	if size > math.MaxUint32-16 {
		return encErr("bulkdata PixelData quá lớn")
	}
	w.Write([]byte{0xFE, 0xFF, 0x00, 0xE0})
	w.Write(binary.LittleEndian.AppendUint32(nil, uint32(size)))
	e.tail = append(e.tail, b.R)
	if odd {
		e.tail = append(e.tail, bytes.NewReader([]byte{0}))
	}
	e.tail = append(e.tail, bytes.NewReader([]byte{0xFE, 0xFF, 0xDD, 0xE0, 0, 0, 0, 0}))
	return nil
}

func (e *encoder) element(w *bytes.Buffer, g, el uint16, elem Element, depth int, top bool) error {
	vr := elem.VR
	if len(vr) != 2 {
		return encErr("thiếu VR")
	}
	var value []byte
	var err error
	switch vr {
	case "SQ":
		return e.sequence(w, g, el, elem, depth)
	case "OB", "OD", "OF", "OL", "OV", "OW", "UN":
		value, err = e.binaryValue(elem, top)
		if value != nil && len(value)%2 == 1 {
			value = append(value, 0)
		}
	case "US", "SS", "UL", "SL", "FL", "FD", "SV", "UV":
		value, err = numericValue(vr, elem.Value)
	case "AT":
		value, err = atValue(elem.Value)
	case "IS", "DS":
		value, err = decimalValue(vr, elem.Value)
	case "PN":
		value, err = pnValue(elem.Value)
	case "AE", "AS", "CS", "DA", "DT", "LO", "LT", "SH", "ST", "TM", "UC", "UI", "UR", "UT":
		value, err = stringValue(vr, elem.Value)
	default:
		return encErr("VR %q không hỗ trợ", vr)
	}
	if err != nil {
		return err
	}
	if !longForm[vr] && len(value) > 0xFFFF {
		return encErr("giá trị quá dài cho VR %s", vr)
	}
	writeElem(w, g, el, vr, value)
	return nil
}

// binaryValue lấy giá trị nhị phân: InlineBinary, hoặc bulkdata (đọc vào bộ nhớ vì phải biết độ dài trước, chỉ cho phần tử nhỏ; bulkdata lớn ngoài PixelData bị từ chối).
func (e *encoder) binaryValue(elem Element, top bool) ([]byte, error) {
	switch {
	case elem.InlineBinary != "":
		data, err := base64.StdEncoding.DecodeString(elem.InlineBinary)
		if err != nil {
			return nil, encErr("InlineBinary không phải base64")
		}
		return data, nil
	case elem.BulkDataURI != "":
		if !top {
			return nil, encErr("BulkDataURI trong SQ không hỗ trợ")
		}
		b, ok := e.resolve(elem.BulkDataURI)
		if !ok {
			return nil, encErr("không có bulkdata cho %s", elem.BulkDataURI)
		}
		if b.Size > maxBulkInMemory {
			return nil, encErr("bulkdata ngoài PixelData quá lớn (%d byte, tối đa %d)", b.Size, maxBulkInMemory)
		}
		data, err := io.ReadAll(io.LimitReader(b.R, maxBulkInMemory+1))
		if err != nil || int64(len(data)) != b.Size {
			return nil, encErr("đọc bulkdata %s lỗi", elem.BulkDataURI)
		}
		return data, nil
	}
	return []byte{}, nil // phần tử rỗng
}

// maxBulkInMemory là trần bulkdata ngoài PixelData (đưa vào bộ nhớ để biết độ dài phần tử): 16 MiB.
const maxBulkInMemory = 16 << 20

func (e *encoder) sequence(w *bytes.Buffer, g, el uint16, elem Element, depth int) error {
	w.Write(binary.LittleEndian.AppendUint16(nil, g))
	w.Write(binary.LittleEndian.AppendUint16(nil, el))
	w.WriteString("SQ")
	w.Write([]byte{0, 0})
	w.Write(binary.LittleEndian.AppendUint32(nil, 0xFFFFFFFF))
	for _, v := range elem.Value {
		m, ok := v.(map[string]any)
		if !ok {
			if d, isDS := v.(Dataset); isDS {
				m = map[string]any{}
				b, _ := json.Marshal(d)
				_ = json.Unmarshal(b, &m)
			} else {
				return encErr("phần tử của SQ phải là dataset")
			}
		}
		item, err := toDataset(m)
		if err != nil {
			return encErr("item SQ không hợp lệ: %v", err)
		}
		w.Write([]byte{0xFE, 0xFF, 0x00, 0xE0})
		w.Write(binary.LittleEndian.AppendUint32(nil, 0xFFFFFFFF))
		if err := e.dataset(w, item, depth+1, false); err != nil {
			return err
		}
		w.Write([]byte{0xFE, 0xFF, 0x0D, 0xE0, 0, 0, 0, 0})
	}
	w.Write([]byte{0xFE, 0xFF, 0xDD, 0xE0, 0, 0, 0, 0})
	return nil
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func numericValue(vr string, vals []any) ([]byte, error) {
	var out []byte
	for _, v := range vals {
		if v == nil {
			return nil, encErr("giá trị null trong %s", vr)
		}
		f, ok := asFloat(v)
		if !ok {
			return nil, encErr("giá trị %v không phải số (VR %s)", v, vr)
		}
		switch vr {
		case "US":
			if f < 0 || f > math.MaxUint16 || f != math.Trunc(f) {
				return nil, encErr("US ngoài miền: %v", f)
			}
			out = binary.LittleEndian.AppendUint16(out, uint16(f))
		case "SS":
			if f < math.MinInt16 || f > math.MaxInt16 || f != math.Trunc(f) {
				return nil, encErr("SS ngoài miền: %v", f)
			}
			out = binary.LittleEndian.AppendUint16(out, uint16(int16(f)))
		case "UL":
			if f < 0 || f > math.MaxUint32 || f != math.Trunc(f) {
				return nil, encErr("UL ngoài miền: %v", f)
			}
			out = binary.LittleEndian.AppendUint32(out, uint32(f))
		case "SL":
			if f < math.MinInt32 || f > math.MaxInt32 || f != math.Trunc(f) {
				return nil, encErr("SL ngoài miền: %v", f)
			}
			out = binary.LittleEndian.AppendUint32(out, uint32(int32(f)))
		case "FL":
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(f)))
		case "FD":
			out = binary.LittleEndian.AppendUint64(out, math.Float64bits(f))
		case "SV":
			out = binary.LittleEndian.AppendUint64(out, uint64(int64(f)))
		case "UV":
			if f < 0 {
				return nil, encErr("UV âm: %v", f)
			}
			out = binary.LittleEndian.AppendUint64(out, uint64(f))
		}
	}
	return out, nil
}

func atValue(vals []any) ([]byte, error) {
	var out []byte
	for _, v := range vals {
		s, ok := v.(string)
		if !ok || len(s) != 8 {
			return nil, encErr("giá trị AT phải là 8 chữ số hex")
		}
		n, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return nil, encErr("giá trị AT %q không phải hex", s)
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(n>>16))
		out = binary.LittleEndian.AppendUint16(out, uint16(n))
	}
	return out, nil
}

func decimalValue(vr string, vals []any) ([]byte, error) {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		var s string
		switch x := v.(type) {
		case float64:
			if vr == "IS" {
				if x != math.Trunc(x) {
					return nil, encErr("IS không nguyên: %v", x)
				}
				s = strconv.FormatInt(int64(x), 10)
			} else {
				s = strconv.FormatFloat(x, 'g', -1, 64)
				if len(s) > 16 {
					s = strconv.FormatFloat(x, 'g', 10, 64)
				}
			}
		case string:
			s = strings.TrimSpace(x)
		default:
			return nil, encErr("giá trị %v không hợp lệ cho %s", v, vr)
		}
		if len(s) > 16 {
			return nil, encErr("giá trị %s dài quá 16 ký tự", vr)
		}
		parts = append(parts, s)
	}
	return padText(strings.Join(parts, "\\"), ' '), nil
}

func pnValue(vals []any) ([]byte, error) {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		switch x := v.(type) {
		case string:
			parts = append(parts, x)
		case map[string]any:
			g := func(k string) string { s, _ := x[k].(string); return s }
			comps := []string{g("Alphabetic"), g("Ideographic"), g("Phonetic")}
			for len(comps) > 1 && comps[len(comps)-1] == "" {
				comps = comps[:len(comps)-1]
			}
			parts = append(parts, strings.Join(comps, "="))
		case PersonName:
			comps := []string{x["Alphabetic"], x["Ideographic"], x["Phonetic"]}
			for len(comps) > 1 && comps[len(comps)-1] == "" {
				comps = comps[:len(comps)-1]
			}
			parts = append(parts, strings.Join(comps, "="))
		case nil:
			parts = append(parts, "")
		default:
			return nil, encErr("giá trị PN không hợp lệ: %v", v)
		}
	}
	return padText(strings.Join(parts, "\\"), ' '), nil
}

func stringValue(vr string, vals []any) ([]byte, error) {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		switch x := v.(type) {
		case string:
			parts = append(parts, x)
		case nil:
			parts = append(parts, "")
		default:
			return nil, encErr("giá trị %v không phải chuỗi (VR %s)", v, vr)
		}
	}
	single := vr == "LT" || vr == "ST" || vr == "UT" || vr == "UR"
	if single && len(parts) > 1 {
		return nil, encErr("VR %s chỉ nhận một giá trị", vr)
	}
	for _, p := range parts {
		if strings.ContainsRune(p, '\\') && !single {
			return nil, encErr("giá trị chứa dấu \\ (VR %s)", vr)
		}
	}
	pad := byte(' ')
	if vr == "UI" {
		pad = 0
	}
	return padText(strings.Join(parts, "\\"), pad), nil
}

func padText(s string, pad byte) []byte {
	b := []byte(s)
	if len(b)%2 == 1 {
		b = append(b, pad)
	}
	return b
}
