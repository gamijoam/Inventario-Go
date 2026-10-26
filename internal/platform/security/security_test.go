package security_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gamijoam/inventario-go/internal/platform/security"
)

func TestHashAndVerify(t *testing.T) {
	h := security.Hasher{Params: security.DefaultArgon2}
	enc, err := h.Hash("cafe con leche")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(enc, "cafe") {
		t.Fatalf("formato: %s", enc)
	}
	enc2, _ := h.Hash("cafe con leche")
	if enc == enc2 {
		t.Fatal("cada verificador debe tener sal propia")
	}
	if ok, rehash, err := h.Verify("cafe con leche", enc); !ok || rehash || err != nil {
		t.Fatalf("correcta: %v %v %v", ok, rehash, err)
	}
	if ok, _, err := h.Verify("cafe con lechE", enc); ok || err != nil {
		t.Fatalf("incorrecta: %v %v", ok, err)
	}
	stronger := security.Hasher{Params: security.Argon2Params{MemoryKiB: 32 * 1024, Iterations: 3, Parallelism: 1, SaltLen: 16, KeyLen: 32}}
	if ok, rehash, _ := stronger.Verify("cafe con leche", enc); !ok || !rehash {
		t.Fatalf("parámetros antiguos: ok=%v rehash=%v", ok, rehash)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h := security.Hasher{Params: security.DefaultArgon2}
	for _, enc := range []string{
		"",
		"cafe con leche",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=16$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=99999999,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$***$aGFzaGhhc2hoYXNoaGFzaA",
	} {
		if _, _, err := h.Verify("x", enc); !errors.Is(err, security.ErrMalformedHash) {
			t.Errorf("%q: %v", enc, err)
		}
	}
}

func TestTokens(t *testing.T) {
	tok, digest, err := security.Tokens{}.New()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "bps_") || len(tok) != 47 || len(digest) != 64 || strings.Contains(digest, tok[4:]) {
		t.Fatalf("token=%q digest=%q", tok, digest)
	}
	if (security.Tokens{}).Digest(tok) != digest {
		t.Fatal("el resumen debe ser determinista")
	}
	tok2, _, _ := security.Tokens{}.New()
	if tok == tok2 {
		t.Fatal("tokens repetidos")
	}
}

// BenchmarkHash mide el costo de un verificador con los parámetros por
// defecto (registrado en ADR-0008).
func BenchmarkHash(b *testing.B) {
	h := security.Hasher{Params: security.DefaultArgon2}
	for b.Loop() {
		if _, err := h.Hash("cafe con leche"); err != nil {
			b.Fatal(err)
		}
	}
}
