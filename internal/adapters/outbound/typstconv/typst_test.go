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
	return converterWithLimit(t, timeout, 1<<24)
}

func converterWithLimit(t *testing.T, timeout time.Duration, maxBytes int64) *typstconv.Converter {
	t.Helper()
	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst is not on PATH")
	}
	c, err := typstconv.New(typstconv.Config{Bin: "typst", Timeout: timeout, MaxBytes: maxBytes})
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

func TestAPackageImportIsRefused(t *testing.T) {
	var out bytes.Buffer
	err := converter(t, time.Minute).Convert(context.Background(), &out, strings.NewReader(`#import "@preview/tablex:0.0.8": tablex`))
	if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "failed to download package") {
		t.Errorf("err %v, wrote %d bytes; want a refused download and nothing written", err, out.Len())
	}
}

func TestAnOversizeOutputNamesTheLimit(t *testing.T) {
	var out bytes.Buffer
	src := strings.Repeat("= A lighthouse log\nKept the light through the storm.\n#pagebreak()\n", 200)
	err := converterWithLimit(t, time.Minute, 1024).Convert(context.Background(), &out, strings.NewReader(src))
	if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "exceeds 1024 bytes") {
		t.Errorf("err %v, wrote %d bytes; want the limit named and nothing written", err, out.Len())
	}
}

func TestNewNamesAMissingBinary(t *testing.T) {
	_, err := typstconv.New(typstconv.Config{Bin: "no-such-typst-binary", Timeout: time.Second, MaxBytes: 1})
	if err == nil || !strings.Contains(err.Error(), "no-such-typst-binary") {
		t.Errorf("err = %v", err)
	}
}
