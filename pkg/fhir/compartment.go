package fhir

import "slices"

// PatientCompartment trả về, cho mỗi resource type thuộc compartment Patient, các search parameter
// (ví dụ Observation: subject, performer) mà nếu trỏ tới một Patient thì resource đó thuộc compartment của Patient ấy.
func PatientCompartment() map[string][]string {
	out := make(map[string][]string, len(patientCompartmentData))
	for t, ps := range patientCompartmentData {
		out[t] = slices.Clone(ps)
	}
	return out
}
