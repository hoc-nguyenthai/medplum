package fhirbinary_test

import "trivita-fhir/pkg/fhir"

func binaryNoContent(id string) *fhir.Binary { return &fhir.Binary{ID: id, ContentType: "text/plain"} }
func binaryWithData(id, ct, data string) *fhir.Binary {
	return &fhir.Binary{ID: id, ContentType: ct, Data: data}
}
