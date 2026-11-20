package attachment

import (
	"errors"
	"testing"
)

func TestValidateContent_NFR_SEC_06(t *testing.T) {
	// Reject HTML/script disguised as image
	if _, err := ValidateContent([]byte("<html><script>alert(1)</script></html>")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("expected ErrInvalidFile for HTML payload, got %v", err)
	}
	// Reject empty file
	if _, err := ValidateContent(nil); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("expected ErrInvalidFile for empty payload, got %v", err)
	}
	// Accept valid PNG magic header
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	mime, err := ValidateContent(pngHeader)
	if err != nil || mime != "image/png" {
		t.Fatalf("expected image/png, got mime=%q err=%v", mime, err)
	}
}
