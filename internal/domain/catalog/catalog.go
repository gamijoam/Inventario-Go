// Package catalog modela el catálogo básico de DEC-007.A: productos simples
// con referencia comercial (SKU), códigos de barras y unidad base.
//
// El ID interno (UUIDv7) es estable e invisible; el SKU es la referencia
// que usan las personas y puede cambiar; los códigos de barras identifican
// lo que lee el escáner. Los tres son datos distintos (sección 13.4). El
// catálogo pertenece a la organización y lo comparten sus empresas
// (DEC-008.B, ratificado para V1).
package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/quantity"
)

var ErrInvalid = errors.New("dato de catálogo inválido")

// Unit es una unidad de medida de la organización. Su escala (decimales
// admitidos) no cambia después de creada: las cantidades guardadas dependen
// de ella.
type Unit struct {
	ID             id.ID
	OrganizationID id.ID
	Code           string
	Name           org.Name
	Scale          int32
	CreatedAt      time.Time
}

// UnitTemplate es una unidad sembrada al configurar la organización.
type UnitTemplate struct {
	Code, Name string
	Scale      int32
}

// DefaultUnitCode es la unidad de un producto si no se indica otra.
const DefaultUnitCode = "UND"

// DefaultUnits son las unidades base (DEC-007.C, ratificado para V1). La
// migración 0004 siembra las mismas en organizaciones existentes.
func DefaultUnits() []UnitTemplate {
	return []UnitTemplate{
		{"UND", "Unidad", 0},
		{"KG", "Kilogramo", 3},
		{"G", "Gramo", 0},
		{"L", "Litro", 3},
		{"ML", "Mililitro", 0},
		{"M", "Metro", 3},
		{"CAJA", "Caja", 0},
		{"PAQ", "Paquete", 0},
	}
}

func NewUnit(o org.Organization, code, name string, scale int32, now time.Time) (Unit, error) {
	qu, err := quantity.NewUnit(strings.ToUpper(strings.TrimSpace(code)), scale)
	if err != nil {
		return Unit{}, fmt.Errorf("%w: %v (código de 1 a 16 caracteres A-Z o 0-9; de 0 a 6 decimales)", ErrInvalid, err)
	}
	n, err := org.ParseName(name)
	if err != nil {
		return Unit{}, err
	}
	uid, err := id.New()
	if err != nil {
		return Unit{}, err
	}
	return Unit{ID: uid, OrganizationID: o.ID, Code: qu.Code(), Name: n, Scale: scale, CreatedAt: now.UTC()}, nil
}

// Quantity devuelve la unidad para cantidades exactas.
func (u Unit) Quantity() quantity.Unit {
	qu, err := quantity.NewUnit(u.Code, u.Scale)
	if err != nil {
		panic(err) // NewUnit y la base garantizan una unidad válida
	}
	return qu
}

// SKU es la referencia comercial: única en la organización, en mayúsculas.
type SKU struct{ v string }

var skuPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._/-]{0,39}$`)

func ParseSKU(s string) (SKU, error) {
	v := strings.ToUpper(strings.TrimSpace(s))
	if !skuPattern.MatchString(v) {
		return SKU{}, fmt.Errorf("%w: SKU %q (1 a 40 caracteres A-Z, 0-9, punto, guion, guion bajo o barra)", ErrInvalid, s)
	}
	return SKU{v}, nil
}

func (s SKU) String() string { return s.v }
func (s SKU) IsZero() bool   { return s.v == "" }

// AutoSKU es el SKU que se asigna cuando no se indica uno: P-000001…
func AutoSKU(n int64) SKU { return SKU{fmt.Sprintf("P-%06d", n)} }

// Barcode es un código de barras. Key es su forma de búsqueda y unicidad:
// los GTIN (EAN-8, UPC-A, EAN-13, GTIN-14) se normalizan a 14 dígitos, así
// "012345678905" (UPC-A) y "0012345678905" (EAN-13) son el mismo código.
type Barcode struct {
	code, key string
}

var barcodePattern = regexp.MustCompile(`^[\x21-\x7E]{1,48}$`)

// ParseBarcode valida el dígito verificador de los GTIN numéricos de 8, 12,
// 13 o 14 dígitos; otros códigos (internos, Code 128) solo el formato: de 1 a
// 48 caracteres ASCII visibles, sin espacios.
func ParseBarcode(s string) (Barcode, error) {
	v := strings.TrimSpace(s)
	if !barcodePattern.MatchString(v) {
		return Barcode{}, fmt.Errorf("%w: código de barras %q (1 a 48 caracteres visibles, sin espacios)", ErrInvalid, s)
	}
	if !isGTINLength(v) {
		return Barcode{v, v}, nil
	}
	if !validGTIN(v) {
		return Barcode{}, fmt.Errorf("%w: código de barras %q: dígito verificador incorrecto", ErrInvalid, s)
	}
	return Barcode{v, strings.Repeat("0", 14-len(v)) + v}, nil
}

func (b Barcode) String() string { return b.code }
func (b Barcode) Key() string    { return b.key }

func isGTINLength(s string) bool {
	if !slices.Contains([]int{8, 12, 13, 14}, len(s)) {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validGTIN aplica el módulo 10 de GS1: desde la derecha, sin contar el
// verificador, los dígitos alternan pesos 3 y 1.
func validGTIN(s string) bool {
	sum := 0
	for i := len(s) - 2; i >= 0; i-- {
		d := int(s[i] - '0')
		if (len(s)-2-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return (10-sum%10)%10 == int(s[len(s)-1]-'0')
}

// Status indica si el producto puede usarse en operaciones nuevas.
type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
)

// Product es un producto simple. No se borra: se inactiva (sección 13.4).
type Product struct {
	ID             id.ID
	OrganizationID id.ID
	SKU            SKU
	Name           org.Name
	UnitID         id.ID
	Barcodes       []Barcode
	Status         Status
	// Revision crece con cada cambio; sirve para detectar ediciones
	// concurrentes y, más adelante, para sincronizar el catálogo (DEC-006).
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewProduct(o org.Organization, sku SKU, name string, u Unit, barcodes []Barcode, now time.Time) (Product, error) {
	if sku.IsZero() {
		return Product{}, fmt.Errorf("%w: falta el SKU", ErrInvalid)
	}
	if u.OrganizationID != o.ID {
		return Product{}, fmt.Errorf("%w: la unidad es de otra organización", ErrInvalid)
	}
	n, err := org.ParseName(name)
	if err != nil {
		return Product{}, err
	}
	pid, err := id.New()
	if err != nil {
		return Product{}, err
	}
	p := Product{ID: pid, OrganizationID: o.ID, SKU: sku, Name: n, UnitID: u.ID, Status: StatusActive,
		Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if p, err = p.WithBarcodes(barcodes, nil); err != nil {
		return Product{}, err
	}
	return p, nil
}

// MaxBarcodes limita los códigos por producto.
const MaxBarcodes = 20

// WithBarcodes agrega y quita códigos (comparados por Key).
func (p Product) WithBarcodes(add, remove []Barcode) (Product, error) {
	out := slices.Clone(p.Barcodes)
	for _, r := range remove {
		i := slices.IndexFunc(out, func(b Barcode) bool { return b.key == r.key })
		if i < 0 {
			return Product{}, fmt.Errorf("%w: el producto %s no tiene el código %s", ErrInvalid, p.SKU, r)
		}
		out = slices.Delete(out, i, i+1)
	}
	for _, a := range add {
		if slices.ContainsFunc(out, func(b Barcode) bool { return b.key == a.key }) {
			continue
		}
		out = append(out, a)
	}
	if len(out) > MaxBarcodes {
		return Product{}, fmt.Errorf("%w: un producto admite hasta %d códigos de barras", ErrInvalid, MaxBarcodes)
	}
	p.Barcodes = out
	return p, nil
}

// SearchKey normaliza un texto para buscar sin distinguir mayúsculas ni
// acentos: "Café Ñandú" → "cafe nandu".
func SearchKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.Join(strings.Fields(s), " ")) {
		if f, ok := folds[r]; ok {
			r = f
		}
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var folds = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u',
	'ñ': 'n', 'ç': 'c',
}
