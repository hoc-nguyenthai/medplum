package bootstrap_test

import (
	"context"
	"os"
	"path/filepath"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirstore"
)

// fhirrouterSearch liệt kê mọi resource của một loại bằng kho gốc.
func fhirrouterSearch(st *fhirstore.Store, rt string) ([]fhir.Resource, error) {
	sr, err := fhirsearch.Parse(rt, nil)
	if err != nil {
		return nil, err
	}
	sr.Count = 100
	var res *fhirrouter.SearchResult
	if res, err = st.Search(context.Background(), sr); err != nil {
		return nil, err
	}
	return res.Matches, nil
}

// blobCount đếm tệp nội dung trong kho `file:`.
func blobCount(t interface{ Helper() }, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}
