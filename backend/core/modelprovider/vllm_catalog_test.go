package modelprovider

import (
	"os"
	"testing"
)

func TestVLLMCatalogHasEndpointButNoHardcodedModels(t *testing.T) {
	raw, err := os.ReadFile("../config/model_catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loadModelCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, supplier := range catalog["model_providers"].Suppliers {
		if supplier.Name != "VLLM" {
			continue
		}
		if supplier.BaseURL != "http://localhost:8000/v1/" || len(supplier.Models) != 0 {
			t.Fatalf("vLLM must discover its own models: endpoint=%q, model count=%d", supplier.BaseURL, len(supplier.Models))
		}
		return
	}
	t.Fatal("vLLM supplier missing")
}
