// Package id genera y valida los identificadores técnicos globales.
//
// Se usan UUIDv7 (RFC 9562) generados en Go: ordenables por tiempo,
// independientes de autoincrementales de SQLite/PostgreSQL y aptos para
// operar sin conexión y sincronizar sin colisiones (sección 25.5, ADR-0003).
// No sustituyen a identificadores comerciales como SKU, RIF o número de factura.
package id

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrInvalidID = errors.New("identificador inválido")

// ID es un UUID de 16 bytes. El valor cero significa "sin asignar".
type ID [16]byte

// Parse acepta la forma 8-4-4-4-12 en hexadecimal (mayúsculas o minúsculas)
// con variante RFC 9562. Rechaza el UUID nulo.
func Parse(s string) (ID, error) {
	var v ID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return ID{}, fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	hexDigits := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(v[:], []byte(hexDigits)); err != nil {
		return ID{}, fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	if v.IsZero() {
		return ID{}, fmt.Errorf("%w: UUID nulo", ErrInvalidID)
	}
	if v[8]>>6 != 0b10 {
		return ID{}, fmt.Errorf("%w: variante no RFC 9562 en %q", ErrInvalidID, s)
	}
	return v, nil
}

func (v ID) IsZero() bool     { return v == ID{} }
func (v ID) Bytes() [16]byte  { return v }
func (v ID) Compare(o ID) int { return bytes.Compare(v[:], o[:]) }

// Time devuelve el instante (precisión de milisegundos) de un UUIDv7.
func (v ID) Time() time.Time {
	ms := int64(v[0])<<40 | int64(v[1])<<32 | int64(v[2])<<24 | int64(v[3])<<16 | int64(v[4])<<8 | int64(v[5])
	return time.UnixMilli(ms).UTC()
}

// String devuelve la forma canónica en minúsculas.
func (v ID) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], v[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], v[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], v[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], v[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], v[10:16])
	return string(buf[:])
}

// Generator produce UUIDv7 estrictamente crecientes dentro de un proceso:
// en el mismo milisegundo, o si el reloj retrocede, incrementa un contador
// de 12 bits (rand_a) y, al agotarlo, avanza el milisegundo lógico
// (RFC 9562, sección 6.2, método 1).
type Generator struct {
	mu      sync.Mutex
	now     func() time.Time
	lastMS  int64
	counter uint16
}

// NewGenerator crea un generador con el reloj indicado.
func NewGenerator(now func() time.Time) *Generator {
	return &Generator{now: now}
}

var defaultGenerator = NewGenerator(time.Now)

// New genera un UUIDv7 con el generador del proceso.
func New() (ID, error) { return defaultGenerator.New() }

// MustNew es New; entra en pánico si falla la fuente aleatoria del sistema.
func MustNew() ID { return defaultGenerator.MustNew() }

func (g *Generator) New() (ID, error) {
	var v ID
	if _, err := rand.Read(v[6:]); err != nil {
		return ID{}, fmt.Errorf("fuente aleatoria: %w", err)
	}

	g.mu.Lock()
	ms := g.now().UnixMilli()
	if ms > g.lastMS {
		g.lastMS = ms
		// Semilla aleatoria en la mitad baja: deja margen para incrementar.
		g.counter = uint16(v[6])<<8 | uint16(v[7])
		g.counter &= 0x07ff
	} else {
		g.counter++
		if g.counter > 0x0fff {
			g.lastMS++
			g.counter = 0
		}
	}
	ms, counter := g.lastMS, g.counter
	g.mu.Unlock()

	v[0] = byte(ms >> 40)
	v[1] = byte(ms >> 32)
	v[2] = byte(ms >> 24)
	v[3] = byte(ms >> 16)
	v[4] = byte(ms >> 8)
	v[5] = byte(ms)
	v[6] = 0x70 | byte(counter>>8)
	v[7] = byte(counter)
	v[8] = 0x80 | v[8]&0x3f
	return v, nil
}

func (g *Generator) MustNew() ID {
	v, err := g.New()
	if err != nil {
		panic(err)
	}
	return v
}
