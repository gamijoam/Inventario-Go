package catalog_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

var now = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func TestBarcodeValidatesGTINCheckDigit(t *testing.T) {
	valid := map[string]string{
		"7591234567894":  "07591234567894", // EAN-13
		"012345678905":   "00012345678905", // UPC-A
		"96385074":       "00000096385074", // EAN-8
		"10012345678902": "10012345678902", // GTIN-14
		"ABC-123/x":      "ABC-123/x",      // interno alfanumérico
		"1234567":        "1234567",        // numérico interno de 7 dígitos
	}
	for in, key := range valid {
		b, err := catalog.ParseBarcode(" " + in + " ")
		if err != nil || b.String() != in || b.Key() != key {
			t.Errorf("%s: %v %q %v", in, b, b.Key(), err)
		}
	}
	for _, bad := range []string{"7591234567893", "012345678900", "96385075", "", "con espacio", "ñ123", "123456789012345678901234567890123456789012345678X"} {
		if _, err := catalog.ParseBarcode(bad); !errors.Is(err, catalog.ErrInvalid) {
			t.Errorf("%q aceptado", bad)
		}
	}
	upc, _ := catalog.ParseBarcode("012345678905")
	ean, _ := catalog.ParseBarcode("0012345678905")
	if upc.Key() != ean.Key() {
		t.Error("UPC-A y su EAN-13 equivalente deben coincidir")
	}
}

func TestSKUIsAReferenceNotAnID(t *testing.T) {
	s, err := catalog.ParseSKU(" har-pan/01 ")
	if err != nil || s.String() != "HAR-PAN/01" {
		t.Fatalf("%v %v", s, err)
	}
	for _, bad := range []string{"", "-X", "con espacio", "ÑU", "A23456789012345678901234567890123456789012"} {
		if _, err := catalog.ParseSKU(bad); !errors.Is(err, catalog.ErrInvalid) {
			t.Errorf("%q aceptado", bad)
		}
	}
	if catalog.AutoSKU(42).String() != "P-000042" {
		t.Error(catalog.AutoSKU(42))
	}
}

func TestProductAndBarcodes(t *testing.T) {
	o, _ := org.NewOrganization("ORG", "Org", now)
	und, err := catalog.NewUnit(o, "und", "Unidad", 0, now)
	if err != nil || und.Code != "UND" {
		t.Fatal(und, err)
	}
	sku, _ := catalog.ParseSKU("HARINA")
	b1, _ := catalog.ParseBarcode("7591234567894")
	b1again, _ := catalog.ParseBarcode("07591234567894")
	p, err := catalog.NewProduct(o, sku, "Harina  de maíz", und, []catalog.Barcode{b1, b1again}, now)
	if err != nil || len(p.Barcodes) != 1 || p.Name.String() != "Harina de maíz" || p.Revision != 1 || p.Status != catalog.StatusActive {
		t.Fatalf("%+v %v", p, err)
	}
	b2, _ := catalog.ParseBarcode("INT-01")
	p2, err := p.WithBarcodes([]catalog.Barcode{b2}, []catalog.Barcode{b1again})
	if err != nil || len(p2.Barcodes) != 1 || p2.Barcodes[0].String() != "INT-01" || len(p.Barcodes) != 1 {
		t.Fatalf("%+v %v", p2, err)
	}
	if _, err := p2.WithBarcodes(nil, []catalog.Barcode{b1}); !errors.Is(err, catalog.ErrInvalid) {
		t.Errorf("quitar un código ausente: %v", err)
	}
	other, _ := org.NewOrganization("OTRA", "Otra", now)
	if _, err := catalog.NewProduct(other, sku, "X", und, nil, now); !errors.Is(err, catalog.ErrInvalid) {
		t.Errorf("unidad de otra organización: %v", err)
	}
	if _, err := catalog.NewUnit(o, "kg", "Kilo", 7, now); !errors.Is(err, catalog.ErrInvalid) {
		t.Errorf("escala 7: %v", err)
	}
	kg, _ := catalog.NewUnit(o, "KG", "Kilogramo", 3, now)
	if kg.Quantity().Scale() != 3 {
		t.Error("escala de la unidad")
	}
}

func TestSearchKeyIgnoresCaseAndAccents(t *testing.T) {
	if got := catalog.SearchKey("  Café   ÑANDÚ Über "); got != "cafe nandu uber" {
		t.Errorf("%q", got)
	}
}

func TestDefaultUnitsAreValid(t *testing.T) {
	o, _ := org.NewOrganization("ORG", "Org", now)
	seen := map[string]bool{}
	for _, u := range catalog.DefaultUnits() {
		if _, err := catalog.NewUnit(o, u.Code, u.Name, u.Scale, now); err != nil || seen[u.Code] {
			t.Errorf("%+v: %v", u, err)
		}
		seen[u.Code] = true
	}
	if !seen[catalog.DefaultUnitCode] {
		t.Error("falta la unidad por defecto")
	}
}
