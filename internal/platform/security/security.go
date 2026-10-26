// Package security implementa los verificadores de contraseña (Argon2id) y
// los tokens de sesión. No conoce usuarios ni bases de datos.
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params son los parámetros de Argon2id.
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

// DefaultArgon2 son los mínimos de OWASP (19 MiB, t=2, p=1), adoptados en
// ADR-0008 tras medir en equipos modestos.
var DefaultArgon2 = Argon2Params{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}

var ErrMalformedHash = errors.New("verificador de contraseña con formato inválido")

// Hasher genera y comprueba verificadores en formato PHC:
// $argon2id$v=19$m=19456,t=2,p=1$<sal>$<hash>
type Hasher struct{ Params Argon2Params }

func (h Hasher) Hash(password string) (string, error) {
	p := h.Params
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify compara en tiempo constante. needsRehash indica que el verificador
// usa parámetros distintos de los actuales y conviene regenerarlo.
func (h Hasher) Verify(password, encoded string) (ok, needsRehash bool, err error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(key)))
	ok = subtle.ConstantTimeCompare(got, key) == 1
	want := h.Params
	needsRehash = p.MemoryKiB != want.MemoryKiB || p.Iterations != want.Iterations || p.Parallelism != want.Parallelism ||
		uint32(len(salt)) != want.SaltLen || uint32(len(key)) != want.KeyLen
	return ok, needsRehash, nil
}

func decode(encoded string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &p.Parallelism); err != nil {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	// Límites defensivos: un verificador alterado no debe agotar la memoria.
	if p.MemoryKiB < 8 || p.MemoryKiB > 1<<20 || p.Iterations < 1 || p.Iterations > 100 || p.Parallelism < 1 {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
}

// Tokens genera tokens de sesión opacos. Solo su SHA-256 se guarda en la
// base: copiarla no permite suplantar sesiones.
type Tokens struct{}

// New devuelve un token de 256 bits aleatorios y su resumen.
func (Tokens) New() (token, digest string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = "bps_" + base64.RawURLEncoding.EncodeToString(b)
	return token, Tokens{}.Digest(token), nil
}

func (Tokens) Digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
