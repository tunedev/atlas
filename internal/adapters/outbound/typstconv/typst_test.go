package typstconv_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/typstconv"
)

func converter(t *testing.T, timeout time.Duration) *typstconv.Converter {
	t.Helper()
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst is not on PATH")
	}
	c, err := typstconv.New(typstconv.Config{Bin: "typst", Timeout: timeout, MaxBytes: 1 << 24})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConvertProducesAPDF(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Minute).Convert(context.Background(), &out, strings.NewReader("= A lighthouse log\nKept the light."))
	if err != nil || !bytes.HasPrefix(out.Bytes(), []byte("%PDF-")) {
		t.Fatalf("err %v, %d bytes", err, out.Len())
	}
}

func TestAFailedConversionWritesNothing(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Minute).Convert(context.Background(), &out, strings.NewReader("#let x = "))
	if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "expected expression") {
		t.Errorf("err %v, wrote %d bytes; want typst's own message and nothing written", err, out.Len())
	}
}

func TestATemplateCannotReadFiles(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Minute).Convert(context.Background(), &out, strings.NewReader(`#read("/etc/hostname")`))
	if err == nil || out.Len() != 0 {
		t.Errorf("err %v, wrote %d bytes; a read outside the empty root must fail", err, out.Len())
	}
}

func TestConvertHonoursTheTimeout(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Nanosecond).Convert(context.Background(), &out, strings.NewReader("= slow"))
	if err == nil || out.Len() != 0 {
		t.Errorf("err %v; want the timeout", err)
	}
}

func TestNewNamesAMissingBinary(t *testing.T) {
	_, err := typstconv.New(typstconv.Config{Bin: "no-such-typst-binary", Timeout: time.Second, MaxBytes: 1})
	if err == nil || !strings.Contains(err.Error(), "no-such-typst-binary") {
		t.Errorf("err = %v", err)
	}
}
