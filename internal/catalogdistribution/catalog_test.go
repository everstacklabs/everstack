package catalogdistribution

import "testing"

func TestValidateCatalogDocumentsRejectsEmptyCatalogShape(t *testing.T) {
	err := ValidateCatalogDocuments([]byte("models: signed\n"), []byte("providers: signed\n"))
	if err == nil {
		t.Fatal("ValidateCatalogDocuments() error = nil")
	}
}

func TestValidateCatalogDocumentsAcceptsCompleteCatalog(t *testing.T) {
	err := ValidateCatalogDocuments(
		[]byte("providers:\n  example:\n    models:\n      - name: example-model\n"),
		[]byte("providers:\n  example:\n    display_name: Example\n"),
	)
	if err != nil {
		t.Fatalf("ValidateCatalogDocuments() error = %v", err)
	}
}
