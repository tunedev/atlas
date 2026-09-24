package tools_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"testing"
)

// pdfWith builds a one-page PDF drawing text in Helvetica. withXref false
// omits the cross-reference table, which a reader must rebuild. content
// replaces the drawing operators entirely when non-empty.
func pdfWith(text string, withXref bool, content string) []byte {
	if content == "" {
		content = fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	if withXref {
		fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
		for _, o := range offsets {
			fmt.Fprintf(&b, "%010d 00000 n \n", o)
		}
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// docxWith builds a minimal DOCX holding one paragraph of text.
func docxWith(t *testing.T, text string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, body := range parts {
		w, err := z.Create(name)
		if err != nil {
			t.Fatalf("zip %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip %s: %v", name, err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return b.Bytes()
}
