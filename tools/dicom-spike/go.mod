// Module riêng (không thuộc module trivita-fhir) để spike D0 không kéo thư viện DICOM vào go.mod chính.
module trivita-dicom-spike

go 1.26

require github.com/suyashkumar/dicom v1.1.0

require (
	golang.org/x/exp v0.0.0-20240525044651-4c93da0ed11d // indirect
	golang.org/x/text v0.3.8 // indirect
)
