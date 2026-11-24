package fhirdownload_test

import (
	"encoding/json"

	"trivita-fhir/pkg/fhir"
)

func fhirJSON(r fhir.Resource) ([]byte, error) { return json.Marshal(r) }
