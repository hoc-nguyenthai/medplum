// Package dicomweb là phần thuần (không HTTP, không CSDL, không thư viện DICOM) của DICOMweb: mô hình DICOM JSON (PS3.18 F.2), chuyển đổi ngày, giờ,
// tên người, ánh xạ giữa DICOM JSON và resource FHIR DicomStudy, DicomSeries, làm sạch metadata trước khi lưu, tính số liệu tổng hợp và đọc tham số
// phân trang QIDO. Port từ dicom/utils.ts của Medplum, đã sửa các lỗi ghi ở docs/plans/cds-dicom/dicom.md mục 3.
//
// Đọc tệp DICOM thật (thư viện suyashkumar/dicom, bộ quét luồng) và STOW/QIDO/WADO nằm ở các bước sau (D3, D4); gói này chỉ nhận và trả cấu trúc đã có.
package dicomweb

import (
	"encoding/base64"
	"regexp"
	"strings"
)

// Element là một phần tử của DICOM JSON model. Value là danh sách giá trị: string, số (int, int64, float64), tên người (PersonName), Dataset (phần tử của
// SQ) hoặc []byte (nhị phân thô, trước khi CleanDataset đổi thành InlineBinary). InlineBinary là base64 của dữ liệu nhị phân nhỏ.
type Element struct {
	VR           string `json:"vr"`
	Value        []any  `json:"Value,omitempty"`
	InlineBinary string `json:"InlineBinary,omitempty"`
	BulkDataURI  string `json:"BulkDataURI,omitempty"`
}

// Dataset là một tập phần tử, khóa là tag dạng 8 chữ số hex ("00100010").
type Dataset map[string]Element

// PersonName là giá trị của phần tử VR PN trong DICOM JSON. Chỉ dùng thành phần Alphabetic (như Medplum).
type PersonName map[string]string

// Các tag dùng trong ánh xạ study và series.
const (
	TagStudyInstanceUID                = "0020000D"
	TagSeriesInstanceUID               = "0020000E"
	TagStudyID                         = "00200010"
	TagSeriesNumber                    = "00200011"
	TagStudyDate                       = "00080020"
	TagStudyTime                       = "00080030"
	TagAccessionNumber                 = "00080050"
	TagInstanceAvailability            = "00080056"
	TagModalitiesInStudy               = "00080061"
	TagModality                        = "00080060"
	TagReferringPhysicianName          = "00080090"
	TagSeriesDescription               = "0008103E"
	TagTimezoneOffsetFromUTC           = "00080201"
	TagPatientName                     = "00100010"
	TagPatientID                       = "00100020"
	TagPatientBirthDate                = "00100030"
	TagPatientSex                      = "00100040"
	TagNumberOfStudyRelatedSeries      = "00201206"
	TagNumberOfStudyRelatedInstances   = "00201208"
	TagNumberOfSeriesRelatedInstances  = "00201209"
	TagPerformedProcedureStepStartDate = "00400244"
	TagPerformedProcedureStepStartTime = "00400245"
	tagPixelData                       = "7FE00010"
	maxInlineBinaryBytes               = 10 * 1024
	maxCleanDepth                      = 32
)

var (
	inlineBinaryVRs = map[string]bool{"OB": true, "OD": true, "OF": true, "OL": true, "OV": true, "OW": true, "UN": true}
	group0002       = regexp.MustCompile(`(?i)^0002[0-9A-F]{4}$`)
	overlayData     = regexp.MustCompile(`(?i)^60[0-9A-F]{2}3000$`)
)

// CleanDataset trả bản sao của dataset đã làm sạch để lưu thành metadata (cleanDicomJsonDict của Medplum): bỏ nhóm 0002 (file meta), PixelData (7FE0,0010),
// overlay (60xx,3000); nhị phân (VR OB, OD, OF, OL, OV, OW, UN) lớn hơn 10 KB bị bỏ cả phần tử, nhỏ hơn thì thành InlineBinary (base64); phần tử có Value
// là một mục rỗng (`[{}]`, dấu vết của bộ đọc lỗi) mất Value; SQ được làm sạch đệ quy trong từng item. Giá trị không phải Dataset trong SQ giữ nguyên.
// Không sửa dataset đầu vào. Lồng sâu quá 32 mức bị cắt bỏ (chống tệp độc, d0-spike.md 3.1; bộ đọc ở D3 đã chặn từ trước).
func CleanDataset(ds Dataset) Dataset { return cleanDataset(ds, 0) }

func cleanDataset(ds Dataset, depth int) Dataset {
	out := make(Dataset, len(ds))
	for tag, el := range ds {
		if group0002.MatchString(tag) || strings.EqualFold(tag, tagPixelData) || overlayData.MatchString(tag) {
			continue
		}
		fixed, keep := cleanElement(el, depth)
		if keep {
			out[tag] = fixed
		}
	}
	return out
}

func cleanElement(el Element, depth int) (Element, bool) {
	out := el
	if el.VR == "SQ" && len(el.Value) > 0 {
		if depth >= maxCleanDepth {
			return Element{}, false
		}
		out.Value = make([]any, len(el.Value))
		for i, item := range el.Value {
			if d, ok := item.(Dataset); ok {
				out.Value[i] = cleanDataset(d, depth+1)
			} else {
				out.Value[i] = item
			}
		}
		return out, true
	}
	if inlineBinaryVRs[el.VR] && len(el.Value) > 0 {
		switch first := el.Value[0].(type) {
		case []byte:
			if len(first) > maxInlineBinaryBytes {
				return Element{}, false
			}
			out.Value = nil
			out.InlineBinary = base64.StdEncoding.EncodeToString(first)
		case Dataset:
			if len(first) == 0 {
				out.Value = nil
			}
		case map[string]any:
			if len(first) == 0 {
				out.Value = nil
			}
		}
	}
	return out, true
}
