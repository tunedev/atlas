package tools_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

const logLine = "Lighthouse keeper at Fastnet Rock 2019"

func fileText(t *testing.T, path string, max int64) (map[string]any, error) {
	t.Helper()
	out, err := tools.NewFileText(max).Invoke(context.Background(), map[string]string{"path": path})
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

func TestFileTextReadsAPDFAndADOCX(t *testing.T) {
	for name, body := range map[string][]byte{
		"log.pdf":  pdfWith(logLine, true, ""),
		"log.docx": docxWith(t, logLine),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, name, body)
			out, err := fileText(t, path, 1<<20)
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if !strings.Contains(out["text"].(string), logLine) {
				t.Errorf("text = %q", out["text"])
			}
			sum := sha256.Sum256(body)
			if out["sha256"] != hex.EncodeToString(sum[:]) {
				t.Errorf("sha256 = %v", out["sha256"])
			}
			if out["name"] != name {
				t.Errorf("name = %v", out["name"])
			}
		})
	}
}

func TestFileTextRecoversAPDFWithNoXrefTable(t *testing.T) {
	out, err := fileText(t, writeFile(t, "log.pdf", pdfWith(logLine, false, "")), 1<<20)
	if err != nil {
		t.Fatalf("a PDF a reader can rebuild was refused: %v", err)
	}
	if !strings.Contains(out["text"].(string), logLine) {
		t.Errorf("text = %q", out["text"])
	}
}

func TestFileTextRefusesAPDFWithNoTextLayer(t *testing.T) {
	path := writeFile(t, "scan.pdf", pdfWith("", true, "0 0 m 100 100 l S"))
	_, err := fileText(t, path, 1<<20)
	if err == nil {
		t.Fatal("a PDF with no text was accepted; the model would be asked to extract from nothing")
	}
	if !strings.Contains(err.Error(), "no extractable text") {
		t.Errorf("error does not say why: %v", err)
	}
}

func TestFileTextRefusesAnUnsupportedFormatAndAnOversizedFile(t *testing.T) {
	if _, err := fileText(t, writeFile(t, "log.txt", []byte(logLine)), 1<<20); err == nil {
		t.Error("a plain text file was accepted as a document")
	}
	if _, err := fileText(t, writeFile(t, "log.pdf", pdfWith(logLine, true, "")), 16); err == nil {
		t.Error("a file over the limit was read")
	}
}
