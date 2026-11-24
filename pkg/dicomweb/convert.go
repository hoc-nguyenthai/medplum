package dicomweb

import (
	"regexp"
	"strings"
	"time"
)

// Cùng biểu thức với validationRegexes của Medplum (FHIR date và time), để giá trị đưa vào resource luôn qua được bước kiểm hợp lệ.
var (
	fhirDate = regexp.MustCompile(`^(\d(\d(\d[1-9]|[1-9]0)|[1-9]00)|[1-9]000)(-(0[1-9]|1[0-2])(-(0[1-9]|[1-2]\d|3[0-1]))?)?$`)
	fhirTime = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d:([0-5]\d|60)(\.\d{1,9})?$`)
)

// DateToFHIR đổi DICOM DA (YYYYMMDD) sang FHIR date (YYYY-MM-DD). Giá trị không đúng dạng hoặc không phải ngày có thật trả "" (bỏ thuộc tính tùy chọn
// thay vì để cả yêu cầu bị từ chối vì không qua kiểm hợp lệ). Chặt hơn Medplum ở chỗ loại cả ngày không tồn tại như 20240230.
func DateToFHIR(da string) string {
	if len(da) != 8 {
		return ""
	}
	d := da[0:4] + "-" + da[4:6] + "-" + da[6:8]
	if !fhirDate.MatchString(d) {
		return ""
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return ""
	}
	return d
}

// DateToDICOM đổi FHIR date (YYYY-MM-DD) sang DICOM DA (YYYYMMDD).
func DateToDICOM(date string) string { return strings.ReplaceAll(date, "-", "") }

// TimeToFHIR đổi DICOM TM (HHMMSS, có thể kèm phần thập phân) sang FHIR time (HH:MM:SS, bỏ phần thập phân như Medplum). Giá trị không hợp lệ trả "".
// Giây 60 (giây nhuận) và 00:00:00 đều hợp lệ.
func TimeToFHIR(tm string) string {
	if len(tm) < 6 {
		return ""
	}
	t := tm[0:2] + ":" + tm[2:4] + ":" + tm[4:6]
	if !fhirTime.MatchString(t) {
		return ""
	}
	return t
}

// TimeToDICOM đổi FHIR time (HH:MM:SS) sang DICOM TM (HHMMSS).
func TimeToDICOM(t string) string { return strings.ReplaceAll(t, ":", "") }

// PersonNameToString lấy tên (thành phần Alphabetic, đã cắt khoảng trắng) từ giá trị của phần tử PN; không có thì "".
func PersonNameToString(value []any) string {
	if len(value) == 0 {
		return ""
	}
	switch pn := value[0].(type) {
	case PersonName:
		return strings.TrimSpace(pn["Alphabetic"])
	case map[string]string:
		return strings.TrimSpace(pn["Alphabetic"])
	case map[string]any:
		if s, ok := pn["Alphabetic"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// StringToPersonName dựng giá trị của phần tử PN từ một tên; tên rỗng trả nil.
func StringToPersonName(name string) []any {
	if name == "" {
		return nil
	}
	return []any{PersonName{"Alphabetic": name}}
}
